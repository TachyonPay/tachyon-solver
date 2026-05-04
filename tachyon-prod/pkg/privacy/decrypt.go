package privacy

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/common"
)

// maxClockSkew bounds how far the client's claimed timestamp can be from
// the service's clock — a defense against very old replays. The value is
// generous because users may sit on an encrypted blob before submitting.
const maxClockSkew = 24 * time.Hour

// DecryptAndValidate parses the hex-encoded ECIES blob, decrypts it with
// the service's private key, JSON-unmarshals into a BridgeIntentPayload,
// and validates every field against the addresses.go route table. The
// returned BridgeIntent is fully populated and ready for st.Save.
//
// registerStealthSig is the optional EIP-712 signature for relayer-
// managed stealth sourcing (mode 1) — empty in mode 2. The caller
// (Submit handler) uses it to bridge into the relayer's
// /recipient/register flow before persisting the intent. It's
// returned separately because the BridgeIntent persists long-term
// and the sig is one-shot.
//
// On any structural / validation failure returns a non-nil error and
// nothing is persisted.
func DecryptAndValidate(priv *ecies.PrivateKey, encryptedHex string) (intent *BridgeIntent, registerStealthSig string, err error) {
	if priv == nil {
		return nil, "", fmt.Errorf("decrypt: nil private key")
	}
	encryptedHex = strings.TrimPrefix(encryptedHex, "0x")
	blob, err := hex.DecodeString(encryptedHex)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt: hex decode: %w", err)
	}

	clear, err := ecies.Decrypt(priv, blob)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt: ECIES: %w", err)
	}

	var p BridgeIntentPayload
	if err := json.Unmarshal(clear, &p); err != nil {
		return nil, "", fmt.Errorf("decrypt: payload JSON: %w", err)
	}

	if err := validatePayload(&p); err != nil {
		return nil, "", err
	}

	amt, ok := new(big.Int).SetString(p.AmountStr, 10)
	if !ok || amt.Sign() <= 0 {
		return nil, "", fmt.Errorf("amount %q is not a positive integer", p.AmountStr)
	}

	now := time.Now().UTC()
	intent = &BridgeIntent{
		SessionID:       p.SessionID,
		UserAddress:     p.UserAddress,
		SourceChainID:   p.SourceChainID,
		SourceToken:     NormalizeToken(string(p.SourceToken)),
		InboundSafe:     p.InboundSafe,
		Amount:          amt,
		DestChainID:     p.DestChainID,
		DestToken:       NormalizeToken(string(p.DestToken)),
		DestStealthSafe: p.DestStealthSafe,
		Status:          StatusPending,
		Created:         now,
		Updated:         now,
	}
	return intent, p.RegisterStealthSig, nil
}

// validatePayload runs structural checks on the decrypted payload before
// it becomes a BridgeIntent. Validation is fail-closed — anything we
// can't verify rejects the request.
func validatePayload(p *BridgeIntentPayload) error {
	if p.SessionID == "" {
		return fmt.Errorf("sessionId required")
	}
	if !common.IsHexAddress(p.UserAddress) {
		return fmt.Errorf("userAddress %q invalid", p.UserAddress)
	}
	if !common.IsHexAddress(p.InboundSafe) {
		return fmt.Errorf("inboundSafe %q invalid", p.InboundSafe)
	}
	// destStealthSafe is required in mode 2 (FE-generated). In mode 1
	// (relayer-managed) the privacy router fills it in via the relayer
	// after decryption — payload-side it's optional. Validity check
	// runs only when the field is non-empty.
	if p.DestStealthSafe != "" && !common.IsHexAddress(p.DestStealthSafe) {
		return fmt.Errorf("destStealthSafe %q invalid", p.DestStealthSafe)
	}
	if p.DestStealthSafe == "" && p.RegisterStealthSig == "" {
		return fmt.Errorf("either destStealthSafe (mode 2) or registerStealthSig (mode 1) is required")
	}
	srcTok := NormalizeToken(string(p.SourceToken))
	dstTok := NormalizeToken(string(p.DestToken))
	if !IsSupportedRoute(p.SourceChainID, p.DestChainID, srcTok, dstTok) {
		return fmt.Errorf("route %s:%s → %s:%s not supported",
			p.SourceChainID, srcTok, p.DestChainID, dstTok)
	}
	if p.UnixTimestamp == 0 {
		return fmt.Errorf("unixTimestamp required")
	}
	delta := time.Since(time.Unix(p.UnixTimestamp, 0))
	if delta < -maxClockSkew || delta > maxClockSkew {
		return fmt.Errorf("unixTimestamp %d skewed beyond %s", p.UnixTimestamp, maxClockSkew)
	}
	return nil
}
