package blockchain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
	pkgtypes "github.com/zk-tachyon/tachyon-prod/pkg/types"
)

const bridgeIntentABI = `[{"inputs":[{"internalType":"uint256","name":"_chainId","type":"uint256"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[{"internalType":"address","name":"owner","type":"address"}],"name":"OwnableInvalidOwner","type":"error"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"OwnableUnauthorizedAccount","type":"error"},{"inputs":[],"name":"ReentrancyGuardReentrantCall","type":"error"},{"inputs":[{"internalType":"address","name":"token","type":"address"}],"name":"SafeERC20FailedOperation","type":"error"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"bidAmount","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"timestamp","type":"uint256"}],"name":"BidPlaced","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":false,"internalType":"address[]","name":"recipients","type":"address[]"},{"indexed":false,"internalType":"uint256[]","name":"amounts","type":"uint256[]"}],"name":"FundsDistributed","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"}],"name":"IntentCompleted","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"user","type":"address"},{"indexed":false,"internalType":"address","name":"tokenA","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"destChainId","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"endTime","type":"uint256"}],"name":"IntentCreated","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"winningBid","type":"uint256"}],"name":"IntentSettled","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountB","type":"uint256"}],"name":"IntentSolvedOnChain2","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"winningSolver","type":"address"},{"indexed":false,"internalType":"uint256","name":"winningBid","type":"uint256"}],"name":"IntentWon","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"previousOwner","type":"address"},{"indexed":true,"internalType":"address","name":"newOwner","type":"address"}],"name":"OwnershipTransferred","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"SolverDeposited","type":"event"},{"inputs":[],"name":"MAX_AUCTION_DURATION","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"MIN_AUCTION_DURATION","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"_relayer","type":"address"}],"name":"addRelayer","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"authorizedRelayers","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"cancelIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"chainId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"_tokenA","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_amountA","type":"uint256"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"_reward","type":"uint256"},{"internalType":"uint256","name":"_destChainId","type":"uint256"},{"internalType":"uint256","name":"_auctionDuration","type":"uint256"}],"name":"createIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"depositAndPickup","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"address[]","name":"_recipients","type":"address[]"},{"internalType":"uint256[]","name":"_amounts","type":"uint256[]"}],"name":"distributeFunds","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"finalizeAuction","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"getActiveIntents","outputs":[{"internalType":"uint256[]","name":"","type":"uint256[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getChainId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"intentId","type":"uint256"}],"name":"getHighestBid","outputs":[{"internalType":"uint256","name":"amount","type":"uint256"},{"internalType":"address","name":"bidder","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getIntent","outputs":[{"components":[{"internalType":"uint256","name":"intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"bool","name":"deposited","type":"bool"}],"internalType":"struct BridgeIntent.Intent","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getIntentBids","outputs":[{"components":[{"internalType":"address","name":"solver","type":"address"},{"internalType":"uint256","name":"bidAmount","type":"uint256"},{"internalType":"uint256","name":"timestamp","type":"uint256"}],"internalType":"struct BridgeIntent.Bid[]","name":"","type":"tuple[]"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"intentId","type":"uint256"}],"name":"getIntentBidsCount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"intentId","type":"uint256"}],"name":"getIntentDetails","outputs":[{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"intentId","type":"uint256"}],"name":"getIntentTimeRemaining","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"},{"internalType":"uint256","name":"","type":"uint256"}],"name":"intentBids","outputs":[{"internalType":"address","name":"solver","type":"address"},{"internalType":"uint256","name":"bidAmount","type":"uint256"},{"internalType":"uint256","name":"timestamp","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"intents","outputs":[{"internalType":"uint256","name":"intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"bool","name":"deposited","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"intentId","type":"uint256"}],"name":"isIntentActive","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_sourceChainId","type":"uint256"},{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"isIntentSolvedOnChain2","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"nextIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"owner","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"uint256","name":"_bidAmount","type":"uint256"}],"name":"placeBid","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"_relayer","type":"address"}],"name":"removeRelayer","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"renounceOwnership","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"settleIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"uint256","name":"_chain2IntentId","type":"uint256"}],"name":"settleIntentWithChain2Verification","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_sourceChainId","type":"uint256"},{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"address","name":"_user","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"address[]","name":"_recipients","type":"address[]"},{"internalType":"uint256[]","name":"_amounts","type":"uint256[]"}],"name":"solveIntentOnChain2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"name":"solvedIntents","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"},{"internalType":"address","name":"","type":"address"}],"name":"solverBids","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"newOwner","type":"address"}],"name":"transferOwnership","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

// Client wraps Ethereum client with contract interaction
type Client struct {
	ethClient      *ethclient.Client
	contractAddr   common.Address
	contract       *bind.BoundContract
	contractABI    abi.ABI
	privateKey     *ecdsa.PrivateKey
	publicKey      *ecdsa.PublicKey
	address        common.Address
	chainID        *big.Int
	networkName    string
}

// NewClient creates a new blockchain client
func NewClient(rpcURL string, contractAddress string, privateKeyHex string, chainID *big.Int, networkName string) (*Client, error) {
	ethClient, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum client: %w", err)
	}

	contractAddr := common.HexToAddress(contractAddress)

	contractABI, err := abi.JSON(strings.NewReader(bridgeIntentABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract ABI: %w", err)
	}

	contract := bind.NewBoundContract(contractAddr, contractABI, ethClient, ethClient, ethClient)

	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("failed to cast public key to ECDSA")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	log.WithFields(log.Fields{
		"network": networkName,
		"address": address.Hex(),
		"chainID": chainID,
	}).Info("Blockchain client initialized")

	return &Client{
		ethClient:    ethClient,
		contractAddr: contractAddr,
		contract:     contract,
		contractABI:  contractABI,
		privateKey:   privateKey,
		publicKey:    publicKeyECDSA,
		address:      address,
		chainID:      chainID,
		networkName:  networkName,
	}, nil
}

// GetAddress returns the wallet address
func (c *Client) GetAddress() common.Address {
	return c.address
}

// GetBalance returns the token balance for the wallet.
// If tokenAddress is zero, returns ETH balance; otherwise returns ERC20 balance.
func (c *Client) GetBalance(ctx context.Context, tokenAddress common.Address) (*big.Int, error) {
	if tokenAddress == (common.Address{}) {
		balance, err := c.ethClient.BalanceAt(ctx, c.address, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get ETH balance: %w", err)
		}
		return balance, nil
	}

	tokenABI := `[{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"}]`
	parsedABI, err := abi.JSON(strings.NewReader(tokenABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse token ABI: %w", err)
	}

	tokenContract := bind.NewBoundContract(tokenAddress, parsedABI, c.ethClient, c.ethClient, c.ethClient)

	var balance []interface{}
	err = tokenContract.Call(&bind.CallOpts{Context: ctx}, &balance, "balanceOf", c.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get token balance: %w", err)
	}

	if len(balance) == 0 {
		return big.NewInt(0), nil
	}

	return balance[0].(*big.Int), nil
}

// GetTokenDecimals retrieves the decimals of an ERC20 token
func (c *Client) GetTokenDecimals(ctx context.Context, tokenAddress common.Address) (uint8, error) {
	tokenABI := `[{"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"}]`
	parsedABI, err := abi.JSON(strings.NewReader(tokenABI))
	if err != nil {
		return 0, fmt.Errorf("failed to parse token ABI: %w", err)
	}

	tokenContract := bind.NewBoundContract(tokenAddress, parsedABI, c.ethClient, c.ethClient, c.ethClient)

	var decimals []interface{}
	err = tokenContract.Call(&bind.CallOpts{Context: ctx}, &decimals, "decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to get token decimals: %w", err)
	}

	if len(decimals) == 0 {
		return 18, nil // Default to 18 if not available
	}

	return decimals[0].(uint8), nil
}

// GetIntent retrieves an intent from the contract
func (c *Client) GetIntent(ctx context.Context, intentID *big.Int) (*pkgtypes.Intent, error) {
	var result []interface{}
	err := c.contract.Call(&bind.CallOpts{Context: ctx}, &result, "getIntent", intentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get intent: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no result returned")
	}

	intentData := result[0].(struct {
		IntentId        *big.Int       `json:"intentId"`
		User            common.Address `json:"user"`
		TokenA          common.Address `json:"tokenA"`
		TokenB          common.Address `json:"tokenB"`
		AmountA         *big.Int       `json:"amountA"`
		ExpectedAmountB *big.Int       `json:"expectedAmountB"`
		Reward          *big.Int       `json:"reward"`
		DestChainId     *big.Int       `json:"destChainId"` // NEW: Destination chain ID
		EndTime         *big.Int       `json:"endTime"`
		WinningSolver   common.Address `json:"winningSolver"`
		WinningBid      *big.Int       `json:"winningBid"`
		Completed       bool           `json:"completed"`
		Deposited       bool           `json:"deposited"`
	})

	return &pkgtypes.Intent{
		IntentID:        intentData.IntentId,
		User:            intentData.User.Hex(),
		TokenA:          intentData.TokenA.Hex(),
		TokenB:          intentData.TokenB.Hex(),
		AmountA:         intentData.AmountA,
		ExpectedAmountB: intentData.ExpectedAmountB,
		Reward:          intentData.Reward,
		DestChainId:     intentData.DestChainId, // NEW: Include destination chain ID
		EndTime:         intentData.EndTime,
		Completed:       intentData.Completed,
		WinningSolver:   intentData.WinningSolver.Hex(),
		WinningBid:      intentData.WinningBid,
		Deposited:       intentData.Deposited,
	}, nil
}

// GetHighestBid returns the highest bid for an intent
func (c *Client) GetHighestBid(ctx context.Context, intentID *big.Int) (*big.Int, common.Address, error) {
	var result []interface{}
	err := c.contract.Call(&bind.CallOpts{Context: ctx}, &result, "getHighestBid", intentID)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to get highest bid: %w", err)
	}

	if len(result) < 2 {
		return big.NewInt(0), common.Address{}, nil
	}

	amount := result[0].(*big.Int)
	bidder := result[1].(common.Address)

	return amount, bidder, nil
}

