package privacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// RelayerStealth bundles the calls into the relayer's existing stealth
// recipient registry — the same pipeline the tachyon flow uses for
// generating destination Safes that the relayer's TEE can later
// derive the spend key for. Used by the privacy router's Submit
// handler in mode-1 (relayer-managed stealth) flow.
//
// Auth + ownership shape:
//   - /recipient/register requires an EIP-712 sig from the recipient
//     EOA. The privacy router relays the sig the FE included in the
//     decrypted BridgeIntentPayload.
//   - /recipient/generate-stealth is unauth (anyone can ask for a
//     stealth address to be generated for a recipient — authorization
//     is enforced later at derive-stealth-key / deploy-safe time).

type registerStealthReq struct {
	RecipientAddress string `json:"recipientAddress"`
	Signature        string `json:"signature"` // hex, no 0x prefix per existing convention
}

type registerStealthResp struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type generateStealthReq struct {
	RecipientAddress string `json:"recipientAddress"`
	ChainID          string `json:"chainId"`
	IntentID         string `json:"intentId"`
}

type generateStealthResp struct {
	Success        bool   `json:"success"`
	StealthAddress string `json:"stealthAddress"`
	SafeAddress    string `json:"safeAddress"`
	Error          string `json:"error,omitempty"`
}

// RegisterRecipient calls /recipient/register on the upstream relayer.
// Idempotent: returns nil if the recipient is already registered (the
// relayer's handler treats a re-register as a no-op).
func RegisterRecipient(cfg *Config, recipientAddr, sigHex string) error {
	if cfg.RelayerURL == "" {
		return fmt.Errorf("RELAYER_URL not configured")
	}
	body, _ := json.Marshal(registerStealthReq{
		RecipientAddress: recipientAddr,
		Signature:        strings.TrimPrefix(sigHex, "0x"),
	})
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Post(cfg.RelayerURL+"/recipient/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post /recipient/register: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var parsed registerStealthResp
	_ = json.Unmarshal(rb, &parsed)
	if !parsed.Success {
		// Tolerate "already registered" — the upstream may surface it as
		// an error string or as success=true depending on the build. Treat
		// any 200 OK as success even if parsed.Success is false but the
		// body mentions already-registered.
		if resp.StatusCode == http.StatusOK || strings.Contains(strings.ToLower(parsed.Error), "already") {
			log.WithField("recipient", recipientAddr).Debug("recipient already registered (treating as success)")
			return nil
		}
		return fmt.Errorf("/recipient/register %d: %s", resp.StatusCode, string(rb))
	}
	return nil
}

// GenerateStealth calls /recipient/generate-stealth on the upstream
// relayer for a fresh stealth address + predicted Safe scoped to
// (recipient, dest chain, sessionId-as-intentId). The returned Safe
// address is the bridge recipient the privacy router will deliver to.
func GenerateStealth(cfg *Config, recipientAddr, chainID, intentID string) (stealthAddr, safeAddr string, err error) {
	if cfg.RelayerURL == "" {
		return "", "", fmt.Errorf("RELAYER_URL not configured")
	}
	body, _ := json.Marshal(generateStealthReq{
		RecipientAddress: recipientAddr,
		ChainID:          chainID,
		IntentID:         intentID,
	})
	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Post(cfg.RelayerURL+"/recipient/generate-stealth", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("post /recipient/generate-stealth: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var parsed generateStealthResp
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return "", "", fmt.Errorf("/recipient/generate-stealth decode: %w (body=%s)", err, string(rb))
	}
	if !parsed.Success {
		return "", "", fmt.Errorf("/recipient/generate-stealth %d: %s", resp.StatusCode, parsed.Error)
	}
	if !isHexAddr(parsed.SafeAddress) {
		return "", "", fmt.Errorf("/recipient/generate-stealth returned invalid safeAddress %q", parsed.SafeAddress)
	}
	return parsed.StealthAddress, parsed.SafeAddress, nil
}

// isHexAddr is a tiny local check (avoids pulling go-ethereum into
// callers that don't already use it).
func isHexAddr(s string) bool {
	if len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return false
	}
	for _, r := range s[2:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
