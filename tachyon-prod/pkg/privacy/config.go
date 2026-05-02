package privacy

import (
	"fmt"
	"os"
	"strings"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/common"
)

// Config holds the runtime config for the privacy router service.
type Config struct {
	// HTTP port the service binds to.
	Port string

	// Pebble path for persistence (parent directory; the service appends
	// "/privacy" to land at <DBPath>/privacy/...).
	DBPath string

	// ECIES key used to decrypt encrypted bridge intents from clients.
	// Reused from the existing relayer's ECIES_PRIVATE_KEY env so clients
	// only need one service pubkey to encrypt against.
	ECIESPrivateKey *ecies.PrivateKey

	// RelayerURL is the upstream relayer that signs Safe execTransactions
	// on behalf of the TEE hot Safes. The privacy router POSTs to
	// {RelayerURL}/recipient/relay-proxy to get a Safe tx submitted.
	RelayerURL string

	// LiFi integrator key. Empty = unauthenticated (rate-limited public
	// tier). Set LIFI_API_KEY in .env for production traffic.
	LifiAPIKey string

	// AdminAPIKey is the relayer's ADMIN_API_KEY, used to publish
	// completed bridges to the relayer's /admin/transactions/complete
	// endpoint so they show up in the unified user history. Empty = no
	// publishing (privacy router still tracks history locally per
	// sessionID via /bridge/:id).
	AdminAPIKey string

	// Per-chain RPC URLs and TEE Safe addresses. Only chains with both
	// fields populated are considered "live"; missing entries cause the
	// router to skip routes touching that chain.
	Chains map[string]ChainConfig
}

// ChainConfig is the per-chain runtime info the privacy router needs.
type ChainConfig struct {
	ChainID string         // string form so it joins cleanly with addresses.go
	RPC     string         // public or private RPC endpoint
	TEESafe common.Address // TEE-controlled hot Safe on this chain
}

// LoadConfig pulls config out of the environment. Fails on missing
// required fields (port, ECIES key, relayer URL); chain entries are
// optional but at least two chains (one source, one dest) must be
// configured for the service to be useful.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:       getEnv("PRIVACY_PORT", "3003"),
		DBPath:     getEnv("DB_PATH", "./data"),
		RelayerURL:  strings.TrimRight(getEnv("RELAYER_URL", "http://relayer:3000"), "/"),
		LifiAPIKey:  getEnv("LIFI_API_KEY", ""),
		AdminAPIKey: getEnv("ADMIN_API_KEY", ""),
		Chains:      map[string]ChainConfig{},
	}

	// ECIES key. The relayer also reads ECIES_PRIVATE_KEY — reusing the
	// same key means clients encrypt against one pubkey for both services.
	keyHex := strings.TrimPrefix(getEnv("ECIES_PRIVATE_KEY", ""), "0x")
	if keyHex == "" {
		return nil, fmt.Errorf("ECIES_PRIVATE_KEY is required")
	}
	priv, err := ecies.NewPrivateKeyFromHex(keyHex)
	if err != nil {
		return nil, fmt.Errorf("ECIES_PRIVATE_KEY parse: %w", err)
	}
	cfg.ECIESPrivateKey = priv

	// Per-chain config. Each supported chain has its own env-var pair,
	// keyed by the chain's symbolic name. Unset chains are silently
	// skipped (the route table in addresses.go gates which chains are
	// reachable anyway).
	for _, c := range []struct {
		id      string
		rpcEnv  string
		safeEnv string
	}{
		{ChainIDMegaETH, "PRIVACY_RPC_MEGAETH", "TEE_SAFE_MEGAETH"},
		{ChainIDBase, "PRIVACY_RPC_BASE", "TEE_SAFE_BASE"},
		{ChainIDEthereum, "PRIVACY_RPC_ETHEREUM", "TEE_SAFE_ETHEREUM"},
		{ChainIDArbitrum, "PRIVACY_RPC_ARBITRUM", "TEE_SAFE_ARBITRUM"},
		{ChainIDOptimism, "PRIVACY_RPC_OPTIMISM", "TEE_SAFE_OPTIMISM"},
		{ChainIDHorizen, "PRIVACY_RPC_HORIZEN", "TEE_SAFE_HORIZEN"},
	} {
		rpc := os.Getenv(c.rpcEnv)
		safeStr := os.Getenv(c.safeEnv)
		if rpc == "" || safeStr == "" {
			continue
		}
		if !common.IsHexAddress(safeStr) {
			return nil, fmt.Errorf("%s=%q is not a valid address", c.safeEnv, safeStr)
		}
		cfg.Chains[c.id] = ChainConfig{
			ChainID: c.id,
			RPC:     rpc,
			TEESafe: common.HexToAddress(safeStr),
		}
	}

	// Note: zero chains is allowed (degraded mode) so the service can boot
	// before the operator has deployed TEE Safes. main.go skips processor
	// + bridger startup when len(Chains) == 0; HTTP still serves /health
	// and /pubkey for clients to encrypt against. Bridge submits return
	// the sessionID but no on-chain action happens until Safes appear.

	return cfg, nil
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ECIESPublicKeyHex returns the service's ECIES public key as an
// uncompressed 65-byte hex string with "0x" prefix. Clients use this to
// encrypt bridge intent payloads.
func (c *Config) ECIESPublicKeyHex() string {
	if c == nil || c.ECIESPrivateKey == nil {
		return ""
	}
	pub := c.ECIESPrivateKey.PublicKey
	return "0x" + common.Bytes2Hex(pub.Bytes(false))
}
