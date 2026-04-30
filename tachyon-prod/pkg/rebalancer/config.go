package rebalancer

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// Config holds the operator-set targets and thresholds for the rebalancer.
//
// Targets are absolute amounts in raw token units (e.g. 100 USDC = 100_000_000
// since USDC has 6 decimals). When a balance falls below ThresholdPct * target
// the rebalancer plans a top-up. While picking a source it never drains a
// surplus below (1 - BufferPct) * target on the source chain.
type Config struct {
	Enabled        bool
	ThresholdPct   float64           // e.g. 0.30 — rebalance triggers when balance < 30% of target
	BufferPct      float64           // e.g. 0.10 — never drain a source below 90% of its target
	SlippagePct    float64           // e.g. 0.01 — 1% max slippage on swaps
	Targets        map[Key]*big.Int  // (chainID, token) → target raw amount
	StargateGasETH *big.Int          // wei buffer to overpay Stargate native fee (e.g. 5_000_000_000_000)
	OFTGasETH      *big.Int          // wei buffer to overpay OFT native fee
}

// Key identifies a (chain, token) bucket.
type Key struct {
	ChainID string
	Token   TokenSym
}

func (k Key) String() string { return string(k.Token) + "@" + k.ChainID }

// LoadConfig reads rebalancer configuration from env vars.
//
// Required when REBALANCER_ENABLED=true:
//   TARGET_<TOKEN>_<CHAIN> = raw_units  (e.g. TARGET_USDC_BASE=1000000000)
//
// Optional:
//   REBALANCER_THRESHOLD_PCT = 0.30
//   REBALANCER_BUFFER_PCT    = 0.10
//   REBALANCER_SLIPPAGE_PCT  = 0.01
//   REBALANCER_STARGATE_GAS_BUFFER_WEI = 5000000000000
//   REBALANCER_OFT_GAS_BUFFER_WEI      = 5000000000000
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Enabled:        getBool("REBALANCER_ENABLED", false),
		ThresholdPct:   getFloat("REBALANCER_THRESHOLD_PCT", 0.30),
		BufferPct:      getFloat("REBALANCER_BUFFER_PCT", 0.10),
		SlippagePct:    getFloat("REBALANCER_SLIPPAGE_PCT", 0.01),
		Targets:        map[Key]*big.Int{},
		StargateGasETH: getBigWei("REBALANCER_STARGATE_GAS_BUFFER_WEI", "5000000000000"),
		OFTGasETH:      getBigWei("REBALANCER_OFT_GAS_BUFFER_WEI", "5000000000000"),
	}

	if !cfg.Enabled {
		return cfg, nil
	}

	// Discover target envs of the form TARGET_<TOKEN>_<CHAIN>=raw_units.
	supportedTokens := []TokenSym{TokenUSDC, TokenWETH, TokenZEN}
	supportedChains := map[string]string{ // env suffix → chainID
		"BASE":    ChainIDBase,
		"HORIZEN": ChainIDHorizen,
	}

	for _, tok := range supportedTokens {
		for chainSuffix, chainID := range supportedChains {
			// Skip combos with no on-chain support.
			if TokenAddress(chainID, tok) == (common.Address{}) {
				continue
			}
			envName := fmt.Sprintf("TARGET_%s_%s", tok, chainSuffix)
			raw := os.Getenv(envName)
			if raw == "" {
				continue
			}
			amt, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
			if !ok {
				return nil, fmt.Errorf("%s=%q is not a valid integer", envName, raw)
			}
			if amt.Sign() < 0 {
				return nil, fmt.Errorf("%s=%s is negative", envName, raw)
			}
			cfg.Targets[Key{ChainID: chainID, Token: tok}] = amt
		}
	}

	if len(cfg.Targets) == 0 {
		return nil, fmt.Errorf("REBALANCER_ENABLED=true but no TARGET_*_* envs were set")
	}

	if cfg.ThresholdPct <= 0 || cfg.ThresholdPct >= 1 {
		return nil, fmt.Errorf("REBALANCER_THRESHOLD_PCT must be in (0, 1), got %f", cfg.ThresholdPct)
	}
	if cfg.BufferPct < 0 || cfg.BufferPct >= 1 {
		return nil, fmt.Errorf("REBALANCER_BUFFER_PCT must be in [0, 1), got %f", cfg.BufferPct)
	}
	if cfg.SlippagePct <= 0 || cfg.SlippagePct >= 0.5 {
		return nil, fmt.Errorf("REBALANCER_SLIPPAGE_PCT must be in (0, 0.5), got %f", cfg.SlippagePct)
	}

	return cfg, nil
}

// UseSafe returns true when REBALANCER_USE_SAFE is set to a truthy value.
// Caller is expected to also check that all configured chains have
// SAFE_ADDRESS + SAFE_SIGNER_KEY_1/2/3 wired (those are validated by the
// solver's main config loader).
func UseSafe() bool {
	return getBool("REBALANCER_USE_SAFE", false)
}

// helpers ---

func getBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getBigWei(key string, def string) *big.Int {
	v := os.Getenv(key)
	if v == "" {
		v = def
	}
	out, ok := new(big.Int).SetString(strings.TrimSpace(v), 10)
	if !ok {
		out, _ = new(big.Int).SetString(def, 10)
	}
	return out
}

