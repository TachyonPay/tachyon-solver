package types

import (
	"math/big"
	"time"
)

// NetworkConfig defines blockchain network configuration
type NetworkConfig struct {
	Name          string
	RpcURL        string
	ChainID       *big.Int
	BridgeAddress string
	Explorer      string
}

// Intent represents a bridge intent on-chain
type Intent struct {
	IntentID        *big.Int
	User            string
	TokenA          string
	TokenB          string
	AmountA         *big.Int
	ExpectedAmountB *big.Int
	Reward          *big.Int
	EndTime         *big.Int
	Completed       bool
	WinningSolver   string
	WinningBid      *big.Int
	Deposited       bool
}

// BidInfo represents a bid placed on an intent
type BidInfo struct {
	Amount    *big.Int
	Bidder    string
	Timestamp *big.Int
}

// SolverConfig holds solver configuration parameters
type SolverConfig struct {
	MinProfitMargin float64
	MaxBidAmount    *big.Int
	BidIncrement    *big.Int
	BalanceBuffer   float64
}

// RecipientInfo stores anonymous recipient information (TEE simulation)
type RecipientInfo struct {
	Success     bool      `json:"success"`
	IntentID    string    `json:"intentId"`
	Recipients  []string  `json:"recipients"`
	Amounts     []string  `json:"amounts"`
	Timestamp   int64     `json:"timestamp"`
	ChainID     int64     `json:"chainId"`
	TotalAmount string    `json:"totalAmount"`
}

// RelayerResponse represents the response from relayer settlement
type RelayerResponse struct {
	Success         bool   `json:"success"`
	TransactionHash string `json:"transactionHash,omitempty"`
	BlockNumber     uint64 `json:"blockNumber,omitempty"`
	Network         string `json:"network,omitempty"`
	ChainID         int64  `json:"chainId,omitempty"`
	IntentID        string `json:"intentId,omitempty"`
	Chain2IntentID  string `json:"chain2IntentId,omitempty"`
	SolverAddress   string `json:"solverAddress,omitempty"`
	Error           string `json:"error,omitempty"`
}

// StoreRecipientsRequest is the request body for storing recipients
type StoreRecipientsRequest struct {
	IntentID   string   `json:"intentId" binding:"required"`
	Recipients []string `json:"recipients" binding:"required"`
	Amounts    []string `json:"amounts" binding:"required"`
	ChainID    int64    `json:"chainId" binding:"required"`
}

// SettleRequest is the request body for settling an intent
type SettleRequest struct {
	IntentID        string `json:"intentId" binding:"required"`
	Network         string `json:"network" binding:"required"`
	Chain2IntentID  string `json:"chain2IntentId" binding:"required"`
	SolverAddress   string `json:"solverAddress" binding:"required"`
	DestinationChain string `json:"destinationChain" binding:"required"`
}

// VerifyRequest is the request body for verifying an intent
type VerifyRequest struct {
	IntentID string `json:"intentId" binding:"required"`
	Network  string `json:"network" binding:"required"`
}

// IntentStatus represents the tracking status of an intent in the solver
type IntentStatus struct {
	IntentID      string    `json:"intentId"`
	Network       string    `json:"network"`
	Status        string    `json:"status"`
	OurBid        string    `json:"ourBid,omitempty"`
	HighestBid    string    `json:"highestBid,omitempty"`
	IsWinner      bool      `json:"isWinner"`
	Deposited     bool      `json:"deposited"`
	Completed     bool      `json:"completed"`
	LastUpdated   time.Time `json:"lastUpdated"`
}

// HealthResponse is the response for health check endpoints
type HealthResponse struct {
	Status       string            `json:"status"`
	Service      string            `json:"service"`
	Timestamp    string            `json:"timestamp"`
	Networks     []string          `json:"networks,omitempty"`
	ActiveIntents int              `json:"activeIntents,omitempty"`
	ChainInfo    map[string]string `json:"chainInfo,omitempty"`
}

// SolverStatusResponse provides detailed solver status
type SolverStatusResponse struct {
	IsRunning     bool                       `json:"isRunning"`
	Networks      map[string]NetworkConfig   `json:"networks"`
	Config        SolverConfig               `json:"config"`
	ActiveIntents map[string]IntentStatus    `json:"activeIntents"`
	WalletAddress string                     `json:"walletAddress"`
	Balances      map[string]string          `json:"balances"`
}
