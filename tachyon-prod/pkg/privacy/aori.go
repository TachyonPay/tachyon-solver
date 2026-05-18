package privacy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// AoriClient is a thin HTTP wrapper around the Aori v1 API
// (https://api.aori.io). The privacy router uses Aori to fulfill
// USDC@{Base,Eth,Arb,Op} → USDM@MegaETH corridors: Aori's solver
// network has pre-positioned USDM inventory on MegaETH, so it
// delivers USDM atomically to a stealth Safe — no destination-side
// signature, no swap leg, just a transfer.
//
// Aori's settlement contract verifies offerer signatures via ecrecover
// only (no EIP-1271 / Safe smart-contract signers). So the offerer
// must be an EOA — we use the static relayer EOA. This degrades the
// bridge's anonymity for these 4 corridors specifically: an observer
// can identify the relayer EOA as a known Aori offerer and correlate
// timing/volume across bridges. The stealth Safe recipient is still
// preserved (Aori delivers there atomically), so the link from a
// specific user deposit to a specific stealth Safe receipt is not
// directly recoverable — but timing/volume correlation through the
// EOA is.
type AoriClient struct {
	baseURL string
	http    *http.Client
}

// NewAoriClient returns a client pointed at api.aori.io.
func NewAoriClient() *AoriClient {
	return &AoriClient{
		baseURL: "https://api.aori.io",
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// AoriQuoteRequest is the body of POST /quote.
type AoriQuoteRequest struct {
	Offerer     string `json:"offerer"`
	Recipient   string `json:"recipient"`
	InputToken  string `json:"inputToken"`
	OutputToken string `json:"outputToken"`
	InputAmount string `json:"inputAmount"`
	InputChain  string `json:"inputChain"`
	OutputChain string `json:"outputChain"`
}

// AoriQuote is the subset of the /quote response we use. Aori returns
// orderHash (the canonical identifier) and signingHash (the 32-byte
// digest the offerer signs over with secp256k1 — Aori's settlement
// contract recovers the signer via ecrecover and matches against
// `offerer`).
type AoriQuote struct {
	OrderHash     string `json:"orderHash"`
	SigningHash   string `json:"signingHash"`
	Offerer       string `json:"offerer"`
	Recipient     string `json:"recipient"`
	InputToken    string `json:"inputToken"`
	OutputToken   string `json:"outputToken"`
	InputAmount   string `json:"inputAmount"`
	OutputAmount  string `json:"outputAmount"`
	InputChain    string `json:"inputChain"`
	OutputChain   string `json:"outputChain"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	EstimatedTime string `json:"estimatedTime"`
}

// Quote calls POST /quote and returns the signable order template.
func (c *AoriClient) Quote(ctx context.Context, req *AoriQuoteRequest) (*AoriQuote, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/quote", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("aori /quote: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("aori /quote %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var q AoriQuote
	if err := json.Unmarshal(rb, &q); err != nil {
		return nil, fmt.Errorf("aori /quote decode: %w", err)
	}
	if q.OrderHash == "" || q.SigningHash == "" {
		return nil, fmt.Errorf("aori /quote returned empty orderHash/signingHash")
	}
	return &q, nil
}

// AoriSwapRequest is the body of POST /swap — submits the signed order.
type AoriSwapRequest struct {
	OrderHash string `json:"orderHash"`
	Signature string `json:"signature"`
}

// Swap submits a signed order to Aori's solver network. A 2xx response
// means the order was accepted into the orderbook; actual settlement
// happens asynchronously when a solver picks it up. Poll Status() (or
// the destination-chain balance) for completion.
func (c *AoriClient) Swap(ctx context.Context, orderHash string, sig []byte) error {
	body, _ := json.Marshal(AoriSwapRequest{
		OrderHash: orderHash,
		Signature: "0x" + hex.EncodeToString(sig),
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/swap", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("aori /swap: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("aori /swap %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return nil
}

// AoriStatus is the subset of /data/query we read for a single order.
// Aori's /data/query returns {orders: [order, ...], pagination: ...} —
// we extract the one matching our orderHash. The `status` field is a
// free-form string; we treat "completed" / "filled" / "settled" /
// "complete" / "delivered" as success and "expired" / "cancelled" /
// "failed" as terminal failure. Anything else is "still pending".
type AoriStatus struct {
	OrderHash string `json:"orderHash"`
	Status    string `json:"status"`
	SrcTx     string `json:"srcTx,omitempty"`
	DstTx     string `json:"dstTx,omitempty"`
}

// aoriQueryResponse is the wrapping shape /data/query returns.
type aoriQueryResponse struct {
	Orders []AoriStatus `json:"orders"`
}

// Status polls /data/query (the canonical lookup endpoint, despite the
// docs page being called "status"). Returns the matching order or an
// empty AoriStatus if the order isn't indexed yet.
func (c *AoriClient) Status(ctx context.Context, orderHash string) (*AoriStatus, error) {
	url := c.baseURL + "/data/query?orderHash=" + orderHash
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("aori /data/query: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("aori /data/query %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var wrapped aoriQueryResponse
	if err := json.Unmarshal(rb, &wrapped); err != nil {
		return nil, fmt.Errorf("aori /data/query decode: %w", err)
	}
	if len(wrapped.Orders) == 0 {
		// Order not indexed yet — return an empty status so callers
		// keep polling.
		return &AoriStatus{OrderHash: orderHash}, nil
	}
	return &wrapped.Orders[0], nil
}

// AoriChainKey maps a numeric chain ID to the string key Aori uses in
// /quote requests. Empty if the chain isn't supported.
func AoriChainKey(chainID string) string {
	switch chainID {
	case ChainIDOptimism:
		return "optimism"
	case ChainIDBase:
		return "base"
	case ChainIDArbitrum:
		return "arbitrum"
	case ChainIDEthereum:
		return "ethereum"
	case ChainIDMegaETH:
		return "megaeth"
	}
	return ""
}

// AoriContractAddress returns Aori's settlement contract on chainID.
// Verified live against /chains on 2026-05-18. Three of the chains
// share a CREATE2 address (Op/Base/Arb), Eth has a different deploy,
// MegaETH another. Returns zero address for unsupported chains.
func AoriContractAddress(chainID string) common.Address {
	switch chainID {
	case ChainIDOptimism, ChainIDBase, ChainIDArbitrum:
		return common.HexToAddress("0xc6868edf1d2a7a8b759856cb8afa333210dfeda6")
	case ChainIDEthereum:
		return common.HexToAddress("0x0736bdc975af0675b9a045384efed91360d25479")
	case ChainIDMegaETH:
		return common.HexToAddress("0xffe691a6ddb5d2645321e0a920c2e7bdd00dd3d8")
	}
	return common.Address{}
}

// signAoriHash signs Aori's signingHash with the offerer's private key.
// Aori's settlement contract verifies via ecrecover over the signingHash
// directly (no EIP-191 personal-sign prefix wrapping), so we produce a
// raw 65-byte secp256k1 signature with the Ethereum v adjustment
// (v ∈ {27, 28}).
func signAoriHash(privKey *ecdsa.PrivateKey, signingHashHex string) ([]byte, error) {
	hashHex := strings.TrimPrefix(signingHashHex, "0x")
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil {
		return nil, fmt.Errorf("decode signingHash: %w", err)
	}
	if len(hashBytes) != 32 {
		return nil, fmt.Errorf("signingHash must be 32 bytes, got %d", len(hashBytes))
	}
	sig, err := crypto.Sign(hashBytes, privKey)
	if err != nil {
		return nil, fmt.Errorf("sign aori hash: %w", err)
	}
	// crypto.Sign returns v as 0 or 1; Ethereum convention is 27 or 28.
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}
