package blockchain

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"sort"
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
	ethClient        *ethclient.Client
	contractAddr     common.Address
	contract         *bind.BoundContract
	contractABI      abi.ABI
	privateKey       *ecdsa.PrivateKey
	publicKey        *ecdsa.PublicKey
	address          common.Address
	safeAddress      common.Address     // zero if no Safe configured
	safeSigningKeys  []*ecdsa.PrivateKey // TEE keys that co-sign Safe transactions
	multicall3Addr   common.Address     // Multicall3 contract for relay-batch; zero = disabled
	chainID          *big.Int
	networkName      string
}

// NewClient creates a new blockchain client.
// safeAddress: Gnosis Safe address on this chain — empty string if not using a Safe.
// safeSigningKeys: the TEE private keys that co-sign Safe transactions (all must sign to reach threshold).
// multicall3Address: Multicall3 contract address for relay-batch — empty string disables batching on this chain.
func NewClient(rpcURL string, contractAddress string, privateKeyHex string, chainID *big.Int, networkName string, safeAddress string, safeSigningKeys []*ecdsa.PrivateKey, multicall3Address string) (*Client, error) {
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

	var safeAddr common.Address
	if safeAddress != "" {
		safeAddr = common.HexToAddress(safeAddress)
	}

	var mc3Addr common.Address
	if multicall3Address != "" {
		mc3Addr = common.HexToAddress(multicall3Address)
	}

	log.WithFields(log.Fields{
		"network":          networkName,
		"address":          address.Hex(),
		"chainID":          chainID,
		"safeAddress":      safeAddr.Hex(),
		"multicall3Addr":   mc3Addr.Hex(),
	}).Info("Blockchain client initialized")

	return &Client{
		ethClient:        ethClient,
		contractAddr:     contractAddr,
		contract:         contract,
		contractABI:      contractABI,
		privateKey:       privateKey,
		publicKey:        publicKeyECDSA,
		address:          address,
		safeAddress:      safeAddr,
		safeSigningKeys:  safeSigningKeys,
		multicall3Addr:   mc3Addr,
		chainID:          chainID,
		networkName:      networkName,
	}, nil
}

// HasSafe returns true if this client is configured with a Gnosis Safe.
func (c *Client) HasSafe() bool {
	return c.safeAddress != (common.Address{})
}

// GetSafeAddress returns the Safe address (zero address if not configured).
func (c *Client) GetSafeAddress() common.Address {
	return c.safeAddress
}

// GetSolverAddress returns the effective solver address: the Safe if configured, otherwise the EOA.
// This is the address that appears as msg.sender for all token-moving transactions.
func (c *Client) GetSolverAddress() common.Address {
	if c.HasSafe() {
		return c.safeAddress
	}
	return c.address
}

// GetAddress returns the wallet address
func (c *Client) GetAddress() common.Address {
	return c.address
}

// EthClient exposes the underlying RPC client. Used by auxiliary services
// (rebalancer) that need to send transactions on this chain with a different key.
func (c *Client) EthClient() *ethclient.Client {
	return c.ethClient
}

