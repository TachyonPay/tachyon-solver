package rebalancer

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

// SafeSigner is the production Signer that issues every transaction through
// the solver's Gnosis Safe. The funds (and the Safe) live on-chain; the actual
// gas-paying EOA submission is delegated to the relayer service.
//
// Per-transaction flow:
//
//  1. SafeSigner asks `blockchain.Client.SignSafeTransaction` to compute the
//     SafeTxHash and produce the concatenated owner signature(s).
//  2. SafeSigner POSTs (safeAddr, chainId, to, value, data, op, sig) to the
//     relayer's /recipient/relay-proxy endpoint.
//  3. The relayer submits execTransaction from its own EOA, forwarding the
//     inner value as msg.value (so the Safe doesn't need to hold ETH/ZEN
//     long-term — it only needs the rebalanced tokens).
//  4. SafeSigner fetches the resulting tx from chain by hash and returns it.
//
// Each chain's blockchain.Client must already have:
//   - SAFE_ADDRESS set
//   - SAFE_SIGNER_KEY_1/2/3 loaded
//
// SafeSigner serializes per-chain submissions behind a mutex to avoid Safe
// nonce races within the rebalancer. The relayer's own queue may interleave
// other Safe ops on the same Safe — operators should not run multiple
// rebalancer instances against the same Safe concurrently.
//
// Each per-chain mutex also gates an in-memory nonce cache. Reading the Safe
// nonce from chain after a tx mines is not safe in tight loops: Alchemy and
// similar providers can briefly serve the pre-tx nonce after the tx has
// already been included, which produces a signature against the wrong nonce
// and reverts the next execTransaction in checkSignatures. The cache is
// populated lazily from chain on first use, advanced after every successful
// relayer-accepted submission, and invalidated (forcing a chain re-read) on
// any failure path.
type SafeSigner struct {
	clients    map[string]*blockchain.Client
	address    common.Address
	mu         map[string]*sync.Mutex
	nonceCache map[string]*big.Int // chainID → next nonce to use; nil = read from chain
	relayerURL string
	httpClient *http.Client
}

// relayProxyRequest mirrors types.RelayProxyRequest to avoid an import cycle.
type relayProxyRequest struct {
	SafeAddress string `json:"safeAddress"`
	ChainID     string `json:"chainId"`
	To          string `json:"to"`
	Value       string `json:"value,omitempty"`
	Data        string `json:"data,omitempty"`
	Operation   uint8  `json:"operation"`
	Signature   string `json:"signature"`
}

type relayProxyResponse struct {
	Success bool   `json:"success"`
	TxHash  string `json:"txHash"`
	Error   string `json:"error"`
}

