package privacy

import (
	"math/big"
	"time"
)

// Status is the lifecycle stage of a single privacy bridge intent.
type Status string

const (
	StatusPending   Status = "pending"   // intent received, waiting for inbound funds
	StatusFunded    Status = "funded"    // inbound Safe has the required token balance
	StatusSwept     Status = "swept"     // funds moved from inbound Safe → TEE Safe
	StatusBatching  Status = "batching"  // in-bucket waiting for batch trigger or jitter timer
	StatusBridging  Status = "bridging"  // Stargate send submitted from TEE → dest stealth
	StatusDelivered Status = "delivered" // verified delivery on dest chain
	StatusFailed    Status = "failed"    // unrecoverable error (kept for visibility)
	StatusRefunded  Status = "refunded"  // funds returned from TEE Safe to user EOA after a swept/failed intent
)

// BridgeIntent is a single user's bridge request after decryption.
//
// All address/amount fields are validated at decrypt time. The service
// derives the inbound stealth Safe address from the user-provided
// recipient pubkey via the same derivation the solver uses for stealth
// recipients (see pkg/stealth) — the user proved control by signing the
// register-stealth EIP-712 envelope at session-creation time.
type BridgeIntent struct {
	// Stable identifier for the bridge session — returned to the user so
	// they can poll status, and indexed in PebbleDB.
	SessionID string `json:"sessionId"`

	// User's externally-owned address (proven via EIP-712 RegisterStealth
	// signature at the time the inbound stealth Safe was registered).
	UserAddress string `json:"userAddress"`

	// Source side: which chain the user is depositing from, the token
	// they are depositing, the per-session inbound stealth Safe address
	// (TEE-controlled), and the amount they intend to bridge.
	SourceChainID string   `json:"sourceChainId"`
	SourceToken   Token    `json:"sourceToken"`
	InboundSafe   string   `json:"inboundSafe"`
	Amount        *big.Int `json:"amount"`

	// Destination side: which chain to deliver to, which token, and the
	// destination stealth Safe address (user-controlled, derived from
	// the user's outbound viewing/spending keypair).
	DestChainID    string `json:"destChainId"`
	DestToken      Token  `json:"destToken"`
	DestStealthSafe string `json:"destStealthSafe"`

	// Lifecycle.
	Status  Status    `json:"status"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`

	// FireAt is the deadline by which this intent must be dispatched
	// even as a singleton. Set when the intent transitions to swept,
	// drawn uniformly from [PRIVACY_FIRE_MIN_S, PRIVACY_FIRE_MAX_S]
	// (defaults: 20s, 60s) — replacing the previous batchMaxAge +
	// jitter combo. A batch can fire earlier if size ≥ batchMinSize.
	FireAt time.Time `json:"fireAt,omitempty"`

	// Tx hashes accumulated as the lifecycle progresses.
	SweepTxHash    string `json:"sweepTxHash,omitempty"`
	BridgeTxHash   string `json:"bridgeTxHash,omitempty"`
	DeliveredTxHash string `json:"deliveredTxHash,omitempty"`
	RefundTxHash   string `json:"refundTxHash,omitempty"` // populated when /refund is called

	// Error message captured if Status == StatusFailed.
	Error string `json:"error,omitempty"`
}

// BatchKey identifies a bucket. Intents in the same bucket can be sent in
// the same logical batch (same source/dest token pair, same chains).
type BatchKey struct {
	SourceChainID string
	SourceToken   Token
	DestChainID   string
	DestToken     Token
}

func (b BatchKey) String() string {
	return string(b.SourceChainID) + ":" + string(b.SourceToken) + "->" +
		string(b.DestChainID) + ":" + string(b.DestToken)
}

// SubmitRequest is the public POST /bridge body. The "Encrypted" field is
// an ECIES-encrypted JSON BridgeIntentPayload (see decrypt.go) blobbed as
// hex string. The receiver decrypts with the service's ECIES private key.
type SubmitRequest struct {
	Encrypted string `json:"encrypted"`
}

type SubmitResponse struct {
	Success   bool   `json:"success"`
	SessionID string `json:"sessionId,omitempty"`
	// DestStealthSafe is the predicted Safe address on the destination
	// chain that will receive the bridged funds. In mode 1 (relayer-
	// managed stealth) this is the address the relayer just generated;
	// the FE should display it to the user as "your funds will land
	// here". In mode 2 (FE-managed) it echoes back the address the
	// caller put in the encrypted payload.
	DestStealthSafe string `json:"destStealthSafe,omitempty"`
	Error           string `json:"error,omitempty"`
}

// BridgeIntentPayload is the cleartext shape inside the encrypted blob.
//
// The privacy router supports two stealth-Safe sourcing modes:
//
//  1. Relayer-managed (preferred): caller sets RegisterStealthSig to
//     an EIP-712 signature of `RegisterStealth(userAddress)` and
//     leaves DestStealthSafe empty. The privacy router calls the
//     relayer's /recipient/register + /recipient/generate-stealth on
//     the user's behalf, gets back a fresh stealth Safe owned by a
//     TEE-derivable stealth EOA, and uses that as the bridge dest.
//     User can later recover via the relayer's existing
//     /recipient/derive-stealth-key + /recipient/deploy-safe flow
//     with their wallet signature — no client-side keypair to
//     persist.
//
//  2. FE-managed (legacy): caller generates the stealth keypair
//     client-side, computes the CREATE2 Safe address, sets
//     DestStealthSafe to that address, leaves RegisterStealthSig
//     empty. FE bears the burden of persisting/deriving the stealth
//     privkey to ever sweep the funds.
//
// Mode 1 is selected iff RegisterStealthSig is non-empty.
type BridgeIntentPayload struct {
	SessionID            string `json:"sessionId"`
	UserAddress          string `json:"userAddress"`
	SourceChainID        string `json:"sourceChainId"`
	SourceToken          Token  `json:"sourceToken"`
	InboundSafe          string `json:"inboundSafe"`
	AmountStr            string `json:"amount"` // big.Int as decimal string
	DestChainID          string `json:"destChainId"`
	DestToken            Token  `json:"destToken"`
	DestStealthSafe      string `json:"destStealthSafe,omitempty"`      // mode 2: FE-generated
	RegisterStealthSig   string `json:"registerStealthSig,omitempty"`   // mode 1: hex-encoded EIP-712 sig
	UnixTimestamp        int64  `json:"unixTimestamp"`
}

// StatusResponse is the GET /bridge/:id body. Mirrors BridgeIntent without
// the cleartext payload (so it's safe to expose with weaker auth).
type StatusResponse struct {
	Success         bool      `json:"success"`
	SessionID       string    `json:"sessionId"`
	Status          Status    `json:"status"`
	SourceChainID   string    `json:"sourceChainId,omitempty"`
	DestChainID     string    `json:"destChainId,omitempty"`
	Created         time.Time `json:"created,omitempty"`
	Updated         time.Time `json:"updated,omitempty"`
	SweepTxHash     string    `json:"sweepTxHash,omitempty"`
	BridgeTxHash    string    `json:"bridgeTxHash,omitempty"`
	DeliveredTxHash string    `json:"deliveredTxHash,omitempty"`
	RefundTxHash    string    `json:"refundTxHash,omitempty"`
	Refundable      bool      `json:"refundable"` // true iff status ∈ {swept, failed} and not yet refunded
	Error           string    `json:"error,omitempty"`
}