// ChainID returns a copy of this client's chain id.
func (c *Client) ChainID() *big.Int {
	return new(big.Int).Set(c.chainID)
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
	err = tokenContract.Call(&bind.CallOpts{Context: ctx}, &balance, "balanceOf", c.GetSolverAddress())
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

// GetTokenSymbol retrieves the symbol of an ERC20 token
func (c *Client) GetTokenSymbol(ctx context.Context, tokenAddress common.Address) (string, error) {
	tokenABI := `[{"constant":true,"inputs":[],"name":"symbol","outputs":[{"name":"","type":"string"}],"type":"function"}]`
	parsedABI, err := abi.JSON(strings.NewReader(tokenABI))
	if err != nil {
		return "", fmt.Errorf("failed to parse token ABI: %w", err)
	}

	tokenContract := bind.NewBoundContract(tokenAddress, parsedABI, c.ethClient, c.ethClient, c.ethClient)

	var result []interface{}
	if err := tokenContract.Call(&bind.CallOpts{Context: ctx}, &result, "symbol"); err != nil {
		return "", fmt.Errorf("failed to get token symbol: %w", err)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("empty response from symbol()")
	}

	symbol, ok := result[0].(string)
	if !ok {
		return "", fmt.Errorf("unexpected type for symbol response")
	}

	return symbol, nil
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

// Safe execTransaction ABI (minimal)
const safeExecABI = `[
  {"inputs":[],"name":"nonce","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
  {"inputs":[{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"value","type":"uint256"},{"internalType":"bytes","name":"data","type":"bytes"},{"internalType":"uint8","name":"operation","type":"uint8"},{"internalType":"uint256","name":"safeTxGas","type":"uint256"},{"internalType":"uint256","name":"baseGas","type":"uint256"},{"internalType":"uint256","name":"gasPrice","type":"uint256"},{"internalType":"address","name":"gasToken","type":"address"},{"internalType":"address payable","name":"refundReceiver","type":"address"},{"internalType":"bytes","name":"signatures","type":"bytes"}],"name":"execTransaction","outputs":[{"internalType":"bool","name":"success","type":"bool"}],"stateMutability":"payable","type":"function"},
  {"inputs":[{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"value","type":"uint256"},{"internalType":"bytes","name":"data","type":"bytes"},{"internalType":"uint8","name":"operation","type":"uint8"},{"internalType":"uint256","name":"safeTxGas","type":"uint256"},{"internalType":"uint256","name":"baseGas","type":"uint256"},{"internalType":"uint256","name":"gasPrice","type":"uint256"},{"internalType":"address","name":"gasToken","type":"address"},{"internalType":"address","name":"refundReceiver","type":"address"},{"internalType":"uint256","name":"_nonce","type":"uint256"}],"name":"getTransactionHash","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"}
]`

// RelaySafeTransaction broadcasts a pre-signed Safe transaction on behalf of an external signer.
// Used by the relayer to submit user-provided signatures (e.g. stealth address relay proxy).
// The EOA pays gas; the Safe is msg.sender for the inner call.
func (c *Client) RelaySafeTransaction(ctx context.Context, safeAddr common.Address, to common.Address, value *big.Int, data []byte, operation uint8, signature []byte) (*types.Transaction, error) {
	safeABI, err := abi.JSON(strings.NewReader(safeExecABI))
	if err != nil {
		return nil, fmt.Errorf("parse safe ABI: %w", err)
	}

	safe := bind.NewBoundContract(safeAddr, safeABI, c.ethClient, c.ethClient, c.ethClient)

	var nonceResult []interface{}
	if err := safe.Call(&bind.CallOpts{Context: ctx}, &nonceResult, "nonce"); err != nil {
		return nil, fmt.Errorf("get safe nonce: %w", err)
	}
	safeNonce := nonceResult[0].(*big.Int)

	var hashResult []interface{}
	if err := safe.Call(&bind.CallOpts{Context: ctx}, &hashResult, "getTransactionHash",
		to, value, data, operation,
		big.NewInt(0), big.NewInt(0), big.NewInt(0),
		common.Address{}, common.Address{},
		safeNonce,
	); err != nil {
		return nil, fmt.Errorf("get transaction hash: %w", err)
	}
	txHash := hashResult[0].([32]byte)

	sigCopy := make([]byte, 65)
	copy(sigCopy, signature)
	if sigCopy[64] >= 27 {
		sigCopy[64] -= 27
	}
	pubKey, err := crypto.SigToPub(txHash[:], sigCopy)
	if err != nil {
		return nil, fmt.Errorf("recover signer: %w", err)
	}
	signer := crypto.PubkeyToAddress(*pubKey)
	log.WithFields(log.Fields{
		"safe":   safeAddr.Hex(),
		"signer": signer.Hex(),
	}).Info("Relaying Safe transaction")

	safeSig := make([]byte, 65)
	copy(safeSig, signature)
	if safeSig[64] < 27 {
		safeSig[64] += 27
	}

	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}
	auth.GasLimit = uint64(300000)

	tx, err := safe.Transact(auth, "execTransaction",
		to, value, data, operation,
		big.NewInt(0), big.NewInt(0), big.NewInt(0),
		common.Address{}, common.Address{},
		safeSig,
	)
	if err != nil {
		return nil, fmt.Errorf("execTransaction: %w", err)
	}

	log.WithFields(log.Fields{
		"safe":   safeAddr.Hex(),
		"to":     to.Hex(),
		"txHash": tx.Hash().Hex(),
	}).Info("Safe transaction relayed")

	return tx, nil
}

// multicall3ABI is the minimal ABI for Multicall3.aggregate3.
// aggregate3 batches arbitrary calls and is deployed at a well-known address on most EVM chains.
const multicall3ABI = `[{
  "inputs":[{"components":[
    {"internalType":"address","name":"target","type":"address"},
    {"internalType":"bool","name":"allowFailure","type":"bool"},
    {"internalType":"bytes","name":"callData","type":"bytes"}
  ],"internalType":"struct Multicall3.Call3[]","name":"calls","type":"tuple[]"}],
  "name":"aggregate3",
  "outputs":[{"components":[
    {"internalType":"bool","name":"success","type":"bool"},
    {"internalType":"bytes","name":"returnData","type":"bytes"}
  ],"internalType":"struct Multicall3.Result[]","name":"returnData","type":"tuple[]"}],
  "stateMutability":"payable",
  "type":"function"
}]`

// BatchRelayItem is a parsed, decoded item ready for RelayBatch.
type BatchRelayItem struct {
	SafeAddr  common.Address
	To        common.Address
	Value     *big.Int
	Data      []byte
	Operation uint8
	Signature []byte // 65-byte sig from the Safe owner
}

// multicall3Call mirrors Multicall3.Call3 for ABI encoding.
type multicall3Call struct {
	Target       common.Address
	AllowFailure bool
	CallData     []byte
}

// RelayBatch verifies each pre-signed Safe transaction, encodes all execTransaction
// calls into one Multicall3.aggregate3 transaction, and submits it from the relayer EOA.
// allowFailure=false → atomic (any failure reverts all); true → best-effort (failures are skipped).
func (c *Client) RelayBatch(ctx context.Context, items []BatchRelayItem, allowFailure bool) (*types.Transaction, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("batch is empty")
	}
	if c.multicall3Addr == (common.Address{}) {
		return nil, fmt.Errorf("Multicall3 not configured for chain %s — set CHAIN_i_MULTICALL3 in env", c.networkName)
	}

	safeABI, err := abi.JSON(strings.NewReader(safeExecABI))
	if err != nil {
		return nil, fmt.Errorf("parse safe ABI: %w", err)
	}

	calls := make([]multicall3Call, 0, len(items))
	for i, item := range items {
		safe := bind.NewBoundContract(item.SafeAddr, safeABI, c.ethClient, c.ethClient, c.ethClient)

		// Fetch current Safe nonce.
		var nonceResult []interface{}
		if err := safe.Call(&bind.CallOpts{Context: ctx}, &nonceResult, "nonce"); err != nil {
			return nil, fmt.Errorf("item %d: get safe nonce: %w", i, err)
		}
		safeNonce := nonceResult[0].(*big.Int)

		// Compute SafeTxHash (all gas params zero — relayer pays externally).
		var hashResult []interface{}
		if err := safe.Call(&bind.CallOpts{Context: ctx}, &hashResult, "getTransactionHash",
			item.To, item.Value, item.Data, item.Operation,
			big.NewInt(0), big.NewInt(0), big.NewInt(0),
			common.Address{}, common.Address{},
			safeNonce,
		); err != nil {
			return nil, fmt.Errorf("item %d: get transaction hash: %w", i, err)
		}
		txHash := hashResult[0].([32]byte)

		// Recover signer for logging; Safe rejects invalid sigs on-chain.
		sigForRecovery := make([]byte, 65)
		copy(sigForRecovery, item.Signature)
		if sigForRecovery[64] >= 27 {
			sigForRecovery[64] -= 27
		}
		pubKey, err := crypto.SigToPub(txHash[:], sigForRecovery)
		if err != nil {
			return nil, fmt.Errorf("item %d: recover signer: %w", i, err)
		}
		log.WithFields(log.Fields{
			"item":   i,
			"safe":   item.SafeAddr.Hex(),
			"signer": crypto.PubkeyToAddress(*pubKey).Hex(),
		}).Info("RelayBatch: verified item signer")

		// Normalise v to 27/28 as Safe expects.
		safeSig := make([]byte, 65)
		copy(safeSig, item.Signature)
		if safeSig[64] < 27 {
			safeSig[64] += 27
		}

		// Encode execTransaction calldata for this Safe.
		callData, err := safeABI.Pack("execTransaction",
			item.To, item.Value, item.Data, item.Operation,
			big.NewInt(0), big.NewInt(0), big.NewInt(0),
			common.Address{}, common.Address{},
			safeSig,
		)
		if err != nil {
			return nil, fmt.Errorf("item %d: encode execTransaction: %w", i, err)
		}

		calls = append(calls, multicall3Call{
			Target:       item.SafeAddr,
			AllowFailure: allowFailure,
			CallData:     callData,
		})
	}

	mc3ABI, err := abi.JSON(strings.NewReader(multicall3ABI))
	if err != nil {
		return nil, fmt.Errorf("parse multicall3 ABI: %w", err)
	}
	mc3Contract := bind.NewBoundContract(c.multicall3Addr, mc3ABI, c.ethClient, c.ethClient, c.ethClient)

	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}
	// 150k base + 200k per Safe call (execTransaction ~100–150k each, buffer included).
	auth.GasLimit = uint64(150000 + len(items)*200000)

	tx, err := mc3Contract.Transact(auth, "aggregate3", calls)
	if err != nil {
		return nil, fmt.Errorf("submit aggregate3 tx: %w", err)
	}

	log.WithFields(log.Fields{
		"txHash":     tx.Hash().Hex(),
		"items":      len(items),
		"multicall3": c.multicall3Addr.Hex(),
	}).Info("RelayBatch: aggregate3 submitted")

	return tx, nil
}

