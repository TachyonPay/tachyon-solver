// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

contract BridgeIntent is ReentrancyGuard, Ownable {
    using SafeERC20 for IERC20;

    struct Intent {
        uint256 intentId;
        address user;
        address tokenA;
        address tokenB;
        uint256 amountA;
        uint256 expectedAmountB;
        uint256 reward;
        uint256 destChainId;  // Destination chain ID where tokens should be delivered
        uint256 endTime;
        address winningSolver;
        uint256 winningBid;
        bool completed;
        bool deposited;
    }

    struct Bid {
        address solver;
        uint256 bidAmount;
        uint256 timestamp;
    }

    mapping(uint256 => Intent) public intents;
    mapping(uint256 => Bid[]) public intentBids;
    mapping(uint256 => mapping(address => uint256)) public solverBids;
    mapping(address => bool) public authorizedRelayers;

    // Chain2 tracking for solved intents - using composite key (sourceChainId-intentId)
    // This prevents collisions when same intent ID exists on multiple chains
    mapping(bytes32 => bool) public solvedIntents;

    // Solana-bound intents: when destChainId is a Solana cluster (LayerZero
    // EID, e.g. 30168 mainnet, 40168 devnet/testnet), the destination token
    // and recipient are 32-byte Solana pubkeys/PDAs that don't fit in
    // address(20). We keep the original Intent struct unchanged and store
    // those extra fields in a parallel mapping. Source-side bookkeeping
    // (auction, deposit, settle) reuses the existing Intent state machine.
    struct SolanaIntentExt {
        bytes32 tokenBMint;       // SPL mint on the destination Solana cluster
        bytes32 recipient;        // stealth PDA or plain Solana pubkey
        uint64  destSolanaEID;    // LayerZero EID mirror of destChainId, kept for clarity
    }
    mapping(uint256 => SolanaIntentExt) public solanaIntentExt;
    mapping(uint256 => bool) public isSolanaDestIntent;

    uint256 public nextIntentId = 1;
    uint256 public chainId;
    uint256 public constant MIN_AUCTION_DURATION = 5 seconds;
    uint256 public constant MAX_AUCTION_DURATION = 24 hours;

    event IntentCreated(
        uint256 indexed intentId,
        address indexed user,
        address tokenA,
        address tokenB,
        uint256 amountA,
        uint256 expectedAmountB,
        uint256 reward,
        uint256 destChainId,
        uint256 endTime
    );

    event BidPlaced(
        uint256 indexed intentId,
        address indexed solver,
        uint256 bidAmount,
        uint256 timestamp
    );

    event IntentWon(
        uint256 indexed intentId,
        address indexed winningSolver,
        uint256 winningBid
    );

    event SolverDeposited(
        uint256 indexed intentId,
        address indexed solver,
        uint256 amount
    );

    event FundsDistributed(
        uint256 indexed intentId,
        address[] recipients,
        uint256[] amounts
    );

    event IntentCompleted(
        uint256 indexed intentId,
        address indexed solver
    );

    event IntentSettled(
        uint256 indexed intentId,
        address indexed solver,
        uint256 amountA,
        uint256 reward,
        uint256 winningBid
    );

    event IntentSolvedOnChain2(
        uint256 indexed intentId,
        address indexed solver,
        address tokenB,
        uint256 amountB
    );

    /// Emitted when a user creates an intent whose destination is a Solana
    /// cluster. Mirrors `IntentCreated` plus the Solana-only 32-byte fields.
    /// Solver listeners use this event (not `IntentCreated`) to recognize
    /// EVM->Solana intents and route delivery through `bridge_intent` on
    /// Solana.
    event IntentCreatedToSolana(
        uint256 indexed intentId,
        address indexed user,
        address tokenA,
        bytes32 tokenBMint,
        uint256 amountA,
        uint256 expectedAmountB,
        uint256 reward,
        uint64  destSolanaEID,
        bytes32 recipient,
        uint256 endTime
    );

    modifier onlyAuthorizedRelayer() {
        require(authorizedRelayers[msg.sender], "Not authorized relayer");
        _;
    }

    modifier validIntentId(uint256 _intentId) {
        require(_intentId > 0, "Invalid intent ID");
        require(_intentId < nextIntentId, "Invalid intent ID");
        _;
    }

    constructor(uint256 _chainId) Ownable(msg.sender) {
        chainId = _chainId;
    }

    /**
     * @dev Get the current chain ID
     * @return Current chain ID
     */
    function getChainId() external view returns (uint256) {
        return chainId;
    }

    /**
     * @dev Get the latest intent ID
     * @return Latest intent ID
     */
    function getLatestIntentId() external view returns (uint256) {
        return nextIntentId - 1;
    }

    /**
     * @dev Get active intents (not completed)
     * @return Array of active intent IDs
     */
    function getActiveIntents() external view returns (uint256[] memory) {
        uint256 count = 0;
        for (uint256 i = 1; i < nextIntentId; i++) {
            if (!intents[i].completed) {
                count++;
            }
        }
        
        uint256[] memory activeIntents = new uint256[](count);
        uint256 index = 0;
        for (uint256 i = 1; i < nextIntentId; i++) {
            if (!intents[i].completed) {
                activeIntents[index] = i;
                index++;
            }
        }
        return activeIntents;
    }

    /**
     * @dev Create a new bridging intent
     * @param _tokenA Source token address
     * @param _tokenB Destination token address
     * @param _amountA Amount of source token to bridge
     * @param _expectedAmountB Expected amount of destination token
     * @param _reward Reward amount for the solver
     * @param _destChainId Destination chain ID where tokens should be delivered
     * @param _auctionDuration Duration of the auction in seconds
     */
    function createIntent(
        address _tokenA,
        address _tokenB,
        uint256 _amountA,
        uint256 _expectedAmountB,
        uint256 _reward,
        uint256 _destChainId,
        uint256 _auctionDuration
    ) external nonReentrant {
        require(_tokenA != address(0) && _tokenB != address(0), "Invalid token addresses");
        require(_amountA > 0 && _expectedAmountB > 0, "Invalid amounts");
        require(_reward > 0, "Invalid reward");
        require(_destChainId > 0, "Invalid destination chain ID");
        require(_auctionDuration >= MIN_AUCTION_DURATION && _auctionDuration <= MAX_AUCTION_DURATION, "Invalid auction duration");

        uint256 intentId = nextIntentId++;
        uint256 endTime = block.timestamp + _auctionDuration;

        // Transfer tokens from user to contract
        IERC20(_tokenA).safeTransferFrom(msg.sender, address(this), _amountA);
        if (_tokenA != _tokenB) {
            IERC20(_tokenA).safeTransferFrom(msg.sender, address(this), _reward);
        }

        intents[intentId] = Intent({
            intentId: intentId,
            user: msg.sender,
            tokenA: _tokenA,
            tokenB: _tokenB,
            amountA: _amountA,
            expectedAmountB: _expectedAmountB,
            reward: _reward,
            destChainId: _destChainId,
            endTime: endTime,
            winningSolver: address(0),
            winningBid: 0,
            completed: false,
            deposited: false
        });

        emit IntentCreated(
            intentId,
            msg.sender,
            _tokenA,
            _tokenB,
            _amountA,
            _expectedAmountB,
            _reward,
            _destChainId,
            endTime
        );
    }

    /**
     * @dev Create a bridging intent whose destination is a Solana cluster.
     *
     * The Intent struct is reused for source-side bookkeeping (auction,
     * deposit, settle) — `tokenB` is set to address(0) since it doesn't
     * exist on EVM, and `destChainId` is the LayerZero EID for Solana
     * (e.g. 30168 mainnet, 40168 devnet). The Solana-only fields live in
     * `solanaIntentExt[intentId]`.
     *
     * @param _tokenA           Source token address (ERC-20)
     * @param _tokenBMint       Destination SPL mint (32 bytes)
     * @param _amountA          Amount of source token to bridge
     * @param _expectedAmountB  Expected amount of destination SPL token (in
     *                          its decimals — typically 6 for USDC, 9 for SOL)
     * @param _reward           Reward in tokenA for the solver
     * @param _destSolanaEID    LayerZero EID for the destination Solana
     *                          cluster (30168 mainnet, 40168 devnet)
     * @param _recipient        Solana recipient pubkey or stealth PDA
     * @param _auctionDuration  Auction duration in seconds
     */
    function createIntentToSolana(
        address _tokenA,
        bytes32 _tokenBMint,
        uint256 _amountA,
        uint256 _expectedAmountB,
        uint256 _reward,
        uint64  _destSolanaEID,
        bytes32 _recipient,
        uint256 _auctionDuration
    ) external nonReentrant {
        require(_tokenA != address(0), "Invalid tokenA");
        require(_tokenBMint != bytes32(0), "Invalid tokenBMint");
        require(_amountA > 0 && _expectedAmountB > 0, "Invalid amounts");
        require(_reward > 0, "Invalid reward");
        require(_destSolanaEID > 0, "Invalid destSolanaEID");
        require(_recipient != bytes32(0), "Invalid recipient");
        require(
            _auctionDuration >= MIN_AUCTION_DURATION && _auctionDuration <= MAX_AUCTION_DURATION,
            "Invalid auction duration"
        );

        uint256 intentId = nextIntentId++;
        uint256 endTime = block.timestamp + _auctionDuration;

        // Pull amountA + reward of tokenA up front. Unlike the EVM->EVM
        // `createIntent`, there is no tokenA==tokenB shortcut: tokenB lives
        // on Solana, so the reward is always pulled separately.
        IERC20(_tokenA).safeTransferFrom(msg.sender, address(this), _amountA);
        IERC20(_tokenA).safeTransferFrom(msg.sender, address(this), _reward);

        intents[intentId] = Intent({
            intentId: intentId,
            user: msg.sender,
            tokenA: _tokenA,
            tokenB: address(0),                      // tokenB lives on Solana — sentinel
            amountA: _amountA,
            expectedAmountB: _expectedAmountB,
            reward: _reward,
            destChainId: uint256(_destSolanaEID),
            endTime: endTime,
            winningSolver: address(0),
            winningBid: 0,
            completed: false,
            deposited: false
        });

        solanaIntentExt[intentId] = SolanaIntentExt({
            tokenBMint:    _tokenBMint,
            recipient:     _recipient,
            destSolanaEID: _destSolanaEID
        });
        isSolanaDestIntent[intentId] = true;

        emit IntentCreatedToSolana(
            intentId,
            msg.sender,
            _tokenA,
            _tokenBMint,
            _amountA,
            _expectedAmountB,
            _reward,
            _destSolanaEID,
            _recipient,
            endTime
        );
    }

    /**
     * @dev Get the Solana-only extension for a Solana-destination intent.
     */
    function getSolanaIntentExt(uint256 _intentId)
        external
        view
        validIntentId(_intentId)
        returns (bytes32 tokenBMint, bytes32 recipient, uint64 destSolanaEID, bool isSolanaDest)
    {
        SolanaIntentExt storage ext = solanaIntentExt[_intentId];
        return (ext.tokenBMint, ext.recipient, ext.destSolanaEID, isSolanaDestIntent[_intentId]);
    }

    /**
     * @dev Place a bid on an intent (higher bid = more tokenA willing to accept)
     * @param _intentId Intent ID to bid on
     * @param _bidAmount Amount of tokenA willing to accept on source chain
     */
    function placeBid(uint256 _intentId, uint256 _bidAmount) external validIntentId(_intentId) {
        Intent storage intent = intents[_intentId];
        require(block.timestamp < intent.endTime, "Auction ended");
        require(!intent.completed, "Intent already completed");
        require(_bidAmount > 0, "Bid must be greater than zero");
        require(_bidAmount > solverBids[_intentId][msg.sender], "Bid not higher than previous");

        solverBids[_intentId][msg.sender] = _bidAmount;
        intentBids[_intentId].push(Bid({
            solver: msg.sender,
            bidAmount: _bidAmount,
            timestamp: block.timestamp
        }));

        emit BidPlaced(_intentId, msg.sender, _bidAmount, block.timestamp);
    }

    /**
     * @dev Finalize auction and determine winning solver
     * @param _intentId Intent ID to finalize
     */
    function finalizeAuction(uint256 _intentId) external validIntentId(_intentId) {
        Intent storage intent = intents[_intentId];
        require(block.timestamp >= intent.endTime, "Auction still active");
        require(intent.winningSolver == address(0), "Auction already finalized");

        Bid[] storage bids = intentBids[_intentId];
        require(bids.length > 0, "No bids placed");

        // Find highest bid
        uint256 highestBid = 0;
        address winningSolver = address(0);
        
        for (uint256 i = 0; i < bids.length; i++) {
            if (bids[i].bidAmount > highestBid) {
                highestBid = bids[i].bidAmount;
                winningSolver = bids[i].solver;
            }
        }

        intent.winningSolver = winningSolver;
        intent.winningBid = highestBid;

        emit IntentWon(_intentId, winningSolver, highestBid);
    }

    /**
     * @dev Winning solver deposits tokens to pick up the intent
     * @param _intentId Intent ID to deposit for
     */
    function depositAndPickup(uint256 _intentId) external validIntentId(_intentId) nonReentrant {
        Intent storage intent = intents[_intentId];
        require(msg.sender == intent.winningSolver, "Not winning solver");
        require(!intent.deposited, "Already deposited");
        require(!intent.completed, "Intent already completed");

        // Solver deposits the winning bid amount of tokenA (source token)
        IERC20(intent.tokenA).safeTransferFrom(msg.sender, address(this), intent.winningBid);
        
        intent.deposited = true;

        emit SolverDeposited(_intentId, msg.sender, intent.winningBid);
    }

    /**
     * @dev Distribute funds to recipients (called by relayer after verification)
     * @param _intentId Intent ID to distribute for
     * @param _recipients Array of recipient addresses
     * @param _amounts Array of amounts corresponding to recipients
     */
    function distributeFunds(
        uint256 _intentId,
        address[] calldata _recipients,
        uint256[] calldata _amounts
    ) external validIntentId(_intentId) onlyAuthorizedRelayer nonReentrant {
        Intent storage intent = intents[_intentId];
        require(intent.deposited, "Solver has not deposited");
        require(!intent.completed, "Intent already completed");
        require(_recipients.length == _amounts.length, "Arrays length mismatch");
        require(_recipients.length > 0, "Empty recipients array");

        uint256 totalAmount = 0;
        for (uint256 i = 0; i < _amounts.length; i++) {
            totalAmount += _amounts[i];
        }
        require(totalAmount <= intent.winningBid, "Total exceeds deposited amount");

        // Distribute funds to recipients
        for (uint256 i = 0; i < _recipients.length; i++) {
            if (_amounts[i] > 0) {
                IERC20(intent.tokenB).safeTransfer(_recipients[i], _amounts[i]);
            }
        }

        // Send reward to solver
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.reward);

        // Return remaining tokens to user
        uint256 remainingAmount = intent.winningBid - totalAmount;
        if (remainingAmount > 0) {
            IERC20(intent.tokenB).safeTransfer(intent.user, remainingAmount);
        }

        intent.completed = true;

        emit FundsDistributed(_intentId, _recipients, _amounts);
        emit IntentCompleted(_intentId, intent.winningSolver);
    }

    /**
     * @dev Solve intent on chain2 (destination chain) - any solver can execute this
     * @param _intentId Intent ID from chain1
     * @param _user User address from chain1
     * @param _tokenB Destination token address
     * @param _expectedAmountB Expected amount of destination token
     * @param _recipients Array of recipient addresses
     * @param _amounts Array of amounts corresponding to recipients
     */
    function solveIntentOnChain2(
        uint256 _sourceChainId,
        uint256 _intentId,
        address _user,
        address _tokenB,
        uint256 _expectedAmountB,
        address[] calldata _recipients,
        uint256[] calldata _amounts
    ) external nonReentrant {
        require(_sourceChainId > 0, "Invalid source chain ID");
        require(_intentId > 0, "Invalid intent ID");
        require(_user != address(0), "Invalid user address");
        require(_tokenB != address(0), "Invalid token address");
        require(_expectedAmountB > 0, "Invalid expected amount");
        require(_recipients.length == _amounts.length, "Arrays length mismatch");
        require(_recipients.length > 0, "Empty recipients array");

        // Create composite key: keccak256(sourceChainId, intentId)
        bytes32 intentKey = keccak256(abi.encodePacked(_sourceChainId, _intentId));
        require(!solvedIntents[intentKey], "Intent already solved");

        uint256 totalAmount = 0;
        for (uint256 i = 0; i < _amounts.length; i++) {
            totalAmount += _amounts[i];
        }
        require(totalAmount <= _expectedAmountB, "Total exceeds expected amount");

        // Transfer tokens from solver to contract
        IERC20(_tokenB).safeTransferFrom(msg.sender, address(this), totalAmount);

        // Distribute funds to recipients
        for (uint256 i = 0; i < _recipients.length; i++) {
            if (_amounts[i] > 0) {
                IERC20(_tokenB).safeTransfer(_recipients[i], _amounts[i]);
            }
        }

        // Mark intent as solved using composite key
        solvedIntents[intentKey] = true;

        emit IntentSolvedOnChain2(_intentId, msg.sender, _tokenB, totalAmount);
        emit IntentCompleted(_intentId, msg.sender);
    }

    /**
     * @dev Check if intent is solved on chain2
     * @param _sourceChainId Source chain ID where intent was created
     * @param _intentId Intent ID to check
     * @return True if intent is solved
     */
    function isIntentSolvedOnChain2(uint256 _sourceChainId, uint256 _intentId) external view returns (bool) {
        bytes32 intentKey = keccak256(abi.encodePacked(_sourceChainId, _intentId));
        return solvedIntents[intentKey];
    }

    /**
     * @dev Emergency withdrawal for user if intent fails
     * @param _intentId Intent ID to cancel
     */
    function cancelIntent(uint256 _intentId) external validIntentId(_intentId) nonReentrant {
        Intent storage intent = intents[_intentId];
        require(msg.sender == intent.user || msg.sender == owner(), "Not authorized");
        require(!intent.completed, "Intent already completed");
        require(block.timestamp > intent.endTime + 1 hours, "Too early to cancel");
        
        // Return user's tokens
        IERC20(intent.tokenA).safeTransfer(intent.user, intent.amountA);
        IERC20(intent.tokenA).safeTransfer(intent.user, intent.reward);
        
        // Return solver's deposit if any
        if (intent.deposited) {
            IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.winningBid);
        }
        
        intent.completed = true;
    }

    /**
     * @dev Settle intent after successful completion on destination chain
     * @param _intentId Intent ID to settle
     */
    function settleIntent(uint256 _intentId) external validIntentId(_intentId) onlyAuthorizedRelayer nonReentrant {
        Intent storage intent = intents[_intentId];
        require(intent.deposited, "Solver has not deposited");
        require(!intent.completed, "Intent already completed");
        require(intent.winningSolver != address(0), "No winning solver");

        // Transfer user's original tokens (tokenA) to solver
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.amountA);
        
        // Transfer user's reward to solver
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.reward);
        
        // Return solver's deposit (tokenA) back to solver
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.winningBid);

        intent.completed = true;

        emit IntentSettled(_intentId, intent.winningSolver, intent.amountA, intent.reward, intent.winningBid);
        emit IntentCompleted(_intentId, intent.winningSolver);
    }

    /**
     * @dev Settle intent after successful completion on destination chain (with chain2 verification)
     * @param _intentId Intent ID to settle
     * @param _chain2IntentId Intent ID from chain2 (should match the solved intent)
     */
    function settleIntentWithChain2Verification(uint256 _intentId, uint256 _chain2IntentId) external validIntentId(_intentId) onlyAuthorizedRelayer nonReentrant {
        Intent storage intent = intents[_intentId];
        require(intent.deposited, "Solver has not deposited");
        require(!intent.completed, "Intent already completed");
        require(intent.winningSolver != address(0), "No winning solver");
        
        // Verify that the intent was solved on chain2
        // Note: This would typically be verified through a cross-chain message or oracle
        // For now, we'll rely on the relayer to provide the correct chain2 intent ID
        require(_chain2IntentId > 0, "Invalid chain2 intent ID");

        // Transfer user's original tokens (tokenA) to solver
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.amountA);
        
        // Transfer user's reward to solver
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.reward);
        
        // Return solver's deposit (tokenA) back to solver
        IERC20(intent.tokenA).safeTransfer(intent.winningSolver, intent.winningBid);

        intent.completed = true;

        emit IntentSettled(_intentId, intent.winningSolver, intent.amountA, intent.reward, intent.winningBid);
        emit IntentCompleted(_intentId, intent.winningSolver);
    }

    /**
     * @dev Add authorized relayer
     * @param _relayer Relayer address to authorize
     */
    function addRelayer(address _relayer) external onlyOwner {
        require(_relayer != address(0), "Invalid relayer address");
        authorizedRelayers[_relayer] = true;
    }

    /**
     * @dev Remove authorized relayer
     * @param _relayer Relayer address to remove
     */
    function removeRelayer(address _relayer) external onlyOwner {
        authorizedRelayers[_relayer] = false;
    }

    /**
     * @dev Get all bids for an intent
     * @param _intentId Intent ID to get bids for
     */
    function getIntentBids(uint256 _intentId) external view validIntentId(_intentId) returns (Bid[] memory) {
        return intentBids[_intentId];
    }

    /**
     * @dev Get intent details
     * @param _intentId Intent ID to get details for
     */
    function getIntent(uint256 _intentId) external view validIntentId(_intentId) returns (Intent memory) {
        return intents[_intentId];
    }

    // View functions for solvers and UI
    function getIntentDetails(uint256 intentId) external view returns (
        address tokenA,
        address tokenB,
        uint256 amountA,
        uint256 expectedAmountB,
        uint256 reward,
        uint256 destChainId,
        uint256 endTime,
        bool completed,
        address user,
        address winningSolver,
        uint256 winningBid
    ) {
        Intent storage intent = intents[intentId];
        return (
            intent.tokenA,
            intent.tokenB,
            intent.amountA,
            intent.expectedAmountB,
            intent.reward,
            intent.destChainId,
            intent.endTime,
            intent.completed,
            intent.user,
            intent.winningSolver,
            intent.winningBid
        );
    }
    
    function isIntentActive(uint256 intentId) external view returns (bool) {
        Intent storage intent = intents[intentId];
        return intent.endTime > block.timestamp && !intent.completed;
    }
    
    function getIntentBidsCount(uint256 intentId) external view returns (uint256) {
        return intentBids[intentId].length;
    }
    
    function getHighestBid(uint256 intentId) external view returns (uint256 amount, address bidder) {
        Intent storage intent = intents[intentId];
        Bid[] storage bids = intentBids[intentId];
        if (bids.length == 0) {
            return (0, address(0));
        }
        uint256 highestBid = 0;
        address winningSolver = address(0);
        for (uint256 i = 0; i < bids.length; i++) {
            if (bids[i].bidAmount > highestBid) {
                highestBid = bids[i].bidAmount;
                winningSolver = bids[i].solver;
            }
        }
        return (highestBid, winningSolver);
    }
    
    function getIntentTimeRemaining(uint256 intentId) external view returns (uint256) {
        Intent storage intent = intents[intentId];
        if (intent.endTime <= block.timestamp) {
            return 0;
        }
        return intent.endTime - block.timestamp;
    }
} 