// PlaceBid places a bid on an intent
func (c *Client) PlaceBid(ctx context.Context, intentID *big.Int, bidAmount *big.Int) (*types.Transaction, error) {
	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := c.contract.Transact(auth, "placeBid", intentID, bidAmount)
	if err != nil {
		return nil, fmt.Errorf("failed to place bid: %w", err)
	}

	log.WithFields(log.Fields{
		"network":  c.networkName,
		"intentID": intentID,
		"bidAmount": bidAmount,
		"txHash":   tx.Hash().Hex(),
	}).Debug("Bid placed")

	return tx, nil
}

// DepositAndPickup deposits tokens for a won intent
func (c *Client) DepositAndPickup(ctx context.Context, intentID *big.Int) (*types.Transaction, error) {
	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := c.contract.Transact(auth, "depositAndPickup", intentID)
	if err != nil {
		return nil, fmt.Errorf("failed to deposit and pickup: %w", err)
	}

	log.WithFields(log.Fields{
		"network":  c.networkName,
		"intentID": intentID,
		"txHash":   tx.Hash().Hex(),
	}).Debug("Deposit and pickup executed")

	return tx, nil
}

// FinalizeAuction finalizes an auction to set the winning solver
func (c *Client) FinalizeAuction(ctx context.Context, intentID *big.Int) (*types.Transaction, error) {
	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := c.contract.Transact(auth, "finalizeAuction", intentID)
	if err != nil {
		return nil, fmt.Errorf("failed to finalize auction: %w", err)
	}

	log.WithFields(log.Fields{
		"network":  c.networkName,
		"intentID": intentID,
		"txHash":   tx.Hash().Hex(),
	}).Debug("Auction finalized")

	return tx, nil
}

