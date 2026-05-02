package privacy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// PublishDelivered records a delivered bridge intent against the relayer's
// /admin/transactions/complete endpoint so the user's main wallet-auth'd
// /transactions history surfaces both tachyon and privacy bridges in a
// single call. Failures are logged and swallowed — privacy router still
// holds the full intent locally and /bridge/:id remains the source of
// truth for the in-flight lifecycle.
//
// IntentID is the privacy session ID (32-byte hex), which won't collide
// with tachyon's integer intent IDs in the relayer's per-chain key
// space. Tx hashes map: deposit (user → TEE Safe sweep) → solve (TEE
// Safe → bridge call) → settle (delivery on dest).
func PublishDelivered(cfg *Config, intent *BridgeIntent) {
	if cfg.RelayerURL == "" || cfg.AdminAPIKey == "" {
		return
	}

	req := types.CompleteTransactionRequest{
		SourceChainID: intent.SourceChainID,
		IntentID:      intent.SessionID,
		UserAddress:   intent.UserAddress,
		DestChainID:   intent.DestChainID,
		TokenA:        string(intent.SourceToken),
		TokenB:        string(intent.DestToken),
		AmountA:       intent.Amount.String(),
		DepositTxHash: intent.SweepTxHash,
		SolveTxHash:   intent.BridgeTxHash,
		SettleTxHash:  intent.DeliveredTxHash,
	}

	body, err := json.Marshal(req)
	if err != nil {
		log.WithError(err).WithField("sessionID", intent.SessionID).Warn("PublishDelivered marshal failed")
		return
	}

	url := cfg.RelayerURL + "/admin/transactions/complete"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.WithError(err).WithField("sessionID", intent.SessionID).Warn("PublishDelivered build request failed")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", cfg.AdminAPIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.WithError(err).WithField("sessionID", intent.SessionID).Warn("PublishDelivered POST failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{
			"sessionID": intent.SessionID,
			"status":    resp.StatusCode,
			"body":      truncate(string(respBody), 200),
		}).Warn("PublishDelivered relayer rejected")
		return
	}

	log.WithFields(log.Fields{
		"sessionID":   intent.SessionID,
		"userAddress": intent.UserAddress,
	}).Info("Privacy bridge published to relayer history")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
