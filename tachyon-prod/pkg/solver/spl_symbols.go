package solver

import (
	"strings"

	"github.com/gagliardetto/solana-go"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// splPubkeyBytesToBase58 renders a 32-byte Solana pubkey from on-chain
// bytes (typically `solanaIntentExt.tokenBMint`) into its canonical
// base58 string form. Returns the empty string for malformed input so
// callers can fail closed.
func splPubkeyBytesToBase58(b []byte) string {
	if len(b) != 32 {
		return ""
	}
	return solana.PublicKeyFromBytes(b).String()
}

// solanaPubkeyFromBase58 parses a base58 Solana pubkey string into its
// raw 32-byte form. Returns a non-nil error for invalid input so
// callers can fail closed.
func solanaPubkeyFromBase58(s string) ([]byte, error) {
	pk, err := solana.PublicKeyFromBase58(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	return pk.Bytes(), nil
}

// splMintToSymbol resolves a Solana SPL mint pubkey (base58) to its
// canonical price-oracle symbol using the chain's configured mints. The
// solver's profitability path needs symbols, not mints, because the
// inline price oracle keys on tickers like "USDC", "SOL", "WSOL".
//
// Lookup is case-insensitive on the inputs; we trim whitespace and match
// the chain's `SolanaUSDCMint` / `SolanaWSOLMint` config fields plus a
// hard-coded canonical WSOL fallback (`So11...112`) for safety on devnet
// configs that omit it. Unknown mints return ("", false) — callers must
// fail closed (skip the intent) rather than guessing.
func splMintToSymbol(chain types.NetworkConfig, mint string) (string, bool) {
	m := strings.TrimSpace(mint)
	if m == "" {
		return "", false
	}
	if strings.EqualFold(m, chain.SolanaUSDCMint) {
		return "USDC", true
	}
	if strings.EqualFold(m, chain.SolanaWSOLMint) {
		return "WSOL", true
	}
	// Canonical wrapped-SOL mint, baked in as a fallback for chain configs
	// that didn't set CHAIN_*_SOLANA_WSOL_MINT.
	if m == "So11111111111111111111111111111111111111112" {
		return "WSOL", true
	}
	return "", false
}

// splMintDecimals returns the on-chain decimals for known SPL mints. The
// program-bundled mints (mock USDC, mock secondary, real USDC, real
// USDT) all use 6 decimals; SOL/WSOL is 9. Unknown mints return (0,
// false); the caller should resolve via RPC `getMint` and cache.
func splMintDecimals(chain types.NetworkConfig, mint string) (uint8, bool) {
	m := strings.TrimSpace(mint)
	if m == "" {
		return 0, false
	}
	switch {
	case strings.EqualFold(m, chain.SolanaUSDCMint):
		return 6, true
	case strings.EqualFold(m, chain.SolanaWSOLMint),
		m == "So11111111111111111111111111111111111111112":
		return 9, true
	}
	return 0, false
}
