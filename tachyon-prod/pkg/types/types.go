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

// ChainPair represents a source-destination chain pair configuration
type ChainPair struct {
	SourceChain NetworkConfig
	DestChain   NetworkConfig
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
	DestChainId     *big.Int // Destination chain where tokens should be delivered
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
	TransactionHash string `json:"transactionHash,omitempty"` // Deprecated: use SettleTxHash
	SettleTxHash    string `json:"settleTxHash,omitempty"`    // Settlement transaction hash on source chain
	DepositTxHash   string `json:"depositTxHash,omitempty"`   // Deposit transaction hash on source chain
	SolveTxHash     string `json:"solveTxHash,omitempty"`     // Solve transaction hash on destination chain
	BlockNumber     uint64 `json:"blockNumber,omitempty"`
	Network         string `json:"network,omitempty"`
	ChainID         int64  `json:"chainId,omitempty"`
	IntentID        string `json:"intentId,omitempty"`
	Chain2IntentID  string `json:"chain2IntentId,omitempty"`
	SolverAddress   string `json:"solverAddress,omitempty"`
	Error           string `json:"error,omitempty"`
}

// StoreRecipientsRequest is the request body for storing recipients
// Supports both encrypted and unencrypted formats for backward compatibility
type StoreRecipientsRequest struct {
	IntentID   string   `json:"intentId" binding:"required"`
	Recipients []string `json:"recipients,omitempty"` // Deprecated: use EncryptedData instead
	Amounts    []string `json:"amounts,omitempty"`    // Deprecated: use EncryptedData instead
	ChainID    int64    `json:"chainId" binding:"required"`

	// New encrypted format - hex-encoded ECIES encrypted data
	// The plaintext should be JSON: {"recipients": [...], "amounts": [...]}
	EncryptedData string `json:"encryptedData,omitempty"`
}

// SettleRequest is the request body for settling an intent
type SettleRequest struct {
	IntentID         string `json:"intentId" binding:"required"`
	Network          string `json:"network" binding:"required"`
	Chain2IntentID   string `json:"chain2IntentId" binding:"required"`
	SolverAddress    string `json:"solverAddress" binding:"required"`
	DestinationChain string `json:"destinationChain" binding:"required"`

	// Transaction hashes for frontend display
	DepositTxHash    string `json:"depositTxHash,omitempty"`    // Deposit transaction on source chain
	SolveTxHash      string `json:"solveTxHash,omitempty"`      // Solve transaction on destination chain
}