// ExecuteViaSafe signs a call with all configured TEE keys and executes it through the Safe.
//
// How it works:
//  1. Fetches the Safe's current nonce on-chain.
//  2. Calls Safe.getTransactionHash to get the exact EIP-712 hash the Safe expects.
//  3. Signs that hash with every key in safeSigningKeys (the 3 TEE keys).
//  4. Sorts all signatures by their recovered signer address, ascending —
//     Gnosis Safe validates signatures in this order.
//  5. Concatenates all 65-byte signatures and calls execTransaction.
//     The EOA (c.address) pays gas; the Safe is msg.sender for the inner call.
func (c *Client) ExecuteViaSafe(ctx context.Context, to common.Address, data []byte) (*types.Transaction, error) {
	return c.ExecuteViaSafeWithValue(ctx, to, big.NewInt(0), data)
}

// ExecuteViaSafeWithValue is the value-aware variant. The Safe must hold enough
// native gas (ETH on Base, ZEN on Horizen) to cover `value` for the inner call.
// Used by the rebalancer to fund LayerZero / Stargate native fees from the Safe.
func (c *Client) ExecuteViaSafeWithValue(ctx context.Context, to common.Address, value *big.Int, data []byte) (*types.Transaction, error) {
	if !c.HasSafe() {
		return nil, fmt.Errorf("no Safe address configured for this client")
	}
	if len(c.safeSigningKeys) == 0 {
		return nil, fmt.Errorf("no Safe signing keys configured — set SAFE_SIGNER_KEY_1/2/3")
	}
	if value == nil {
		value = big.NewInt(0)
	}

	safeABI, err := abi.JSON(strings.NewReader(safeExecABI))
	if err != nil {
		return nil, fmt.Errorf("parse safe ABI: %w", err)
	}
	safe := bind.NewBoundContract(c.safeAddress, safeABI, c.ethClient, c.ethClient, c.ethClient)

	// 1. Fetch Safe nonce
	var nonceResult []interface{}
	if err := safe.Call(&bind.CallOpts{Context: ctx}, &nonceResult, "nonce"); err != nil {
		return nil, fmt.Errorf("get safe nonce: %w", err)
	}
	safeNonce := nonceResult[0].(*big.Int)

	// 2. Get the Safe tx hash (all gas params zero — EOA pays gas externally)
	var hashResult []interface{}
	if err := safe.Call(&bind.CallOpts{Context: ctx}, &hashResult, "getTransactionHash",
		to, value, data, uint8(0),
		big.NewInt(0), big.NewInt(0), big.NewInt(0),
		common.Address{}, common.Address{},
		safeNonce,
	); err != nil {
		return nil, fmt.Errorf("get transaction hash: %w", err)
	}
	txHash := hashResult[0].([32]byte)

	// 3. Sign with every TEE key
	type sigEntry struct {
		addr common.Address
		sig  [65]byte
	}
	entries := make([]sigEntry, 0, len(c.safeSigningKeys))
	for i, key := range c.safeSigningKeys {
		raw, err := crypto.Sign(txHash[:], key)
		if err != nil {
			return nil, fmt.Errorf("sign with TEE key %d: %w", i+1, err)
		}
		// Safe expects v = 27 or 28 for EOA signatures
		if raw[64] < 27 {
			raw[64] += 27
		}
		var s [65]byte
		copy(s[:], raw)
		entries = append(entries, sigEntry{
			addr: crypto.PubkeyToAddress(key.PublicKey),
			sig:  s,
		})
	}

	// 4. Sort by signer address ascending — Gnosis Safe requirement
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].addr.Bytes(), entries[j].addr.Bytes()) < 0
	})

	// 5. Concatenate all 65-byte signatures
	allSigs := make([]byte, 0, len(entries)*65)
	signerAddrs := make([]string, 0, len(entries))
	for _, e := range entries {
		allSigs = append(allSigs, e.sig[:]...)
		signerAddrs = append(signerAddrs, e.addr.Hex())
	}

	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, err
	}
	auth.GasLimit = uint64(400000)

	tx, err := safe.Transact(auth, "execTransaction",
		to, value, data, uint8(0),
		big.NewInt(0), big.NewInt(0), big.NewInt(0),
		common.Address{}, common.Address{},
		allSigs,
	)
	if err != nil {
		return nil, fmt.Errorf("execTransaction: %w", err)
	}

	log.WithFields(log.Fields{
		"safe":    c.safeAddress.Hex(),
		"to":      to.Hex(),
		"signers": signerAddrs,
		"txHash":  tx.Hash().Hex(),
	}).Info("Safe transaction executed with TEE keys")

	return tx, nil
}

