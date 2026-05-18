package luckydraw

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
)

// ChainEntry holds per-chain configuration for the lucky draw.
type ChainEntry struct {
	ChainID     *big.Int
	ChainName   string
	RPCURL      string
	SafeAddress string         // Gnosis Safe that holds USDC for dripping
	USDCAddress common.Address // USDC ERC-20 on this chain
}

// Config holds all lucky draw service configuration.
type Config struct {
	Port    string
	Chains  []ChainEntry // one entry per supported EVM chain
	MinUSDC int
	MaxUSDC int
	DBPath  string

	// WinChance is the probability (0.0–1.0) that an eligible entry wins a drip.
	// Default 0.12 — ~1-in-8 chance per stealth address. With 3 stealth addresses
	// per person, expected wins ≈ 0.36, and P(double-win) ≈ 4%.
	// Set LUCKY_DRAW_WIN_CHANCE=1.0 to force every entry to win (useful for tests).
	WinChance float64

	// PrivateKey is the relayer EOA that pays gas for execTransaction calls.
	PrivateKey string

	// SafeSignerKeys are the keys that co-sign each Safe payout.
	// Populate with SAFE_SIGNER_KEY_1/2/3 (TEE keys) + LUCKY_DRAW_ADMIN_KEY
	// so that all four together satisfy the Safe's 4-of-5 threshold.
	SafeSignerKeys []*ecdsa.PrivateKey

	AdminAPIKey string
}

