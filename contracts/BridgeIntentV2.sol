// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/access/AccessControl.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title BridgeIntentV2
 * @author Tachyon Team
 * @notice Cross-chain bridge intent contract with competitive solver auctions
 * @dev Users create intents specifying tokens to bridge. Solvers compete by depositing
 *      full bid amounts. Highest bidder wins and must fulfill on destination chain.
 *      Non-winners can withdraw deposits after auction ends.
 * 
 * Flow:
 *   1. User creates intent (deposits tokenA + reward)
 *   2. Solvers place bids (deposit full bid amount in tokenA)
 *   3. Auction ends, anyone can finalize to determine winner
 *   4. Winner fulfills intent on destination chain
 *   5. Relayer verifies and settles on source chain
 *   6. Non-winners withdraw their deposits
 */
contract BridgeIntentV2 is ReentrancyGuard, AccessControl, Pausable {
    using SafeERC20 for IERC20;

    /// @notice Can grant/revoke roles, emergency withdraw, instant cancel
    bytes32 public constant ADMIN_ROLE = keccak256("ADMIN_ROLE");
    
    /// @notice Can call settleIntent after verifying destination chain fulfillment
    bytes32 public constant RELAYER_ROLE = keccak256("RELAYER_ROLE");
    
    /// @notice Can pause/unpause contract operations
    bytes32 public constant PAUSER_ROLE = keccak256("PAUSER_ROLE");
    
    /// @notice Can update protocol configuration (min rewards, etc.)
    bytes32 public constant OPERATOR_ROLE = keccak256("OPERATOR_ROLE");

    /**
     * @notice Lifecycle states of an intent
     * @dev Active -> Finalized -> Completed OR Active -> Cancelled
     */
    enum IntentStatus {
        Active,
        Finalized,
        Completed,
        Cancelled
    }

    /**
     * @notice Core intent data structure
     * @dev Optimized for storage packing - uses 5 slots instead of 13
     *      uint96 supports up to 79B tokens with 18 decimals
     * 
     * @param user Creator of the intent who deposits tokenA
     * @param amountA Amount of source token to be bridged
     * @param tokenA Source chain token address
     * @param expectedAmountB Minimum amount user expects on destination
     * @param tokenB Destination chain token address
     * @param reward Bonus paid to solver on successful completion
     * @param winningSolver Address of auction winner (set after finalize)
     * @param winningBid Winning bid amount (set after finalize)
     * @param destChainId Target blockchain chain ID
     * @param endTime Unix timestamp when auction closes
     * @param createdAt Unix timestamp of intent creation
     * @param status Current lifecycle state
     */
    struct Intent {
        address user;
        uint96 amountA;
        address tokenA;
        uint96 expectedAmountB;
        address tokenB;
        uint96 reward;
        address winningSolver;
        uint96 winningBid;
        uint64 destChainId;
        uint64 endTime;
        uint64 createdAt;
        IntentStatus status;
    }

    /**
     * @notice Tracks auction state for O(1) winner determination
     * @param highestBidder Current leading solver address
     * @param highestBid Current highest bid amount
     * @param bidCount Total unique solvers who have bid
     */
    struct BidState {
        address highestBidder;
        uint96 highestBid;
        uint32 bidCount;
    }

    /// @notice Intent ID -> Intent data
    mapping(uint256 => Intent) public intents;
    
    /// @notice Counter for generating unique intent IDs (starts at 1)
    uint256 public nextIntentId = 1;
    
    /// @notice Intent ID -> current auction state
    mapping(uint256 => BidState) public intentBidState;
    
    /// @notice Intent ID -> solver address -> deposited bid amount
    mapping(uint256 => mapping(address => uint96)) public solverBids;
    
    /// @notice Intent ID -> solver address -> whether they've withdrawn
    mapping(uint256 => mapping(address => bool)) public hasWithdrawn;
    
    /// @dev Array of intent IDs with Active or Finalized status
    uint256[] private activeIntentIds;
    
    /// @dev Intent ID -> index in activeIntentIds array
    mapping(uint256 => uint256) private activeIntentIndex;
    
    /// @dev Intent ID -> whether it's in the active array
    mapping(uint256 => bool) private isActiveIntent;
    
    /// @notice Composite key (sourceChainId, intentId) -> solved status on this chain
    mapping(bytes32 => bool) public solvedIntents;
    
    /// @notice Composite key -> solver who fulfilled on this chain
    mapping(bytes32 => address) public intentSolver;
    
    /// @notice Chain ID this contract is deployed on (immutable)
    uint256 public immutable chainId;
    
    /// @notice Token address -> minimum reward required for intents using that token
    mapping(address => uint96) public minRewardPerToken;
    
    uint64 public constant MIN_AUCTION_DURATION = 30 seconds;
    uint64 public constant MAX_AUCTION_DURATION = 24 hours;
    
    /// @notice Bids within this time before end trigger extension
    uint64 public constant EXTENSION_THRESHOLD = 2 minutes;
    
    /// @notice Duration added when anti-snipe triggers
    uint64 public constant EXTENSION_DURATION = 2 minutes;
    
    /// @notice Minimum bid increment in basis points (100 = 1%)
    uint96 public constant MIN_BID_INCREMENT_BPS = 100;

    event IntentCreated(
        uint256 indexed intentId,
        address indexed user,
        address tokenA,
        address tokenB,
        uint96 amountA,
        uint96 expectedAmountB,
        uint96 reward,
        uint64 destChainId,
        uint64 endTime
    );

    event BidPlaced(
        uint256 indexed intentId,
        address indexed solver,
        uint96 bidAmount,
        uint96 previousHighest,
        uint64 newEndTime
    );

    event BidWithdrawn(
        uint256 indexed intentId,
        address indexed solver,
        uint96 amount
    );

    event AuctionFinalized(
        uint256 indexed intentId,
        address indexed winningSolver,
        uint96 winningBid,
        uint32 totalBids
    );

    event IntentSolvedOnChain2(
        uint256 indexed sourceChainId,
        uint256 indexed intentId,
        address indexed solver,
        address tokenB,
        uint96 amountB
    );

    event IntentSettled(
        uint256 indexed intentId,
        address indexed solver,
        uint96 amountA,
        uint96 reward,
        uint96 winningBid
    );

    event IntentCancelled(
        uint256 indexed intentId,
        address indexed user,
        uint96 refundedAmount
    );

    event MinRewardUpdated(address indexed token, uint96 minReward);

    error InvalidAddress();
    error InvalidAmount();
    error InvalidDuration();
    error InvalidIntentId();
    error AuctionEnded();
    error AuctionNotEnded();
    error IntentNotActive();
    error IntentNotFinalized();
    error IntentAlreadyCompleted();
    error BidTooLow();
    error NotWinningSolver();
    error NotAuthorized();
    error AlreadyWithdrawn();
    error NoBidToWithdraw();
    error TooEarlyToCancel();
    error IntentAlreadySolved();
    error ArrayLengthMismatch();
    error RewardBelowMinimum();

    modifier validIntentId(uint256 _intentId) {
        if (_intentId == 0 || _intentId >= nextIntentId) revert InvalidIntentId();
        _;
    }

    /**
     * @notice Deploy the bridge contract
     * @param _chainId Chain ID where this contract is deployed
     * @param _admin Initial admin address (receives all roles)
     */
    constructor(uint256 _chainId, address _admin) {
        if (_admin == address(0)) revert InvalidAddress();
        chainId = _chainId;
        
        _grantRole(DEFAULT_ADMIN_ROLE, _admin);
        _grantRole(ADMIN_ROLE, _admin);
        _grantRole(RELAYER_ROLE, _admin);
        _grantRole(PAUSER_ROLE, _admin);
        _grantRole(OPERATOR_ROLE, _admin);
    }

    /**
     * @notice Create a cross-chain bridging intent
     * @dev Transfers amountA + reward from caller. Starts auction immediately.
     * @param _tokenA Token to send from this chain
     * @param _tokenB Token to receive on destination chain
     * @param _amountA Amount of tokenA to bridge
     * @param _expectedAmountB Minimum acceptable amount of tokenB
     * @param _reward Solver reward in tokenA (paid on successful settlement)
     * @param _destChainId Destination blockchain chain ID
     * @param _auctionDuration How long solvers can bid (30s - 24h)
     */
    function createIntent(
        address _tokenA,
        address _tokenB,
        uint96 _amountA,
        uint96 _expectedAmountB,
        uint96 _reward,
        uint64 _destChainId,
        uint64 _auctionDuration
    ) external nonReentrant whenNotPaused {
        if (_tokenA == address(0) || _tokenB == address(0)) revert InvalidAddress();
        if (_amountA == 0 || _expectedAmountB == 0) revert InvalidAmount();
        if (_reward == 0) revert InvalidAmount();
        if (_destChainId == 0) revert InvalidAmount();
        if (_auctionDuration < MIN_AUCTION_DURATION || _auctionDuration > MAX_AUCTION_DURATION) {
            revert InvalidDuration();
        }
        
        uint96 minReward = minRewardPerToken[_tokenA];
        if (minReward > 0 && _reward < minReward) revert RewardBelowMinimum();

        uint256 intentId = nextIntentId++;
        uint64 endTime = uint64(block.timestamp) + _auctionDuration;

        IERC20(_tokenA).safeTransferFrom(msg.sender, address(this), uint256(_amountA) + uint256(_reward));

        intents[intentId] = Intent({
            user: msg.sender,
            amountA: _amountA,
            tokenA: _tokenA,
            expectedAmountB: _expectedAmountB,
            tokenB: _tokenB,
            reward: _reward,
            winningSolver: address(0),
            winningBid: 0,
            destChainId: _destChainId,
            endTime: endTime,
            createdAt: uint64(block.timestamp),
            status: IntentStatus.Active
        });

        _addToActive(intentId);

        emit IntentCreated(
            intentId, msg.sender, _tokenA, _tokenB,
            _amountA, _expectedAmountB, _reward, _destChainId, endTime
        );
    }

    /**
     * @notice Bid on an intent by depositing tokenA
     * @dev Deposits full bid amount. Can increase existing bid. Triggers anti-snipe extension.
     * @param _intentId Intent to bid on
     * @param _bidAmount Total bid amount (must be > current highest by at least 1%)
     */
    function placeBid(uint256 _intentId, uint96 _bidAmount) 
        external 
        validIntentId(_intentId) 
        nonReentrant 
        whenNotPaused 
    {
        Intent storage intent = intents[_intentId];
        BidState storage bidState = intentBidState[_intentId];
        
        if (intent.status != IntentStatus.Active) revert IntentNotActive();
        if (block.timestamp >= intent.endTime) revert AuctionEnded();
        if (_bidAmount == 0) revert InvalidAmount();
        
        uint96 currentBid = solverBids[_intentId][msg.sender];
        uint96 currentHighest = bidState.highestBid;
        
        if (_bidAmount <= currentHighest) revert BidTooLow();
        
        if (currentHighest > 0) {
            uint96 minBid = currentHighest + (currentHighest * MIN_BID_INCREMENT_BPS / 10000);
            if (_bidAmount < minBid) revert BidTooLow();
        }
        
        uint96 additionalDeposit = _bidAmount - currentBid;
        if (additionalDeposit > 0) {
            IERC20(intent.tokenA).safeTransferFrom(msg.sender, address(this), additionalDeposit);
        }
        
        solverBids[_intentId][msg.sender] = _bidAmount;
        bidState.highestBidder = msg.sender;
        bidState.highestBid = _bidAmount;
        
        if (currentBid == 0) {
            bidState.bidCount++;
        }
        
        uint64 newEndTime = intent.endTime;
        if (intent.endTime - block.timestamp < EXTENSION_THRESHOLD) {
            newEndTime = uint64(block.timestamp) + EXTENSION_DURATION;
            intent.endTime = newEndTime;
        }

        emit BidPlaced(_intentId, msg.sender, _bidAmount, currentHighest, newEndTime);
    }

    /**
     * @notice Withdraw deposited bid after auction ends
     * @dev Only non-winners can withdraw. Winner's deposit is locked until settlement.
     * @param _intentId Intent to withdraw bid from
     */
    function withdrawBid(uint256 _intentId) 
        external 
        validIntentId(_intentId) 
        nonReentrant 
    {
        Intent storage intent = intents[_intentId];
        
        if (intent.status == IntentStatus.Active) {
            if (block.timestamp < intent.endTime) revert AuctionNotEnded();
        }
        
        if (intent.status == IntentStatus.Finalized && msg.sender == intent.winningSolver) {
            revert NotAuthorized();
        }
        
        uint96 bidAmount = solverBids[_intentId][msg.sender];
        if (bidAmount == 0) revert NoBidToWithdraw();
        if (hasWithdrawn[_intentId][msg.sender]) revert AlreadyWithdrawn();
        
        hasWithdrawn[_intentId][msg.sender] = true;
        solverBids[_intentId][msg.sender] = 0;
        
        IERC20(intent.tokenA).safeTransfer(msg.sender, bidAmount);

        emit BidWithdrawn(_intentId, msg.sender, bidAmount);
    }

    /**
     * @notice End auction and lock in the winner
     * @dev Can be called by anyone after auction ends. Auto-cancels if no bids.
     * @param _intentId Intent to finalize
     */
    function finalizeAuction(uint256 _intentId) external validIntentId(_intentId) {
        Intent storage intent = intents[_intentId];
        BidState storage bidState = intentBidState[_intentId];
        
        if (intent.status != IntentStatus.Active) revert IntentNotActive();
        if (block.timestamp < intent.endTime) revert AuctionNotEnded();
        
        if (bidState.bidCount == 0) {
            _cancelIntent(_intentId);
            return;
        }
        
        intent.winningSolver = bidState.highestBidder;
        intent.winningBid = bidState.highestBid;
        intent.status = IntentStatus.Finalized;

        emit AuctionFinalized(_intentId, bidState.highestBidder, bidState.highestBid, bidState.bidCount);
    }

    /**
     * @notice Fulfill an intent on the destination chain
     * @dev Called by solver on chain2. Transfers tokenB to recipients.
     * @param _sourceChainId Chain where intent was created
     * @param _intentId Intent ID on source chain
     * @param _user Original intent creator (for verification)
     * @param _tokenB Token to distribute
     * @param _expectedAmountB Maximum total distribution amount
     * @param _recipients Addresses to receive tokens
     * @param _amounts Amount for each recipient
     */
    function solveIntentOnChain2(
        uint256 _sourceChainId,
        uint256 _intentId,
        address _user,
        address _tokenB,
        uint96 _expectedAmountB,
        address[] calldata _recipients,
        uint96[] calldata _amounts
    ) external nonReentrant whenNotPaused {
        if (_sourceChainId == 0) revert InvalidAmount();
        if (_intentId == 0) revert InvalidIntentId();
        if (_user == address(0)) revert InvalidAddress();
        if (_tokenB == address(0)) revert InvalidAddress();
        if (_expectedAmountB == 0) revert InvalidAmount();
        if (_recipients.length != _amounts.length) revert ArrayLengthMismatch();
        if (_recipients.length == 0) revert InvalidAmount();

        bytes32 intentKey = keccak256(abi.encodePacked(_sourceChainId, _intentId));
        if (solvedIntents[intentKey]) revert IntentAlreadySolved();

        uint96 totalAmount = 0;
        for (uint256 i = 0; i < _amounts.length; i++) {
            totalAmount += _amounts[i];
        }
        if (totalAmount > _expectedAmountB) revert InvalidAmount();

        IERC20(_tokenB).safeTransferFrom(msg.sender, address(this), totalAmount);

        for (uint256 i = 0; i < _recipients.length; i++) {
            if (_amounts[i] > 0 && _recipients[i] != address(0)) {
                IERC20(_tokenB).safeTransfer(_recipients[i], _amounts[i]);
            }
        }

        solvedIntents[intentKey] = true;
        intentSolver[intentKey] = msg.sender;

        emit IntentSolvedOnChain2(_sourceChainId, _intentId, msg.sender, _tokenB, totalAmount);
    }

    /**
     * @notice Check if an intent has been fulfilled on this chain
     * @param _sourceChainId Source chain ID
     * @param _intentId Intent ID on source chain
     * @return True if solved
     */
    function isIntentSolvedOnChain2(uint256 _sourceChainId, uint256 _intentId) external view returns (bool) {
        return solvedIntents[keccak256(abi.encodePacked(_sourceChainId, _intentId))];
    }

    /**
     * @notice Get address of solver who fulfilled intent on this chain
     * @param _sourceChainId Source chain ID
     * @param _intentId Intent ID on source chain
     * @return Solver address (zero if not solved)
     */
    function getIntentSolver(uint256 _sourceChainId, uint256 _intentId) external view returns (address) {
        return intentSolver[keccak256(abi.encodePacked(_sourceChainId, _intentId))];
    }

    /**
     * @notice Complete intent and pay solver after destination chain verification
     * @dev Only callable by relayer. Transfers amountA + reward to winner.
     * @param _intentId Intent to settle
     */
    function settleIntent(uint256 _intentId) 
        external 
        validIntentId(_intentId) 
        onlyRole(RELAYER_ROLE) 
        nonReentrant 
    {
        Intent storage intent = intents[_intentId];
        
        if (intent.status != IntentStatus.Finalized) revert IntentNotFinalized();
        if (intent.winningSolver == address(0)) revert NotWinningSolver();

        uint256 solverPayout = uint256(intent.amountA) + uint256(intent.reward);
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, solverPayout);

        intent.status = IntentStatus.Completed;
        _removeFromActive(_intentId);

        emit IntentSettled(_intentId, intent.winningSolver, intent.amountA, intent.reward, intent.winningBid);
    }

    /**
     * @notice Cancel intent and refund user
     * @dev User must wait 1 hour after auction ends. Admins can cancel immediately.
     * @param _intentId Intent to cancel
     */
    function cancelIntent(uint256 _intentId) external validIntentId(_intentId) nonReentrant {
        Intent storage intent = intents[_intentId];
        
        bool isUserOrAdmin = msg.sender == intent.user || hasRole(ADMIN_ROLE, msg.sender);
        if (!isUserOrAdmin) revert NotAuthorized();
        
        if (intent.status == IntentStatus.Completed) revert IntentAlreadyCompleted();
        if (intent.status == IntentStatus.Cancelled) revert IntentAlreadyCompleted();
        
        if (!hasRole(ADMIN_ROLE, msg.sender)) {
            if (block.timestamp <= intent.endTime + 1 hours) revert TooEarlyToCancel();
        }
        
        _cancelIntent(_intentId);
    }

    function _cancelIntent(uint256 _intentId) internal {
        Intent storage intent = intents[_intentId];
        
        uint256 refundAmount = uint256(intent.amountA) + uint256(intent.reward);
        IERC20(intent.tokenA).safeTransfer(intent.user, refundAmount);
        
        intent.status = IntentStatus.Cancelled;
        _removeFromActive(_intentId);

        emit IntentCancelled(_intentId, intent.user, uint96(refundAmount));
    }

    function _addToActive(uint256 _intentId) internal {
        activeIntentIndex[_intentId] = activeIntentIds.length;
        activeIntentIds.push(_intentId);
        isActiveIntent[_intentId] = true;
    }

    function _removeFromActive(uint256 _intentId) internal {
        if (!isActiveIntent[_intentId]) return;
        
        uint256 index = activeIntentIndex[_intentId];
        uint256 lastId = activeIntentIds[activeIntentIds.length - 1];
        
        activeIntentIds[index] = lastId;
        activeIntentIndex[lastId] = index;
        activeIntentIds.pop();
        
        delete activeIntentIndex[_intentId];
        isActiveIntent[_intentId] = false;
    }

    /**
     * @notice Get paginated list of active/finalized intent IDs
     * @param offset Starting index
     * @param limit Max results to return
     * @return Array of intent IDs
     */
    function getActiveIntents(uint256 offset, uint256 limit) external view returns (uint256[] memory) {
        uint256 total = activeIntentIds.length;
        if (offset >= total) return new uint256[](0);
        
        uint256 end = offset + limit;
        if (end > total) end = total;
        
        uint256[] memory result = new uint256[](end - offset);
        for (uint256 i = offset; i < end; i++) {
            result[i - offset] = activeIntentIds[i];
        }
        return result;
    }

    /// @notice Total number of active/finalized intents
    function getActiveIntentCount() external view returns (uint256) {
        return activeIntentIds.length;
    }

    /// @notice Get full intent data
    function getIntent(uint256 _intentId) external view validIntentId(_intentId) returns (Intent memory) {
        return intents[_intentId];
    }

    /// @notice Get auction state for an intent
    function getBidState(uint256 _intentId) external view validIntentId(_intentId) returns (BidState memory) {
        return intentBidState[_intentId];
    }

    /// @notice Get a solver's current bid amount for an intent
    function getSolverBid(uint256 _intentId, address _solver) external view returns (uint96) {
        return solverBids[_intentId][_solver];
    }

    /// @notice Check if auction is still accepting bids
    function isIntentActive(uint256 _intentId) external view returns (bool) {
        if (_intentId == 0 || _intentId >= nextIntentId) return false;
        Intent storage intent = intents[_intentId];
        return intent.status == IntentStatus.Active && block.timestamp < intent.endTime;
    }

    /// @notice Seconds remaining in auction (0 if ended)
    function getTimeRemaining(uint256 _intentId) external view returns (uint256) {
        if (_intentId == 0 || _intentId >= nextIntentId) return 0;
        Intent storage intent = intents[_intentId];
        if (block.timestamp >= intent.endTime) return 0;
        return intent.endTime - block.timestamp;
    }

    /// @notice Most recently created intent ID
    function getLatestIntentId() external view returns (uint256) {
        return nextIntentId - 1;
    }

    /// @notice Chain ID this contract is deployed on
    function getChainId() external view returns (uint256) {
        return chainId;
    }

    /**
     * @notice Set minimum reward threshold for a token
     * @param _token Token address
     * @param _minReward Minimum reward amount (0 to disable)
     */
    function setMinReward(address _token, uint96 _minReward) external onlyRole(OPERATOR_ROLE) {
        minRewardPerToken[_token] = _minReward;
        emit MinRewardUpdated(_token, _minReward);
    }

    /// @notice Halt all user operations (intents, bids, solves)
    function pause() external onlyRole(PAUSER_ROLE) {
        _pause();
    }

    /// @notice Resume operations
    function unpause() external onlyRole(PAUSER_ROLE) {
        _unpause();
    }

    /**
     * @notice Recover stuck tokens (use with extreme caution)
     * @param _token Token to withdraw
     * @param _to Recipient address
     * @param _amount Amount to withdraw
     */
    function emergencyWithdraw(address _token, address _to, uint256 _amount) external onlyRole(ADMIN_ROLE) {
        IERC20(_token).safeTransfer(_to, _amount);
    }

    function isRelayer(address _account) external view returns (bool) {
        return hasRole(RELAYER_ROLE, _account);
    }

    function isOperator(address _account) external view returns (bool) {
        return hasRole(OPERATOR_ROLE, _account);
    }

    function isPauser(address _account) external view returns (bool) {
        return hasRole(PAUSER_ROLE, _account);
    }

    function isAdmin(address _account) external view returns (bool) {
        return hasRole(ADMIN_ROLE, _account);
    }
}