// ApproveTokenViaSafe sends an ERC20 approve from the Safe.
func (c *Client) ApproveTokenViaSafe(ctx context.Context, tokenAddress common.Address, amount *big.Int) (*types.Transaction, error) {
	erc20ABI, err := abi.JSON(strings.NewReader(`[{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`))
	if err != nil {
		return nil, fmt.Errorf("parse ERC20 ABI: %w", err)
	}

	data, err := erc20ABI.Pack("approve", c.contractAddr, amount)
	if err != nil {
		return nil, fmt.Errorf("pack approve: %w", err)
	}

	return c.ExecuteViaSafe(ctx, tokenAddress, data)
}

// PlaceBidViaSafe places a bid on an intent from the Safe.
func (c *Client) PlaceBidViaSafe(ctx context.Context, intentID *big.Int, bidAmount *big.Int) (*types.Transaction, error) {
	data, err := c.contractABI.Pack("placeBid", intentID, bidAmount)
	if err != nil {
		return nil, fmt.Errorf("pack placeBid: %w", err)
	}

	tx, err := c.ExecuteViaSafe(ctx, c.contractAddr, data)
	if err != nil {
		return nil, fmt.Errorf("failed to place bid via Safe: %w", err)
	}

	log.WithFields(log.Fields{
		"safe":      c.safeAddress.Hex(),
		"intentID":  intentID,
		"bidAmount": bidAmount,
		"txHash":    tx.Hash().Hex(),
	}).Debug("Bid placed via Safe")

	return tx, nil
}