// WaitForTransaction waits for a transaction to be mined and confirmed
func (c *Client) WaitForTransaction(ctx context.Context, tx *types.Transaction) (*types.Receipt, error) {
	receipt, err := bind.WaitMined(ctx, c.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for transaction: %w", err)
	}

	if receipt.Status == 0 {
		return receipt, fmt.Errorf("transaction failed: %s", tx.Hash().Hex())
	}

	log.WithFields(log.Fields{
		"network":     c.networkName,
		"txHash":      tx.Hash().Hex(),
		"blockNumber": receipt.BlockNumber,
		"gasUsed":     receipt.GasUsed,
	}).Info("Transaction confirmed")

	return receipt, nil
}

// ApproveToken approves token spending for the bridge contract
func (c *Client) ApproveToken(ctx context.Context, tokenAddress common.Address, amount *big.Int) (*types.Transaction, error) {
	erc20ABI, err := abi.JSON(strings.NewReader(`[{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ERC20 ABI: %w", err)
	}

	token := bind.NewBoundContract(tokenAddress, erc20ABI, c.ethClient, c.ethClient, c.ethClient)

	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := token.Transact(auth, "approve", c.contractAddr, amount)
	if err != nil {
		return nil, fmt.Errorf("failed to approve token: %w", err)
	}

	log.WithFields(log.Fields{
		"network": c.networkName,
		"token":   tokenAddress.Hex(),
		"spender": c.contractAddr.Hex(),
		"amount":  amount.String(),
		"txHash":  tx.Hash().Hex(),
	}).Debug("Token approval submitted")

	return tx, nil
}

// SolveIntentOnChain2 solves an intent on the destination chain
func (c *Client) SolveIntentOnChain2(ctx context.Context, sourceChainID *big.Int, intentID *big.Int, user common.Address, tokenB common.Address, expectedAmountB *big.Int, recipients []common.Address, amounts []*big.Int) (*types.Transaction, error) {
	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := c.contract.Transact(auth, "solveIntentOnChain2", sourceChainID, intentID, user, tokenB, expectedAmountB, recipients, amounts)
	if err != nil {
		return nil, fmt.Errorf("failed to solve intent on chain2: %w", err)
	}

	log.WithFields(log.Fields{
		"network":       c.networkName,
		"sourceChainID": sourceChainID,
		"intentID":      intentID,
		"user":          user.Hex(),
		"tokenB":        tokenB.Hex(),
		"recipients":    len(recipients),
		"txHash":        tx.Hash().Hex(),
	}).Debug("Intent solved on chain2")

	return tx, nil
}

// SettleIntent settles an intent with chain2 verification
func (c *Client) SettleIntent(ctx context.Context, chain1IntentID *big.Int, chain2IntentID *big.Int) (*types.Transaction, error) {
	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := c.contract.Transact(auth, "settleIntentWithChain2Verification", chain1IntentID, chain2IntentID)
	if err != nil {
		return nil, fmt.Errorf("failed to settle intent: %w", err)
	}

	log.WithFields(log.Fields{
		"network":        c.networkName,
		"chain1IntentID": chain1IntentID,
		"chain2IntentID": chain2IntentID,
		"txHash":         tx.Hash().Hex(),
	}).Debug("Intent settled")

	return tx, nil
}

// VerifyIntentSolved checks if an intent has been solved on chain2.
// Requires sourceChainID to create the composite key for verification.
func (c *Client) VerifyIntentSolved(ctx context.Context, sourceChainID *big.Int, intentID *big.Int) (bool, error) {
	var resultFunc []interface{}
	err := c.contract.Call(&bind.CallOpts{Context: ctx}, &resultFunc, "isIntentSolvedOnChain2", sourceChainID, intentID)
	if err == nil && len(resultFunc) > 0 {
		solved := resultFunc[0].(bool)
		log.WithFields(log.Fields{
			"network":       c.networkName,
			"sourceChainID": sourceChainID,
			"intentID":      intentID,
			"solved":        solved,
			"method":        "isIntentSolvedOnChain2",
		}).Info("Checked intent solved status on contract")
		return solved, nil
	}

	// Fallback: Query solvedIntents mapping directly with composite key
	// Compute composite key: keccak256(abi.encodePacked(sourceChainId, intentId))
	sourceChainIDBytes := common.LeftPadBytes(sourceChainID.Bytes(), 32)
	intentIDBytes := common.LeftPadBytes(intentID.Bytes(), 32)
	compositeKey := crypto.Keccak256Hash(append(sourceChainIDBytes, intentIDBytes...))

	var result []interface{}
	err = c.contract.Call(&bind.CallOpts{Context: ctx}, &result, "solvedIntents", compositeKey)
	if err != nil {
		return false, fmt.Errorf("failed to verify intent solved: %w", err)
	}

	if len(result) == 0 {
		log.WithFields(log.Fields{
			"network":       c.networkName,
			"sourceChainID": sourceChainID,
			"intentID":      intentID,
			"compositeKey":  compositeKey.Hex(),
		}).Warn("solvedIntents mapping returned empty result")
		return false, nil
	}

	solved := result[0].(bool)
	log.WithFields(log.Fields{
		"network":  c.networkName,
		"intentID": intentID,
		"solved":   solved,
		"method":   "solvedIntents",
	}).Info("Checked solvedIntents mapping on contract")

	return solved, nil
}

// GetLogs retrieves logs for specific events
// Automatically chunks large block ranges to avoid RPC provider limits (e.g., Alchemy free tier 10-block limit)
func (c *Client) GetLogs(ctx context.Context, fromBlock, toBlock *big.Int, eventNames []string) ([]types.Log, error) {
	var topics [][]common.Hash
	if len(eventNames) > 0 {
		eventTopics := make([]common.Hash, len(eventNames))
		for i, name := range eventNames {
			event, ok := c.contractABI.Events[name]
			if !ok {
				return nil, fmt.Errorf("event %s not found in ABI", name)
			}
			eventTopics[i] = event.ID
		}
		topics = append(topics, eventTopics)
	}

	// Chunk size to avoid RPC provider limits
	// Using 10 blocks per chunk to work with Alchemy free tier (max 10 blocks per request)
	const maxBlocksPerChunk = 10

	allLogs := []types.Log{}
	currentFrom := new(big.Int).Set(fromBlock)

	for currentFrom.Cmp(toBlock) <= 0 {
		currentTo := new(big.Int).Add(currentFrom, big.NewInt(maxBlocksPerChunk-1))
		if currentTo.Cmp(toBlock) > 0 {
			currentTo = new(big.Int).Set(toBlock)
		}

		query := ethereum.FilterQuery{
			FromBlock: currentFrom,
			ToBlock:   currentTo,
			Addresses: []common.Address{c.contractAddr},
			Topics:    topics,
		}

		logs, err := c.ethClient.FilterLogs(ctx, query)
		if err != nil {
			log.WithFields(log.Fields{
				"network":   c.networkName,
				"fromBlock": currentFrom.String(),
				"toBlock":   currentTo.String(),
			}).WithError(err).Warn("Failed to filter logs for chunk, continuing with next chunk")
			// Continue to next chunk instead of failing completely
			currentFrom = new(big.Int).Add(currentTo, big.NewInt(1))
			continue
		}

		allLogs = append(allLogs, logs...)

		// Move to next chunk
		currentFrom = new(big.Int).Add(currentTo, big.NewInt(1))
	}

	log.WithFields(log.Fields{
		"network":    c.networkName,
		"fromBlock":  fromBlock.String(),
		"toBlock":    toBlock.String(),
		"totalLogs":  len(allLogs),
	}).Debug("Completed chunked log retrieval")

	return allLogs, nil
}

// ParseIntentCreatedEvent parses an IntentCreated event log.
// Extracts intentID from event topics (second topic contains indexed intentId).
func (c *Client) ParseIntentCreatedEvent(log types.Log) (intentID *big.Int, err error) {
	event := struct {
		IntentID        *big.Int
		User            common.Address
		TokenA          common.Address
		TokenB          common.Address
		AmountA         *big.Int
		ExpectedAmountB *big.Int
		Reward          *big.Int
		DestChainId     *big.Int
		EndTime         *big.Int
	}{}

	err = c.contractABI.UnpackIntoInterface(&event, "IntentCreated", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack event: %w", err)
	}

	if len(log.Topics) > 1 {
		intentID = new(big.Int).SetBytes(log.Topics[1].Bytes())
	}

	return intentID, nil
}

// GetCurrentBlock returns the current block number
func (c *Client) GetCurrentBlock(ctx context.Context) (*big.Int, error) {
	blockNumber, err := c.ethClient.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current block: %w", err)
	}
	return new(big.Int).SetUint64(blockNumber), nil
}

// getTransactOpts creates transaction options with gas estimation and nonce management
func (c *Client) getTransactOpts(ctx context.Context) (*bind.TransactOpts, error) {
	nonce, err := c.ethClient.PendingNonceAt(ctx, c.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	gasPrice, err := c.ethClient.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to suggest gas price: %w", err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(c.privateKey, c.chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to create transactor: %w", err)
	}

	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(0)
	auth.GasLimit = uint64(300000) // Default gas limit
	auth.GasPrice = gasPrice
	auth.Context = ctx

	return auth, nil
}

func (c *Client) Close() {
	c.ethClient.Close()
}

// GetTokenBalance returns the ERC20 token balance for a given address
func (c *Client) GetTokenBalance(ctx context.Context, owner, token common.Address) (*big.Int, error) {
	erc20ABI, err := abi.JSON(strings.NewReader(`[{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ERC20 ABI: %w", err)
	}

	tokenContract := bind.NewBoundContract(token, erc20ABI, c.ethClient, c.ethClient, c.ethClient)

	var result []interface{}
	err = tokenContract.Call(&bind.CallOpts{Context: ctx}, &result, "balanceOf", owner)
	if err != nil {
		return nil, fmt.Errorf("failed to call balanceOf: %w", err)
	}

	balance, ok := result[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("failed to cast balance to *big.Int")
	}

	return balance, nil
}
