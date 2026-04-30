package rebalancer

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

// SafeSigner is the production Signer that issues every transaction through
// the solver's Gnosis Safe. The funds and approvals live on the Safe; a
// separate gas-paying EOA (already configured for the solver via PRIVATE_KEY)
// submits the wrapping execTransaction call.
//
// Each chain's blockchain.Client must already have:
//   - SAFE_ADDRESS set (chain has a Safe wired)
//   - SAFE_SIGNER_KEY_1/2/3 loaded (3-of-N TEE signers)
//   - PRIVATE_KEY set (gas-paying EOA, separate from Safe owners)
//
// SafeSigner serializes per-chain submissions behind a mutex to avoid
// nonce races on the Safe (Safe nonce is monotonic).
type SafeSigner struct {
	clients map[string]*blockchain.Client
	address common.Address // the Safe address (same on all chains via CREATE2)
	mu      map[string]*sync.Mutex
}

// NewSafeSignerFromClients builds a SafeSigner from per-chain blockchain.Clients
// that have Safes wired. Returns an error if any client doesn't have a Safe
// configured. Returns (nil, nil) if `clients` is empty.
func NewSafeSignerFromClients(clients map[string]*blockchain.Client) (*SafeSigner, error) {
	if len(clients) == 0 {
		return nil, nil
	}
	safe := common.Address{}
	mus := make(map[string]*sync.Mutex, len(clients))
	for chainID, c := range clients {
		if !c.HasSafe() {
			return nil, fmt.Errorf("chain %s has no SAFE_ADDRESS configured", chainID)
		}
		addr := c.GetSafeAddress()
		if safe == (common.Address{}) {
			safe = addr
		} else if safe != addr {
			// Safe addresses differ across chains. This is allowed (different
			// Safes per chain), but flag it for visibility.
			log.WithFields(log.Fields{
				"chain1Safe": safe.Hex(),
				"chain2Safe": addr.Hex(),
			}).Warn("Safe addresses differ across chains — this is unusual")
		}
		mus[chainID] = &sync.Mutex{}
	}

	log.WithFields(log.Fields{
		"address": safe.Hex(),
		"chains":  len(clients),
	}).Info("Rebalancer Safe signer initialized")

	return &SafeSigner{clients: clients, address: safe, mu: mus}, nil
}

// Address returns the Safe address.
func (s *SafeSigner) Address() common.Address { return s.address }

// SendTx wraps the call as a Safe execTransaction signed by the configured
// TEE keys, submitted by the gas-paying EOA. value > 0 requires the Safe to
// hold native gas on this chain (ETH on Base, ZEN on Horizen).
//
// gasLimit is currently ignored (Safe's execTransaction has its own internal
// gas accounting; the outer EOA tx is sized by ApproveTokenViaSafe-style
// hardcoded limits). Kept in the signature for Signer-interface conformance.
func (s *SafeSigner) SendTx(ctx context.Context, chainID string, to common.Address, value *big.Int, data []byte, gasLimit uint64) (*types.Transaction, error) {
	_ = gasLimit
	c, ok := s.clients[chainID]
	if !ok {
		return nil, fmt.Errorf("no Safe client for chain %s", chainID)
	}
	mu, ok := s.mu[chainID]
	if !ok {
		return nil, fmt.Errorf("no nonce mutex for chain %s", chainID)
	}
	mu.Lock()
	defer mu.Unlock()

	if value == nil || value.Sign() == 0 {
		return c.ExecuteViaSafe(ctx, to, data)
	}
	return c.ExecuteViaSafeWithValue(ctx, to, value, data)
}

// SendAndWait sends and blocks on bind.WaitMined.
func (s *SafeSigner) SendAndWait(ctx context.Context, chainID string, to common.Address, value *big.Int, data []byte, gasLimit uint64, timeout time.Duration) (*types.Transaction, *types.Receipt, error) {
	tx, err := s.SendTx(ctx, chainID, to, value, data, gasLimit)
	if err != nil {
		return nil, nil, err
	}
	c, ok := s.clients[chainID]
	if !ok {
		return tx, nil, fmt.Errorf("no Safe client for chain %s", chainID)
	}
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rcpt, err := bind.WaitMined(waitCtx, c.EthClient(), tx)
	if err != nil {
		return tx, nil, fmt.Errorf("wait mined %s: %w", tx.Hash().Hex(), err)
	}
	if rcpt.Status != 1 {
		return tx, rcpt, fmt.Errorf("safe tx %s reverted (status %d)", tx.Hash().Hex(), rcpt.Status)
	}
	return tx, rcpt, nil
}

// CallReadOnly executes eth_call with msg.sender = Safe address.
// Read-only operations don't need Safe wrapping; we just call directly.
func (s *SafeSigner) CallReadOnly(ctx context.Context, chainID string, to common.Address, data []byte) ([]byte, error) {
	c, ok := s.clients[chainID]
	if !ok {
		return nil, fmt.Errorf("no Safe client for chain %s", chainID)
	}
	msg := callMsgFromTx(s.address, to, big.NewInt(0), data)
	return c.EthClient().CallContract(ctx, msg, nil)
}

// QuoteIn implements Quoter on SafeSigner — same logic as EOASigner since it's
// just on-chain reads.
func (s *SafeSigner) QuoteIn(ctx context.Context, chainID string, srcToken, dstToken TokenSym, amountOut *big.Int) (*big.Int, error) {
	return quoteIn(ctx, s, chainID, srcToken, dstToken, amountOut)
}
