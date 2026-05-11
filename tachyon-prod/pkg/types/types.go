package types

import (
	"math/big"
	"time"
)

// ChainType discriminates the on-chain runtime of a configured chain.
// EVM chains use go-ethereum + secp256k1 keys; Solana uses solana-go +
// ed25519 keys + the Anchor bridge_intent program.
type ChainType string

const (
	ChainTypeEVM    ChainType = "evm"
	ChainTypeSolana ChainType = "solana"
)

// NetworkConfig defines blockchain network configuration.
//
// EVM-only fields (BridgeAddress, SafeAddress, Multicall3Address) are
// ignored when ChainType == ChainTypeSolana. Solana-only fields
// (SolanaProgramID, SolanaUSDCMint, SolanaWSOLMint, HeliusRPCURL,
// SolanaCommitment, SolanaPriorityFeeMicroLamports) are ignored for EVM.
//
// `ChainID` for Solana is the LayerZero EID (30168 mainnet, 40168 devnet)
// to keep the cross-chain composite key (sourceChainId, intentId) globally
// unique across both runtimes.
type NetworkConfig struct {
	Name              string
	ChainType         ChainType
	RpcURL            string
	ChainID           *big.Int
	BridgeAddress     string
	Explorer          string
	SafeAddress       string // EVM only — Gnosis Safe holding liquidity on this chain
	Multicall3Address string // EVM only — Multicall3 contract; defaults to canonical 0xcA11...

	// Solana-only fields
	SolanaProgramID                string // Anchor `bridge_intent` program ID (base58)
	SolanaUSDCMint                 string // SPL USDC mint address on this Solana cluster
	SolanaWSOLMint                 string // wrapped SOL mint (canonical: So11...112)
	HeliusRPCURL                   string // optional Helius RPC; falls back to RpcURL
	SolanaCommitment               string // "processed" | "confirmed" | "finalized"; default "confirmed"
	SolanaPriorityFeeMicroLamports uint64 // priority fee per CU; 0 disables
}

// SolanaConfigPublic is the FE-safe view of a Solana chain's config —
// exactly the fields a frontend wallet needs to construct transactions
// against the bridge_intent program. Excludes any operator secrets
// (signing keys, private RPC tokens).
type SolanaConfigPublic struct {
	Name       string `json:"name"`
	ChainID    string `json:"chainId"` // LayerZero EID, stringified
	ProgramID  string `json:"programId"`
	USDCMint   string `json:"usdcMint"`
	WSOLMint   string `json:"wsolMint"`
	Commitment string `json:"commitment"`
	Explorer   string `json:"explorer,omitempty"`
}

// IsSolana reports whether the chain is configured as a Solana cluster.
func (n *NetworkConfig) IsSolana() bool { return n.ChainType == ChainTypeSolana }

