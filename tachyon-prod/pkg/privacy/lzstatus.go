package privacy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LayerZero v2 mainnet scan API. Poll by source-chain tx hash; works
// for any LZ-based bridge (Stargate v2 pools, ZEN OFT adapter, etc.)
// because indexing is at the LZ message level, not the protocol level.
//
// Sister project hyperstealth uses the same endpoint — see
// /Users/sudeepskamat/hyperstealth/pkg/bridge/lzstatus.go.
const lzScanAPI = "https://api-mainnet.layerzero-scan.com"

// LZMessageStatus is the subset of fields we read from /tx/<srcTxHash>.
type LZMessageStatus struct {
	SrcTxHash  string `json:"srcTxHash"`
	DstTxHash  string `json:"dstTxHash"`
	SrcChainID uint32 `json:"srcChainId"`
	DstChainID uint32 `json:"dstChainId"`
	Status     string `json:"status"` // INFLIGHT | DELIVERED | FAILED
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
}

type lzScanResponse struct {
	Messages []LZMessageStatus `json:"messages"`
}

// checkLZStatus queries the LZ scan API. Returns (nil, nil) when the
// message hasn't been indexed yet — caller should keep polling.
func checkLZStatus(ctx context.Context, srcTxHash string) (*LZMessageStatus, error) {
	url := fmt.Sprintf("%s/tx/%s", lzScanAPI, srcTxHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lz scan %d: %s", resp.StatusCode, string(body))
	}

	var out lzScanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Messages) == 0 {
		return nil, nil
	}
	return &out.Messages[0], nil
}

// waitLZDelivery polls LZ scan until the message status terminates
// (DELIVERED or FAILED) or the deadline elapses. Returns the final
// status on success or an error on timeout/failure. Returns the
// destination tx hash when DELIVERED so the caller can record it.
func waitLZDelivery(ctx context.Context, srcTxHash string, pollInterval, deadline time.Duration) (*LZMessageStatus, error) {
	cutoff := time.Now().Add(deadline)
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		if time.Now().After(cutoff) {
			return nil, fmt.Errorf("lz delivery timeout after %s (srcTx %s)", deadline, srcTxHash)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
		status, err := checkLZStatus(ctx, srcTxHash)
		if err != nil {
			// transient; keep polling
			continue
		}
		if status == nil {
			// not indexed yet
			continue
		}
		switch status.Status {
		case "DELIVERED":
			return status, nil
		case "FAILED":
			return status, fmt.Errorf("lz message failed: dstTx=%s", status.DstTxHash)
		}
		// INFLIGHT — continue
	}
}