// VerifyRequest is the request body for verifying an intent
type VerifyRequest struct {
	IntentID      string `json:"intentId" binding:"required"`
	Network       string `json:"network" binding:"required"`       // Destination chain where intent was solved
	SourceChainID string `json:"sourceChainId" binding:"required"` // Source chain where intent was created
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
	DepositTxHash string    `json:"depositTxHash,omitempty"` // Transaction hash of deposit on source chain
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

// Route represents a supported trading route between chains
type Route struct {
	SourceChain string `json:"sourceChain"`
	DestChain   string `json:"destChain"`
	TokenA      string `json:"tokenA"`
	TokenB      string `json:"tokenB"`
}

// SolverBalance represents token balance on a specific chain
type SolverBalance struct {
	Chain   string `json:"chain"`
	Token   string `json:"token"`
	Balance string `json:"balance"`
}

// RegisterSolverRequest is the request body for solver registration
type RegisterSolverRequest struct {
	SolverAddress string  `json:"solverAddress" binding:"required"`
	Routes        []Route `json:"routes" binding:"required"`
}

// RegisterSolverResponse is the response from solver registration
type RegisterSolverResponse struct {
	Success      bool            `json:"success"`
	SolverAddress string         `json:"solverAddress"`
	RoutesCount  int             `json:"routesCount"`
	Balances     []SolverBalance `json:"balances"`
	Error        string          `json:"error,omitempty"`
}

// GetRoutesRequest is the request body for querying routes
type GetRoutesRequest struct {
	SourceChain string `json:"sourceChain,omitempty"`
	DestChain   string `json:"destChain,omitempty"`
	TokenA      string `json:"tokenA,omitempty"`
	TokenB      string `json:"tokenB,omitempty"`
}

// SolverInfo represents information about a registered solver
type SolverInfo struct {
	Address      string          `json:"address"`
	Routes       []Route         `json:"routes"`
	Balances     []SolverBalance `json:"balances"`
	RegisteredAt time.Time       `json:"registeredAt"`
	LastSeen     time.Time       `json:"lastSeen"`
}

// TWAPOrder represents a private TWAP order managed by the TEE
type TWAPOrder struct {
	ID              string    `json:"id"`
	User            string    `json:"user"`            // User wallet address
	AssetIn         string    `json:"assetIn"`         // Source token address (on source chain)
	AssetOut        string    `json:"assetOut"`        // Destination token address (on dest chain)
	ChainIn         string    `json:"chainIn"`         // Source chain name (e.g. "base")
	ChainOut        string    `json:"chainOut"`        // Destination chain name (e.g. "robinhood")
	SourceChainID   int64     `json:"sourceChainId"`   // Chain where funds are pulled from
	DestChainID     int64     `json:"destChainId"`     // Chain where tokens are delivered
	TotalAmountIn   string    `json:"totalAmountIn"`   // Total amount of assetIn to spend
	AmountPerPeriod string    `json:"amountPerPeriod"` // Amount of assetIn per tranche
	Period          int64     `json:"period"`          // Seconds between tranches
	StartTime       int64     `json:"startTime"`       // Unix timestamp to start
	EndTime         int64     `json:"endTime"`         // Unix timestamp to stop
	PermitSignature string    `json:"permitSignature"` // Permit2 signature for pulling funds
	Recipients      []string  `json:"recipients"`      // Destination recipients (encrypted at rest)
	Amounts         []string  `json:"amounts"`         // Per-recipient amounts (proportional)
	Status          string    `json:"status"`          // "active", "paused", "completed", "cancelled"
	TranchesFilled  int       `json:"tranchesFilled"`  // Number of tranches executed
	TotalTranches   int       `json:"totalTranches"`   // Total expected tranches
	AmountSpent     string    `json:"amountSpent"`     // Total assetIn spent so far
	IntentIDs       []string  `json:"intentIds"`       // On-chain intent IDs created per tranche (frontend can check status directly)
	CreatedAt       time.Time `json:"createdAt"`
	LastExecutedAt  time.Time `json:"lastExecutedAt,omitempty"`
}

// CreateTWAPRequest is the request body for creating a TWAP order
// The entire request is ECIES-encrypted by the client
type CreateTWAPRequest struct {
	EncryptedData string `json:"encryptedData" binding:"required"` // ECIES encrypted JSON of TWAPOrderParams
	Signature     string `json:"signature" binding:"required"`     // Hex-encoded signature of the message
	Message       string `json:"message" binding:"required"`       // Message that was signed (e.g. "tachyon-twap:<userAddress>:<timestamp>")
}

// TWAPOrderParams is the decrypted contents of a CreateTWAPRequest
type TWAPOrderParams struct {
	User            string   `json:"user"`
	AssetIn         string   `json:"assetIn"`
	AssetOut        string   `json:"assetOut"`
	ChainIn         string   `json:"chainIn"`         // Source chain name
	ChainOut        string   `json:"chainOut"`        // Destination chain name
	SourceChainID   int64    `json:"sourceChainId"`
	DestChainID     int64    `json:"destChainId"`
	TotalAmountIn   string   `json:"totalAmountIn"`
	AmountPerPeriod string   `json:"amountPerPeriod"`
	Period          int64    `json:"period"`          // seconds
	StartTime       int64    `json:"startTime"`
	EndTime         int64    `json:"endTime"`
	PermitSignature string   `json:"permitSignature"`
	Recipients      []string `json:"recipients"`
	Amounts         []string `json:"amounts"`         // proportional split per recipient
}

// TWAPOrderResponse is the response after creating a TWAP order
type TWAPOrderResponse struct {
	Success bool   `json:"success"`
	OrderID string `json:"orderId,omitempty"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TWAPStatusResponse is the response for querying TWAP order status
type TWAPStatusResponse struct {
	Success        bool     `json:"success"`
	OrderID        string   `json:"orderId"`
	Status         string   `json:"status"`
	TranchesFilled int      `json:"tranchesFilled"`
	TotalTranches  int      `json:"totalTranches"`
	AmountSpent    string   `json:"amountSpent"`
	IntentIDs      []string `json:"intentIds"`       // On-chain intent IDs for frontend to check contract status
	SourceChainID  int64    `json:"sourceChainId"`   // Chain where intents were created
	Error          string   `json:"error,omitempty"`
}

// GetRoutesResponse is the response from get routes endpoint
type GetRoutesResponse struct {
	Success bool         `json:"success"`
	Routes  []Route      `json:"routes"`
	Solvers []SolverInfo `json:"solvers"`
	Error   string       `json:"error,omitempty"`
}

// IntentDetails stores complete intent information including transaction hashes
type IntentDetails struct {
	IntentID         string    `json:"intentId"`
	SourceChainID    string    `json:"sourceChainId"`
	DestChainID      string    `json:"destChainId"`
	SolverAddress    string    `json:"solverAddress"`
	DepositTxHash    string    `json:"depositTxHash,omitempty"`
	SolveTxHash      string    `json:"solveTxHash,omitempty"`
	SettleTxHash     string    `json:"settleTxHash,omitempty"`
	Status           string    `json:"status"`           // "pending", "solving", "settled", "completed"
	Timestamp        time.Time `json:"timestamp"`
	SettledAt        time.Time `json:"settledAt,omitempty"`
}

// GetIntentDetailsResponse is the response for intent details query
type GetIntentDetailsResponse struct {
	Success bool           `json:"success"`
	Intent  *IntentDetails `json:"intent,omitempty"`
	Error   string         `json:"error,omitempty"`
}
