package blockchain

import (
	"context"
	"math/big"
)

// ChainAddress is the canonical address representation that crosses chain
// runtimes. EVM addresses use 20 bytes (left-padded zeros to 32 are a common
// EVM-side convention but we keep them at their natural length here); Solana
// pubkeys use 32 bytes.
type ChainAddress struct {
	// "evm" or "solana"; matches types.ChainType.
	Kind string
	// Raw bytes — 20 for EVM, 32 for Solana.
	Bytes []byte
}

// IntentSnapshot is the chain-agnostic view of a BridgeIntent record. EVM
// solver code constructs this from the on-chain `getIntent` tuple; Solana
// solver code constructs it from the Anchor `IntentRecord` PDA.
type IntentSnapshot struct {
	IntentID        *big.Int
	User            ChainAddress
	TokenA          ChainAddress
	TokenB          ChainAddress
	AmountA         *big.Int
	ExpectedAmountB *big.Int
	Reward          *big.Int
	DestChainID     *big.Int
	Recipient       ChainAddress
	EndTime         int64
	WinningSolver   ChainAddress
	WinningBid      *big.Int
	Completed       bool
	Deposited       bool
}

// IntentEvent is what the solver's listener loop consumes. The EVM
// implementation parses log events; the Solana implementation parses Anchor
// program logs from `logsSubscribe`.
type IntentEvent struct {
	Kind      IntentEventKind
	Snapshot  IntentSnapshot
	TxHash    string
	Timestamp int64
}

type IntentEventKind int

const (
	EventIntentCreated IntentEventKind = iota
	EventBidPlaced
	EventIntentWon
	EventSolverDeposited
	EventIntentSettled
	EventIntentSolvedOnChain2
	EventIntentCompleted
	EventIntentCancelled
)

// ChainClient is the runtime-agnostic interface the solver uses to interact
// with any configured chain. EVMClient (this package, see client.go) and
// SolanaClient (solana_client.go) both satisfy it.
//
// Methods that aren't applicable to a given runtime return ErrUnsupported.
type ChainClient interface {
	// ChainID returns the configured chain ID. For Solana this is the
	// LayerZero EID (30168 mainnet, 40168 devnet/testnet).
	ChainID() *big.Int

	// NetworkName returns the human-readable chain name from config.
	NetworkName() string

	// SubscribeIntentEvents streams BridgeIntent lifecycle events. The
	// returned cancel func tears down the subscription.
	SubscribeIntentEvents(ctx context.Context, handler func(IntentEvent)) (cancel func(), err error)

	// GetIntent fetches the current on-chain state of an intent by ID. For
	// Solana, `intentID` is the local sequence number stored in the
	// `IntentRecord` PDA at seed [b"intent", intent_id_le].
	GetIntent(ctx context.Context, intentID *big.Int) (*IntentSnapshot, error)

	// PlaceBid submits a bid on `intentID`. Returns the broadcast tx
	// hash/signature.
	PlaceBid(ctx context.Context, intentID, bidAmount *big.Int) (string, error)

	// DepositAndPickup is called by the winning solver to deposit
	// `winningBid` of token_a into the contract/program escrow.
	DepositAndPickup(ctx context.Context, intentID *big.Int) (string, error)

	// SolveIntentOnChain2 delivers token_b on the destination chain.
	SolveIntentOnChain2(
		ctx context.Context,
		srcChainID, srcIntentID *big.Int,
		user ChainAddress,
		tokenB ChainAddress,
		expectedAmountB *big.Int,
		recipients []ChainAddress,
		amounts []*big.Int,
	) (string, error)

	// IsIntentSolvedOnChain2 returns true if the (srcChainID, srcIntentID)
	// pair has already been solved on this chain.
	IsIntentSolvedOnChain2(ctx context.Context, srcChainID, srcIntentID *big.Int) (bool, error)

	// BalanceOf returns the on-chain balance of `token` held by `account`.
	BalanceOf(ctx context.Context, account, token ChainAddress) (*big.Int, error)

	// Close releases held resources (RPC clients, websocket subs, etc.).
	Close() error
}

// ErrUnsupported is returned by ChainClient methods that don't apply to the
// underlying runtime (e.g., Safe-related EVM methods on a Solana client).
type ErrUnsupported struct {
	Op   string
	Kind string
}

func (e *ErrUnsupported) Error() string {
	return "blockchain: operation " + e.Op + " unsupported on chain kind " + e.Kind
}