// LoadConfig reads configuration from the environment.
//
// Chain configuration (repeat for each chain, N=1,2,...):
//
//	LUCKY_DRAW_CHAIN_COUNT     number of chains (default 2)
//	LUCKY_DRAW_CHAIN_N_ID      chain ID (decimal)
//	LUCKY_DRAW_CHAIN_N_NAME    human name (default "chain-N")
//	LUCKY_DRAW_WIN_CHANCE      win probability 0.0–1.0 (default 0.33)
//	LUCKY_DRAW_CHAIN_N_RPC     JSON-RPC URL
//	LUCKY_DRAW_CHAIN_N_SAFE    Gnosis Safe address (holds USDC)
//	LUCKY_DRAW_CHAIN_N_USDC    USDC ERC-20 address on this chain
//
// Signing keys (shared across all chains):
//
//	PRIVATE_KEY                relayer EOA for gas (hex, no 0x needed)
//	SAFE_SIGNER_KEY_1          TEE key 1 (required)
//	SAFE_SIGNER_KEY_2          TEE key 2 (optional)
//	SAFE_SIGNER_KEY_3          TEE key 3 (optional)
//	LUCKY_DRAW_ADMIN_KEY       4th signer to reach 4-of-5 threshold (required)
//
// Optional:
//
//	LUCKY_DRAW_PORT            HTTP listen port (default 8082)
//	LUCKY_DRAW_MIN_USDC        min drip in whole USDC (default 5)
//	LUCKY_DRAW_MAX_USDC        max drip in whole USDC (default 10)
//	LUCKY_DRAW_DB_PATH         PebbleDB path (default ./data/luckydraw)
//	LUCKY_DRAW_ADMIN_API_KEY   bearer key for /lucky-draw/admin/* routes
func LoadConfig() (*Config, error) {
	godotenv.Load()

	countStr := ldEnv("LUCKY_DRAW_CHAIN_COUNT", "2")
	count, err := strconv.Atoi(countStr)
	if err != nil || count < 1 || count > 10 {
		return nil, fmt.Errorf("LUCKY_DRAW_CHAIN_COUNT must be 1–10, got %q", countStr)
	}

	chains := make([]ChainEntry, 0, count)
	for i := 1; i <= count; i++ {
		p := fmt.Sprintf("LUCKY_DRAW_CHAIN_%d_", i)

		chainIDStr := os.Getenv(p + "ID")
		if chainIDStr == "" {
			return nil, fmt.Errorf("%sID is required", p)
		}
		chainID, ok := new(big.Int).SetString(chainIDStr, 10)
		if !ok {
			return nil, fmt.Errorf("invalid %sID: %s", p, chainIDStr)
		}

		rpcURL := os.Getenv(p + "RPC")
		if rpcURL == "" {
			return nil, fmt.Errorf("%sRPC is required", p)
		}

		safeAddr := os.Getenv(p + "SAFE")
		if safeAddr == "" {
			return nil, fmt.Errorf("%sSAFE is required", p)
		}
		if !common.IsHexAddress(safeAddr) {
			return nil, fmt.Errorf("invalid %sSAFE: %s", p, safeAddr)
		}

		usdcStr := os.Getenv(p + "USDC")
		if usdcStr == "" {
			return nil, fmt.Errorf("%sUSDC is required", p)
		}
		if !common.IsHexAddress(usdcStr) {
			return nil, fmt.Errorf("invalid %sUSDC: %s", p, usdcStr)
		}

		chains = append(chains, ChainEntry{
			ChainID:     chainID,
			ChainName:   ldEnv(p+"NAME", fmt.Sprintf("chain-%d", i)),
			RPCURL:      rpcURL,
			SafeAddress: safeAddr,
			USDCAddress: common.HexToAddress(usdcStr),
		})
	}

	privateKey := os.Getenv("PRIVATE_KEY")
	if privateKey == "" {
		return nil, fmt.Errorf("PRIVATE_KEY is required")
	}

	// Collect signing keys: up to 3 TEE keys + mandatory admin key.
	var signerKeys []*ecdsa.PrivateKey
	for i := 1; i <= 3; i++ {
		hexKey := strings.TrimPrefix(os.Getenv(fmt.Sprintf("SAFE_SIGNER_KEY_%d", i)), "0x")
		if hexKey == "" {
			continue
		}
		k, err := crypto.HexToECDSA(hexKey)
		if err != nil {
			return nil, fmt.Errorf("invalid SAFE_SIGNER_KEY_%d: %w", i, err)
		}
		signerKeys = append(signerKeys, k)
	}

	// LUCKY_DRAW_ADMIN_KEY is the 4th signer for a 4-of-5 production Safe.
	// Optional: omit when testing against the existing 3-of-6 testnets Safes
	// (threshold = 3, so SAFE_SIGNER_KEY_1/2/3 alone are sufficient).
	if adminKeyHex := strings.TrimPrefix(os.Getenv("LUCKY_DRAW_ADMIN_KEY"), "0x"); adminKeyHex != "" {
		adminKey, err := crypto.HexToECDSA(adminKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid LUCKY_DRAW_ADMIN_KEY: %w", err)
		}
		signerKeys = append(signerKeys, adminKey)
	}

	if len(signerKeys) == 0 {
		return nil, fmt.Errorf("at least SAFE_SIGNER_KEY_1 is required")
	}

	minUSDC, err := strconv.Atoi(ldEnv("LUCKY_DRAW_MIN_USDC", "5"))
	if err != nil {
		return nil, fmt.Errorf("invalid LUCKY_DRAW_MIN_USDC: %w", err)
	}
	maxUSDC, err := strconv.Atoi(ldEnv("LUCKY_DRAW_MAX_USDC", "10"))
	if err != nil {
		return nil, fmt.Errorf("invalid LUCKY_DRAW_MAX_USDC: %w", err)
	}
	if minUSDC < 1 || maxUSDC < minUSDC {
		return nil, fmt.Errorf("LUCKY_DRAW_MIN_USDC (%d) must be >= 1 and <= LUCKY_DRAW_MAX_USDC (%d)", minUSDC, maxUSDC)
	}

	winChance := 0.12
	if v := os.Getenv("LUCKY_DRAW_WIN_CHANCE"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			return nil, fmt.Errorf("LUCKY_DRAW_WIN_CHANCE must be a float in [0, 1], got %q", v)
		}
		winChance = f
	}

	return &Config{
		Port:           ldEnv("LUCKY_DRAW_PORT", "8082"),
		Chains:         chains,
		MinUSDC:        minUSDC,
		MaxUSDC:        maxUSDC,
		WinChance:      winChance,
		DBPath:         ldEnv("LUCKY_DRAW_DB_PATH", "./data/luckydraw"),
		PrivateKey:     privateKey,
		SafeSignerKeys: signerKeys,
		AdminAPIKey:    os.Getenv("LUCKY_DRAW_ADMIN_API_KEY"),
	}, nil
}

func ldEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
