package privacy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LiFiClient is a thin HTTP wrapper around LI.FI's v1 API. We only use
// /v1/quote and /v1/status — the bridge service builds the
// transactionRequest LI.FI returns into a Safe execTx via the relayer.
type LiFiClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewLiFiClient returns a client pointed at li.quest/v1 with the
// integrator key set. Empty apiKey is allowed (public tier, rate-limited).
func NewLiFiClient(apiKey string) *LiFiClient {
	return &LiFiClient{
		baseURL: "https://li.quest/v1",
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// LiFiQuote is the subset of the /v1/quote response we read.
type LiFiQuote struct {
	ToolDetails struct {
		Name string `json:"name"`
	} `json:"toolDetails"`
	Action struct {
		FromAmount string `json:"fromAmount"`
		ToAmount   string `json:"toAmount"`
	} `json:"action"`
	Estimate struct {
		ApprovalAddress string `json:"approvalAddress"`
		ToAmount        string `json:"toAmount"`
		ToAmountMin     string `json:"toAmountMin"`
		ToAmountUSD     string `json:"toAmountUSD"`
		ExecutionDur    int    `json:"executionDuration"`
	} `json:"estimate"`
	TransactionRequest struct {
		To       string `json:"to"`
		Value    string `json:"value"`
		Data     string `json:"data"`
		GasLimit string `json:"gasLimit"`
		GasPrice string `json:"gasPrice"`
		ChainID  int    `json:"chainId"`
	} `json:"transactionRequest"`
}

// Quote calls /v1/quote. fromAddress is the wallet/Safe owning the source
// funds — LI.FI signs the transactionRequest with that as msg.sender, so
// it must match the address that will execute the tx (the TEE Safe in
// our case). toAddress is the recipient of the bridged funds on the
// destination chain.
func (c *LiFiClient) Quote(ctx context.Context, srcChainID, dstChainID string, srcToken, dstToken, fromAddress, toAddress string, amount *big.Int, slippage float64) (*LiFiQuote, error) {
	q := url.Values{}
	q.Set("fromChain", srcChainID)
	q.Set("toChain", dstChainID)
	q.Set("fromToken", srcToken)
	q.Set("toToken", dstToken)
	q.Set("fromAmount", amount.String())
	q.Set("fromAddress", fromAddress)
	q.Set("toAddress", toAddress)
	q.Set("slippage", fmt.Sprintf("%g", slippage))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/quote?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("x-lifi-api-key", c.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lifi /quote: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		// LI.FI returns {"message":"..."} on errors.
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("lifi /quote %d: %s", resp.StatusCode, e.Message)
	}

	var quote LiFiQuote
	if err := json.Unmarshal(body, &quote); err != nil {
		return nil, fmt.Errorf("lifi /quote decode: %w", err)
	}
	if quote.TransactionRequest.To == "" || quote.TransactionRequest.Data == "" {
		return nil, fmt.Errorf("lifi /quote returned empty transactionRequest")
	}
	return &quote, nil
}

// LiFiStatus is the subset of /v1/status response we read.
type LiFiStatus struct {
	Status      string `json:"status"`      // PENDING, DONE, FAILED, INVALID, NOT_FOUND
	Substatus   string `json:"substatus"`   // CHAIN_DELAY, BRIDGE_NOT_AVAILABLE, etc.
	Sending     LiFiTransfer `json:"sending"`
	Receiving   LiFiTransfer `json:"receiving"`
}

type LiFiTransfer struct {
	TxHash      string `json:"txHash"`
	ChainID     int    `json:"chainId"`
	Amount      string `json:"amount"`
	Token       struct {
		Address string `json:"address"`
		Symbol  string `json:"symbol"`
	} `json:"token"`
}

// Status polls /v1/status for cross-chain delivery confirmation. The
// txHash is the source-chain hash returned by submitting the quote's
// transactionRequest. Returns ("DONE", ...) once the destination tx
// has been seen.
func (c *LiFiClient) Status(ctx context.Context, srcTxHash string) (*LiFiStatus, error) {
	q := url.Values{}
	q.Set("txHash", srcTxHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/status?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("x-lifi-api-key", c.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lifi /status: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("lifi /status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var s LiFiStatus
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("lifi /status decode: %w", err)
	}
	return &s, nil
}