// NewSafeSignerFromClients builds a SafeSigner using the given chain clients
// and the relayer URL from config (REBALANCER_RELAYER_URL, falling back to
// RELAYER_URL, then http://localhost:3000).
//
// Chains without a SAFE_ADDRESS configured are silently skipped — the
// rebalancer only needs Safes on the mainnet chains it actually operates on
// (Base + Horizen mainnet). Testnet chains in the same .env can stay
// EOA-only.
//
// Returns (nil, nil) if `clients` is empty or no chain has a Safe.
func NewSafeSignerFromClients(clients map[string]*blockchain.Client) (*SafeSigner, error) {
	if len(clients) == 0 {
		return nil, nil
	}
	safeClients := make(map[string]*blockchain.Client, len(clients))
	mus := make(map[string]*sync.Mutex, len(clients))
	safe := common.Address{}
	skipped := 0
	for chainID, c := range clients {
		if !c.HasSafe() {
			skipped++
			continue
		}
		safeClients[chainID] = c
		mus[chainID] = &sync.Mutex{}

		addr := c.GetSafeAddress()
		if safe == (common.Address{}) {
			safe = addr
		} else if safe != addr {
			log.WithFields(log.Fields{
				"chain1Safe": safe.Hex(),
				"chain2Safe": addr.Hex(),
			}).Warn("Safe addresses differ across chains — this is unusual")
		}
	}
	if len(safeClients) == 0 {
		return nil, nil
	}
	if skipped > 0 {
		log.WithField("skipped", skipped).Info("Some chains skipped from Safe wiring (no SAFE_ADDRESS configured)")
	}
	clients = safeClients

	relayerURL := RelayerURL()

	log.WithFields(log.Fields{
		"address":    safe.Hex(),
		"chains":     len(clients),
		"relayerURL": relayerURL,
	}).Info("Rebalancer Safe signer initialized (relayer-mediated)")

	return &SafeSigner{
		clients:    clients,
		address:    safe,
		mu:         mus,
		nonceCache: make(map[string]*big.Int, len(clients)),
		relayerURL: relayerURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// nextNonce returns the nonce to sign with for the next Safe tx on chainID.
// Caller must hold s.mu[chainID]. If the cache is empty (first use, or the
// previous tx errored and invalidated the cache) it reads from chain.
func (s *SafeSigner) nextNonce(ctx context.Context, chainID string, c *blockchain.Client) (*big.Int, error) {
	if n := s.nonceCache[chainID]; n != nil {
		return new(big.Int).Set(n), nil
	}
	n, err := c.ReadSafeNonce(ctx)
	if err != nil {
		return nil, fmt.Errorf("read safe nonce: %w", err)
	}
	return n, nil
}

// commitNonce advances the cached nonce for chainID by 1. Called after the
// relayer accepts a tx — at that point we've committed to using the nonce we
// signed with, so the next signature must use nonce+1.
// Caller must hold s.mu[chainID].
func (s *SafeSigner) commitNonce(chainID string, used *big.Int) {
	s.nonceCache[chainID] = new(big.Int).Add(used, big.NewInt(1))
}

// invalidateNonce clears the cached nonce for chainID, forcing the next
// nextNonce call to re-read from chain. Called on any submission/mining
// failure so an external Safe tx (or a stale read) doesn't compound across
// retries.
// Caller must hold s.mu[chainID].
func (s *SafeSigner) invalidateNonce(chainID string) {
	delete(s.nonceCache, chainID)
}

// Address returns one of the per-chain Safe addresses. Most callers should
// prefer AddressOnChain since Safe addresses can differ between chains
// (different proxy factories on Base vs Horizen).
func (s *SafeSigner) Address() common.Address { return s.address }

// AddressOnChain returns the Safe address on the given chain. Different chains
// may have Safes at different addresses when the underlying SafeProxyFactory
// differs (e.g. Base uses canonical Safe, Horizen uses a custom deployment),
// so balance reads, allowance checks, bridge recipients/refunds must use this
// rather than Address().
func (s *SafeSigner) AddressOnChain(chainID string) common.Address {
	c, ok := s.clients[chainID]
	if !ok {
		// Fall back to the primary address; the caller will error elsewhere
		// when the chain isn't actually configured.
		return s.address
	}
	return c.GetSafeAddress()
}

// SendTx signs the inner call (to, value, data) as a Safe execTransaction,
// POSTs the signed payload to the relayer, and returns the resulting tx after
// fetching it from chain by hash.
//
// gasLimit is currently ignored — the relayer manages its own outer gas. Kept
// in the signature for Signer-interface conformance.
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

	if value == nil {
		value = big.NewInt(0)
	}

	// Sign with the cached nonce (or chain-read on first use). On any error
	// past this point we invalidate the cache so a subsequent retry starts
	// from a fresh chain read instead of compounding a stale value.
	nonce, err := s.nextNonce(ctx, chainID, c)
	if err != nil {
		return nil, err
	}
	sig, err := c.SignSafeTransactionWithNonce(ctx, to, value, data, 0, nonce)
	if err != nil {
		s.invalidateNonce(chainID)
		return nil, fmt.Errorf("sign safe tx: %w", err)
	}

	// POST to the relayer's RelayProxy endpoint. Use the per-chain Safe
	// address (Safes can differ between chains).
	body, _ := json.Marshal(relayProxyRequest{
		SafeAddress: c.GetSafeAddress().Hex(),
		ChainID:     chainID,
		To:          to.Hex(),
		Value:       value.String(),
		Data:        "0x" + hex.EncodeToString(data),
		Operation:   0, // Call
		Signature:   "0x" + hex.EncodeToString(sig),
	})

	url := s.relayerURL + "/recipient/relay-proxy"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.invalidateNonce(chainID)
		return nil, fmt.Errorf("build relay-proxy request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		s.invalidateNonce(chainID)
		return nil, fmt.Errorf("relay-proxy request: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)

	var parsed relayProxyResponse
	if err := json.Unmarshal(rb, &parsed); err != nil {
		s.invalidateNonce(chainID)
		return nil, fmt.Errorf("parse relay-proxy response (%d): %s", resp.StatusCode, string(rb))
	}
	if !parsed.Success || parsed.TxHash == "" {
		s.invalidateNonce(chainID)
		return nil, fmt.Errorf("relay-proxy refused (%d): %s", resp.StatusCode, parsed.Error)
	}

	// Relayer accepted the tx — commit the nonce we used so the next signing
	// call advances to nonce+1 without a chain re-read.
	s.commitNonce(chainID, nonce)

	log.WithFields(log.Fields{
		"chain":  chainID,
		"nonce":  nonce.String(),
		"to":     to.Hex(),
		"value":  value.String(),
		"txHash": parsed.TxHash,
	}).Info("Safe tx submitted via relayer")

	// Fetch the tx from chain by hash so we have a real *types.Transaction
	// for downstream WaitMined. Retry briefly to absorb propagation lag.
	hash := common.HexToHash(parsed.TxHash)
	for attempt := 0; attempt < 6; attempt++ {
		tx, _, err := c.EthClient().TransactionByHash(ctx, hash)
		if err == nil && tx != nil {
			return tx, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("could not fetch tx %s by hash after relayer accepted it", parsed.TxHash)
}

// SendAndWait sends and blocks on bind.WaitMined.
//
// On any non-success outcome (mining error or status != 1) the per-chain
// nonce cache is invalidated. With safeTxGas=0 / gasPrice=0 (our default),
// a Safe execTransaction whose inner call fails reverts the OUTER tx —
// which rolls back the nonce++ inside the Safe. So a chain-reverted Safe
// tx does NOT consume the nonce, and the cache (which we optimistically
// advanced when the relayer accepted) is now off-by-one.
func (s *SafeSigner) SendAndWait(ctx context.Context, chainID string, to common.Address, value *big.Int, data []byte, gasLimit uint64, timeout time.Duration) (*types.Transaction, *types.Receipt, error) {
	tx, err := s.SendTx(ctx, chainID, to, value, data, gasLimit)
	if err != nil {
		return nil, nil, err
	}
	c, ok := s.clients[chainID]
	if !ok {
		s.invalidateNonceLocked(chainID)
		return tx, nil, fmt.Errorf("no Safe client for chain %s", chainID)
	}
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rcpt, err := bind.WaitMined(waitCtx, c.EthClient(), tx)
	if err != nil {
		s.invalidateNonceLocked(chainID)
		return tx, nil, fmt.Errorf("wait mined %s: %w", tx.Hash().Hex(), err)
	}
	if rcpt.Status != 1 {
		s.invalidateNonceLocked(chainID)
		return tx, rcpt, fmt.Errorf("safe tx %s reverted (status %d)", tx.Hash().Hex(), rcpt.Status)
	}
	return tx, rcpt, nil
}

// invalidateNonceLocked acquires the per-chain mutex and clears the cache.
// Used by SendAndWait callers that don't already hold the mutex (SendTx
// releases it on return).
func (s *SafeSigner) invalidateNonceLocked(chainID string) {
	mu, ok := s.mu[chainID]
	if !ok {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	s.invalidateNonce(chainID)
}

// CallReadOnly executes eth_call with msg.sender = Safe address ON THIS CHAIN.
// Read-only operations don't touch the Safe; we just call directly.
func (s *SafeSigner) CallReadOnly(ctx context.Context, chainID string, to common.Address, data []byte) ([]byte, error) {
	c, ok := s.clients[chainID]
	if !ok {
		return nil, fmt.Errorf("no Safe client for chain %s", chainID)
	}
	msg := callMsgFromTx(c.GetSafeAddress(), to, big.NewInt(0), data)
	return c.EthClient().CallContract(ctx, msg, nil)
}

// QuoteIn implements Quoter on SafeSigner — delegates to the shared free
// function since it's just on-chain reads.
func (s *SafeSigner) QuoteIn(ctx context.Context, chainID string, srcToken, dstToken TokenSym, amountOut *big.Int) (*big.Int, error) {
	return quoteIn(ctx, s, chainID, srcToken, dstToken, amountOut)
}