// DepositAndPickupViaSafe calls depositAndPickup from the Safe (Safe is msg.sender = winningSolver).
func (c *Client) DepositAndPickupViaSafe(ctx context.Context, intentID *big.Int) (*types.Transaction, error) {
	data, err := c.contractABI.Pack("depositAndPickup", intentID)
	if err != nil {
		return nil, fmt.Errorf("pack depositAndPickup: %w", err)
	}

	tx, err := c.ExecuteViaSafe(ctx, c.contractAddr, data)
	if err != nil {
		return nil, fmt.Errorf("failed to depositAndPickup via Safe: %w", err)
	}

	log.WithFields(log.Fields{
		"safe":     c.safeAddress.Hex(),
		"intentID": intentID,
		"txHash":   tx.Hash().Hex(),
	}).Debug("DepositAndPickup executed via Safe")

	return tx, nil
}

// SolveIntentOnChain2ViaSafe calls solveIntentOnChain2 from the Safe (Safe transfers tokenB to recipients).
func (c *Client) SolveIntentOnChain2ViaSafe(ctx context.Context, sourceChainID *big.Int, intentID *big.Int, user common.Address, tokenB common.Address, expectedAmountB *big.Int, recipients []common.Address, amounts []*big.Int) (*types.Transaction, error) {
	data, err := c.contractABI.Pack("solveIntentOnChain2", sourceChainID, intentID, user, tokenB, expectedAmountB, recipients, amounts)
	if err != nil {
		return nil, fmt.Errorf("pack solveIntentOnChain2: %w", err)
	}

	tx, err := c.ExecuteViaSafe(ctx, c.contractAddr, data)
	if err != nil {
		return nil, fmt.Errorf("failed to solveIntentOnChain2 via Safe: %w", err)
	}

	log.WithFields(log.Fields{
		"safe":          c.safeAddress.Hex(),
		"sourceChainID": sourceChainID,
		"intentID":      intentID,
		"tokenB":        tokenB.Hex(),
		"recipients":    len(recipients),
		"txHash":        tx.Hash().Hex(),
	}).Debug("Intent solved on chain2 via Safe")

	return tx, nil
}