// IsEVM reports whether the chain is configured as an EVM chain. EVM is the
// default when ChainType is empty (so existing configs keep working).
func (n *NetworkConfig) IsEVM() bool {
	return n.ChainType == "" || n.ChainType == ChainTypeEVM
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

	// UserDailyCapUSDCents is the per-user daily $-value ceiling, expressed in
	// US dollar cents (int64). Each intent's USD value is computed at bid
	// decision time via the price oracle (`amountA / 10^decimalsA * priceA`)
	// and added to the user's running total. Intents that would push a user
	// over this ceiling are skipped. Set via SOLVER_USER_DAILY_CAP_USD as a
	// human-readable USD figure (e.g. "2000" for $2,000); zero/absent disables.
	UserDailyCapUSDCents int64
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

// RegisterSolverRequest is the request body for solver registration.
// Type lets a single relayer index multiple service kinds:
//
//	"tachyon"        — the original solver (default if Type is empty for
//	                   backwards compatibility)
//	"lifiXtach"      — privacy router routing legs through LI.FI
//	"stargateXtach"  — privacy router routing legs through Stargate
//
// Frontends discover the available routes per bridge by calling
// GET /solvers?type=<value>. Tokens is an optional sidecar dictionary
// of (chain, symbol) → {address, decimals} entries used by the routes
// — surfaced verbatim on /solvers so the frontend doesn't need its own
// token registry.
type RegisterSolverRequest struct {
	Type          string      `json:"type,omitempty"`
	SolverAddress string      `json:"solverAddress" binding:"required"`
	Routes        []Route     `json:"routes" binding:"required"`
	Tokens        []TokenInfo `json:"tokens,omitempty"`
}

// TokenInfo describes a single token on a single chain. Address is
// the on-chain ERC-20 contract for stables / OFTs and the LI.FI
// "native" sentinel (0xEee…EEeE) for native gas tokens like ETH.
type TokenInfo struct {
	ChainID  string `json:"chainId"`
	Symbol   string `json:"symbol"`
	Address  string `json:"address"`
	Decimals uint8  `json:"decimals"`
}

// RegisterSolverResponse is the response from solver registration
type RegisterSolverResponse struct {
	Success      bool            `json:"success"`
	SolverAddress string         `json:"solverAddress"`
	RoutesCount  int             `json:"routesCount"`
	Balances     []SolverBalance `json:"balances"`
	Error        string          `json:"error,omitempty"`
}

// GetRoutesRequest is the request body for querying routes. Type narrows
// results to a specific bridge kind ("tachyon", "lifiXtach",
// "stargateXtach"); empty returns all kinds.
type GetRoutesRequest struct {
	Type        string `json:"type,omitempty"`
	SourceChain string `json:"sourceChain,omitempty"`
	DestChain   string `json:"destChain,omitempty"`
	TokenA      string `json:"tokenA,omitempty"`
	TokenB      string `json:"tokenB,omitempty"`
}

// SolverInfo represents information about a registered solver. Type is
// the bridge kind ("tachyon", "lifiXtach", "stargateXtach"); empty maps
// to "tachyon" for backwards compatibility with old registrations
// persisted before the field existed.
type SolverInfo struct {
	Type         string          `json:"type,omitempty"`
	Address      string          `json:"address"`
	Routes       []Route         `json:"routes"`
	Tokens       []TokenInfo     `json:"tokens,omitempty"`
	Balances     []SolverBalance `json:"balances"`
	RegisteredAt time.Time       `json:"registeredAt"`
	LastSeen     time.Time       `json:"lastSeen"`
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

// StealthAddress represents a single stealth address generated for a recipient
type StealthAddress struct {
	Address        string `json:"address"`        // The stealth address (ETH address)
	EphemeralPubKey string `json:"ephemeralPubKey"` // Ephemeral public key used to derive this address
	ChainID        string `json:"chainId"`         // Chain where funds were sent
	IntentID       string `json:"intentId"`        // Intent that triggered this stealth address
	SafeDeployed   bool   `json:"safeDeployed"`    // Whether a Safe has been deployed at this address
	SafeAddress    string `json:"safeAddress,omitempty"` // Deployed Safe address (if any)
	CreatedAt      int64  `json:"createdAt"`
}

// RecipientAccount stores the TEE-generated keys and stealth addresses for a recipient
type RecipientAccount struct {
	RecipientAddress string           `json:"recipientAddress"` // The recipient's public EOA address
	ViewPrivKey      string           `json:"viewPrivKey"`      // Hex-encoded viewing private key (TEE-held)
	SpendPrivKey     string           `json:"spendPrivKey"`     // Hex-encoded spending private key (TEE-held)
	SpendPubKey      string           `json:"spendPubKey"`      // Hex-encoded spending public key
	ViewPubKey       string           `json:"viewPubKey"`       // Hex-encoded viewing public key
	StealthAddresses []StealthAddress `json:"stealthAddresses"` // All stealth addresses for this recipient
	Nonce            uint64           `json:"nonce"`            // Next nonce for stealth address generation
	CreatedAt        int64            `json:"createdAt"`
	UpdatedAt        int64            `json:"updatedAt"`
}

// RegisterRecipientRequest is sent by the recipient to register/fetch their keys
type RegisterRecipientRequest struct {
	RecipientAddress string `json:"recipientAddress" binding:"required"`
	Signature        string `json:"signature" binding:"required"` // Signature of "REGISTER_STEALTH:<address>" to prove ownership
}

// SolanaStealthRecord holds one (intent, salt) entry for a Solana recipient.
// Salt can be either:
//   - Persisted explicitly (legacy /store-salt flow — Salt field set, Nonce==0)
//   - Re-derivable via HMAC(viewSecret, intentID || chainID || nonce-LE) when
//     the new register + generate-salt flow was used (Salt field empty,
//     Nonce set to the derivation index). The relayer recomputes Salt on
//     each GetSolanaStealthSalts call and never persists it to disk.
type SolanaStealthRecord struct {
	IntentID   string `json:"intentId"`
	ChainID    string `json:"chainId"`
	Salt       string `json:"salt,omitempty"`  // hex 32 bytes (legacy path) or empty (derived path)
	Nonce      uint64 `json:"nonce,omitempty"` // derivation index for HMAC path
	StealthPDA string `json:"stealthPda"`      // base58 — convenience for FE
	CreatedAt  int64  `json:"createdAt"`
}

// SolanaRecipientAccount mirrors EVM's RecipientAccount for Solana
// ed25519 recipients. The single sensitive field is `ViewSecret` —
// a 32-byte HMAC key that, combined with (intentID, chainID, nonce),
// deterministically produces every per-intent salt. This matches the
// EVM "one keypair per recipient, derive everything else" pattern.
//
// The Nonce field is the monotonic counter incremented on each
// /generate-salt call. Records[] holds the metadata (intentID, chainID,
// nonce, stealthPDA) needed to re-derive the salts later.
type SolanaRecipientAccount struct {
	RecipientPubkey string                `json:"recipientPubkey"` // base58 ed25519 pubkey
	ViewSecret      string                `json:"viewSecret,omitempty"` // hex 32 bytes; empty for legacy-only accounts
	Nonce           uint64                `json:"nonce"`           // next-to-use derivation index
	Records         []SolanaStealthRecord `json:"records"`
	CreatedAt       int64                 `json:"createdAt"`
	UpdatedAt       int64                 `json:"updatedAt"`
}

// RegisterSolanaRecipientRequest registers a recipient and generates
// their per-account viewSecret (sealed in a TEE deployment; plaintext
// today). Idempotent — re-register returns the existing account.
type RegisterSolanaRecipientRequest struct {
	RecipientPubkey string `json:"recipientPubkey" binding:"required"`
	Timestamp       int64  `json:"timestamp" binding:"required"`
	Signature       string `json:"signature" binding:"required"` // hex ed25519 sig over "REGISTER_SOLANA_STEALTH:<pubkey>:<timestamp>"
}

type RegisterSolanaRecipientResponse struct {
	Success         bool   `json:"success"`
	RecipientPubkey string `json:"recipientPubkey,omitempty"`
	AlreadyExists   bool   `json:"alreadyExists,omitempty"`
	Error           string `json:"error,omitempty"`
}

// GenerateSolanaSaltRequest asks the relayer for the next deterministic
// salt for (recipient, intentID, chainID). Unauthenticated, mirroring
// the EVM /recipient/generate-stealth pattern (sender can ask on the
// recipient's behalf; possession of the returned salt alone never
// allows theft since sweep_stealth still requires recipient signature).
type GenerateSolanaSaltRequest struct {
	RecipientPubkey string `json:"recipientPubkey" binding:"required"`
	IntentID        string `json:"intentId" binding:"required"`
	ChainID         string `json:"chainId" binding:"required"`
	ProgramID       string `json:"programId" binding:"required"` // base58 bridge_intent program ID for PDA derivation
}

type GenerateSolanaSaltResponse struct {
	Success    bool   `json:"success"`
	Salt       string `json:"salt,omitempty"`       // hex 32 bytes
	Nonce      uint64 `json:"nonce,omitempty"`      // index used; debug/audit
	StealthPDA string `json:"stealthPda,omitempty"` // base58, derived from ["stealth", recipient, salt]
	Error      string `json:"error,omitempty"`
}

// StoreSolanaStealthRequest stores a salt for a recipient. No signature
// required (the sender stores; leaking the salt only allows pattern
// correlation, not theft — since sweep_stealth still requires the
// recipient's signature). In production a sealed-box should be used.
type StoreSolanaStealthRequest struct {
	RecipientPubkey string `json:"recipientPubkey" binding:"required"`
	IntentID        string `json:"intentId" binding:"required"`
	ChainID         string `json:"chainId" binding:"required"`
	Salt            string `json:"salt" binding:"required"`       // hex 32 bytes
	StealthPDA      string `json:"stealthPda,omitempty"`          // base58, optional
}

// GetSolanaStealthSaltsRequest is auth'd by an ed25519 signature over
// "GET_SOLANA_STEALTH:<recipientPubkey>:<timestamp>". The timestamp
// prevents trivial replay.
type GetSolanaStealthSaltsRequest struct {
	RecipientPubkey string `json:"recipientPubkey" binding:"required"`
	Timestamp       int64  `json:"timestamp" binding:"required"`
	Signature       string `json:"signature" binding:"required"` // hex ed25519 sig
}

type GetSolanaStealthSaltsResponse struct {
	Success bool                  `json:"success"`
	Records []SolanaStealthRecord `json:"records,omitempty"`
	Error   string                `json:"error,omitempty"`
}

// RegisterRecipientResponse returns the recipient's public keys and stealth addresses
type RegisterRecipientResponse struct {
	Success          bool             `json:"success"`
	SpendPubKey      string           `json:"spendPubKey"`
	ViewPubKey       string           `json:"viewPubKey"`
	StealthAddresses []StealthAddress `json:"stealthAddresses"`
	Error            string           `json:"error,omitempty"`
}

// GetStealthAddressesResponse returns all stealth addresses for a recipient
type GetStealthAddressesResponse struct {
	Success          bool             `json:"success"`
	StealthAddresses []StealthAddress `json:"stealthAddresses"`
	Error            string           `json:"error,omitempty"`
}

// DeploySafeRequest is sent to deploy a Safe on a stealth address
// Anyone can call this, but must provide a valid spend key signature
type DeploySafeRequest struct {
	StealthAddress string `json:"stealthAddress" binding:"required"` // The stealth address to deploy Safe on
	Signature      string `json:"signature" binding:"required"`      // Signature from spend private key over "DEPLOY_SAFE:<stealthAddress>:<chainId>"
	ChainID        string `json:"chainId" binding:"required"`        // Chain to deploy Safe on
}

// DeploySafeResponse returns the deployed Safe details
type DeploySafeResponse struct {
	Success     bool   `json:"success"`
	SafeAddress string `json:"safeAddress,omitempty"`
	TxHash      string `json:"txHash,omitempty"`
	Error       string `json:"error,omitempty"`
}

// GenerateStealthRequest is used internally when a sender sends to a recipient
// The TEE generates a new stealth address for the recipient
type GenerateStealthRequest struct {
	RecipientAddress string `json:"recipientAddress" binding:"required"`
	ChainID          string `json:"chainId" binding:"required"`
	IntentID         string `json:"intentId" binding:"required"`
}

// GenerateStealthResponse returns the stealth EOA and its predicted Safe address
type GenerateStealthResponse struct {
	StealthAddress string `json:"stealthAddress"`
	SafeAddress    string `json:"safeAddress"`
}

// DeriveStealthKeyRequest is sent by the recipient to get the stealth private key
type DeriveStealthKeyRequest struct {
	RecipientAddress string `json:"recipientAddress" binding:"required"`
	StealthAddress   string `json:"stealthAddress" binding:"required"`
	Signature        string `json:"signature" binding:"required"` // Signs "DERIVE_STEALTH_KEY:<recipient>:<stealth>"
}

// DeriveStealthKeyResponse returns the derived stealth private key
type DeriveStealthKeyResponse struct {
	Success           bool   `json:"success"`
	StealthAddress    string `json:"stealthAddress"`
	StealthPrivateKey string `json:"stealthPrivateKey"` // Hex-encoded private key
	Error             string `json:"error,omitempty"`
}

// RelayProxyRequest asks the relayer to broadcast a pre-signed Safe transaction.
// The caller derives the stealth key, signs the Safe tx hash themselves, and
// sends the signature here. The relayer pays gas and calls execTransaction.
type RelayProxyRequest struct {
	SafeAddress string `json:"safeAddress" binding:"required"` // Safe proxy address
	ChainID     string `json:"chainId" binding:"required"`
	// execTransaction params
	To        string `json:"to" binding:"required"`   // target address
	Value     string `json:"value"`                   // ETH value in wei (default "0")
	Data      string `json:"data"`                    // hex-encoded calldata (default "0x")
	Operation uint8  `json:"operation"`               // 0=Call, 1=DelegateCall
	Signature string `json:"signature" binding:"required"` // 65-byte sig from stealth key over Safe tx hash
}

// RelayProxyResponse returns the broadcast tx hash
type RelayProxyResponse struct {
	Success bool   `json:"success"`
	TxHash  string `json:"txHash,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TransactionRecord stores the full lifecycle of an on-chain intent for user-facing history
type TransactionRecord struct {
	IntentID        string     `json:"intentId"`
	UserAddress     string     `json:"userAddress"`
	SourceChainID   string     `json:"sourceChainId"`
	DestChainID     string     `json:"destChainId"`
	TokenA          string     `json:"tokenA"`
	TokenB          string     `json:"tokenB"`
	AmountA         string     `json:"amountA"`
	ExpectedAmountB string     `json:"expectedAmountB"`
	Status          string     `json:"status"` // "pending" | "completed"
	DepositTxHash   string     `json:"depositTxHash,omitempty"`
	SolveTxHash     string     `json:"solveTxHash,omitempty"`
	SettleTxHash    string     `json:"settleTxHash,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

// TransactionPageResponse is the paginated response for transaction history
type TransactionPageResponse struct {
	Success      bool                 `json:"success"`
	Transactions []*TransactionRecord `json:"transactions"`
	Page         int                  `json:"page"`
	Limit        int                  `json:"limit"`
	Total        int                  `json:"total"`
	TotalPages   int                  `json:"totalPages"`
	Error        string               `json:"error,omitempty"`
}

// CreateTransactionRequest is sent by the FE to record a pending intent immediately.
type CreateTransactionRequest struct {
	IntentID        string `json:"intentId" binding:"required"`
	UserAddress     string `json:"userAddress" binding:"required"`
	SourceChainID   string `json:"sourceChainId" binding:"required"`
	DestChainID     string `json:"destChainId" binding:"required"`
	TokenA          string `json:"tokenA" binding:"required"`
	TokenB          string `json:"tokenB" binding:"required"`
	AmountA         string `json:"amountA" binding:"required"`
	ExpectedAmountB string `json:"expectedAmountB" binding:"required"`
	DepositTxHash   string `json:"depositTxHash,omitempty"`
}

// CompleteTransactionRequest is sent by the solver after settling.
// It carries the full intent object so the relayer can upsert: if the record
// already exists it is updated; if not, a new completed record is created.
type CompleteTransactionRequest struct {
	SourceChainID   string `json:"sourceChainId" binding:"required"`
	IntentID        string `json:"intentId" binding:"required"`
	UserAddress     string `json:"userAddress,omitempty"`
	DestChainID     string `json:"destChainId,omitempty"`
	TokenA          string `json:"tokenA,omitempty"`
	TokenB          string `json:"tokenB,omitempty"`
	AmountA         string `json:"amountA,omitempty"`
	ExpectedAmountB string `json:"expectedAmountB,omitempty"`
	DepositTxHash   string `json:"depositTxHash,omitempty"`
	SolveTxHash     string `json:"solveTxHash,omitempty"`
	SettleTxHash    string `json:"settleTxHash,omitempty"`
}

// RelayBatchItem is a single Safe transaction inside a POST /recipient/relay-batch request.
// The caller pre-signs the SafeTxHash (identical to relay-proxy) for each Safe independently.
// All items in a request must target the same chainId.
type RelayBatchItem struct {
	SafeAddress string `json:"safeAddress" binding:"required"`
	ChainID     string `json:"chainId" binding:"required"`
	To          string `json:"to" binding:"required"`
	Value       string `json:"value"`     // wei string, default "0"
	Data        string `json:"data"`      // hex-encoded calldata, default "0x"
	Operation   uint8  `json:"operation"` // 0=Call, 1=DelegateCall
	Signature   string `json:"signature" binding:"required"` // 65-byte sig over Safe tx hash
}

// RelayBatchRequest is the body for POST /recipient/relay-batch.
// Encodes all execTransaction calls into one Multicall3.aggregate3 transaction.
// AllowFailure controls atomicity: false (default) = all-or-nothing; true = best-effort.
type RelayBatchRequest struct {
	Items        []RelayBatchItem `json:"items" binding:"required"`
	AllowFailure bool             `json:"allowFailure"` // default false = atomic
}

// RelayBatchResponse is returned after the batch is submitted on-chain.
type RelayBatchResponse struct {
	Success bool   `json:"success"`
	TxHash  string `json:"txHash,omitempty"`
	Error   string `json:"error,omitempty"`
}
