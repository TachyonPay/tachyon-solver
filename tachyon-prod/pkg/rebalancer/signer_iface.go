package rebalancer

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Signer is the abstraction the rebalancer uses to sign and submit
// transactions. Two implementations exist:
//
//   - EOASigner    — single private key. Used for early testing on a
//                    small-funds wallet. The EOA pays gas AND owns the funds.
//   - SafeSigner   — Gnosis Safe v1.3.0 multisig. Production path. The Safe
//                    holds the funds; a separate EOA (already configured for
//                    the solver) pays gas via execTransaction.
//
// All higher-level operations (ERC20 approve, OFT/Stargate send, Aerodrome
// swap, on-chain quoter calls) are implemented as free functions taking a
// Signer, so the same code paths work for both.
type Signer interface {
	// Address returns a "primary" on-chain address. For EOA, the EOA itself.
	// For Safe, an arbitrary one of the per-chain Safe addresses (used only
	// where the chain context is unknown — most callers should prefer
	// AddressOnChain).
	Address() common.Address

	// AddressOnChain returns the address that owns funds and appears as
	// msg.sender on the given chain. For EOA-mode signers this is the same
	// for every chain. For Safe-mode signers it can differ across chains
	// (different proxy factories produce different proxy addresses).
	//
	// All chain-scoped operations (balance reads, allowance checks, recipient
	// and refund addresses for bridges, swap recipients) must use this rather
	// than Address() to behave correctly when Safes differ per chain.
	AddressOnChain(chainID string) common.Address

	// SendTx builds, signs, and submits a transaction targeting `to` with
	// `value` native gas and `data` calldata on the chain identified by
	// chainID. Returns the submitted *types.Transaction (not yet mined).
	//
	// gasLimit=0 leaves it to the underlying signer to estimate.
	SendTx(ctx context.Context, chainID string, to common.Address, value *big.Int, data []byte, gasLimit uint64) (*types.Transaction, error)

	// SendAndWait is SendTx + bind.WaitMined with a status check. Returns
	// (tx, receipt, nil) on success, or non-nil error on revert/timeout.
	SendAndWait(ctx context.Context, chainID string, to common.Address, value *big.Int, data []byte, gasLimit uint64, timeout time.Duration) (*types.Transaction, *types.Receipt, error)

	// CallReadOnly executes `eth_call` with msg.sender = Address(). Used by
	// quoters and balance/allowance probes.
	CallReadOnly(ctx context.Context, chainID string, to common.Address, data []byte) ([]byte, error)
}
