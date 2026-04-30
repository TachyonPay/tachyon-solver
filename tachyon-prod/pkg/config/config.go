package config

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// Config holds all application configuration
type Config struct {
	// Chain configurations - key is chain ID as string
	Chains map[string]types.NetworkConfig

	// Solver configuration
	SolverConfig types.SolverConfig
	RelayerURL   string

	// Wallet configuration
	PrivateKey string

	// SafeSignerKeys are the 3 TEE private keys that co-sign every Safe transaction.
	// All keys sign each tx; signatures are sorted by address before submission.
	// Set via SAFE_SIGNER_KEY_1, SAFE_SIGNER_KEY_2, SAFE_SIGNER_KEY_3.
	SafeSignerKeys []*ecdsa.PrivateKey

	// ECIES keypair for encrypting recipient information
	ECIESPrivateKey *ecies.PrivateKey
	ECIESPublicKey  *ecies.PublicKey

	// Service ports
	SolverPort  string
	RelayerPort string

	// Database path for PebbleDB
	DBPath string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	godotenv.Load()

	cfg := &Config{
		Chains: make(map[string]types.NetworkConfig),
	}

	// Load chain configurations
	// Chain count determines how many chains the solver supports
	chainCountStr := getEnv("CHAIN_COUNT", "3")
	chainCount, err := strconv.Atoi(chainCountStr)
	if err != nil || chainCount < 1 || chainCount > 20 {
		return nil, fmt.Errorf("CHAIN_COUNT must be between 1 and 20, got: %s", chainCountStr)
	}

	for i := 1; i <= chainCount; i++ {
		prefix := fmt.Sprintf("CHAIN_%d_", i)

		chainIDStr := getEnv(prefix+"ID", "")
		chainID, ok := new(big.Int).SetString(chainIDStr, 10)
		if !ok || chainID.Cmp(big.NewInt(0)) == 0 {
			return nil, fmt.Errorf("invalid or missing %sID: %s", prefix, chainIDStr)
		}

		bridgeAddr := getEnv(prefix+"BRIDGE", "")
		if !common.IsHexAddress(bridgeAddr) {
			return nil, fmt.Errorf("invalid %sBRIDGE address: %s", prefix, bridgeAddr)
		}

		rpcURL := getEnv(prefix+"RPC", "")
		if rpcURL == "" {
			return nil, fmt.Errorf("missing %sRPC", prefix)
		}

		safeAddr := getEnv(prefix+"SAFE_ADDRESS", "")
		if safeAddr != "" && !common.IsHexAddress(safeAddr) {
			return nil, fmt.Errorf("invalid %sSAFE_ADDRESS: %s", prefix, safeAddr)
		}

		// Multicall3 address: canonical address works on most EVM chains; override per chain if needed.
		// Set CHAIN_i_MULTICALL3 to the chain-specific address (e.g. Horizen testnet).
		multicall3Addr := getEnv(prefix+"MULTICALL3", "0xcA11bde05977b3631167028862bE2a173976CA11")
		if multicall3Addr != "" && !common.IsHexAddress(multicall3Addr) {
			return nil, fmt.Errorf("invalid %sMULTICALL3 address: %s", prefix, multicall3Addr)
		}

		chainConfig := types.NetworkConfig{
			Name:              getEnv(prefix+"NAME", fmt.Sprintf("chain-%s", chainIDStr)),
			RpcURL:            rpcURL,
			ChainID:           chainID,
			BridgeAddress:     bridgeAddr,
			Explorer:          getEnv(prefix+"EXPLORER", ""),
			SafeAddress:       safeAddr,
			Multicall3Address: multicall3Addr,
		}

		// Use chain ID as the map key
		cfg.Chains[chainIDStr] = chainConfig
	}

	// Load solver configuration
	minProfitMargin, err := strconv.ParseFloat(getEnv("MIN_PROFIT_MARGIN", "0.02"), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid MIN_PROFIT_MARGIN: %w", err)
	}

	maxBidAmount, ok := new(big.Int).SetString(getEnv("MAX_BID_AMOUNT", "1000000000000000000000"), 10) // 1000 ether in wei
	if !ok {
		return nil, fmt.Errorf("invalid MAX_BID_AMOUNT")
	}

	bidIncrement, ok := new(big.Int).SetString(getEnv("BID_INCREMENT", "1000000000000000000"), 10) // 1 ether in wei
	if !ok {
		return nil, fmt.Errorf("invalid BID_INCREMENT")
	}

	balanceBuffer, err := strconv.ParseFloat(getEnv("BALANCE_BUFFER", "0.1"), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid BALANCE_BUFFER: %w", err)
	}

	cfg.SolverConfig = types.SolverConfig{
		MinProfitMargin: minProfitMargin,
		MaxBidAmount:    maxBidAmount,
		BidIncrement:    bidIncrement,
		BalanceBuffer:   balanceBuffer,
	}

	// Load wallet configuration
	cfg.PrivateKey = os.Getenv("PRIVATE_KEY")
	if cfg.PrivateKey == "" {
		return nil, fmt.Errorf("PRIVATE_KEY environment variable is required")
	}

	// Load TEE Safe signing keys (optional — only needed when a chain has SAFE_ADDRESS set)
	for i := 1; i <= 3; i++ {
		envKey := fmt.Sprintf("SAFE_SIGNER_KEY_%d", i)
		hexKey := strings.TrimPrefix(os.Getenv(envKey), "0x")
		if hexKey == "" {
			continue
		}
		privKey, err := crypto.HexToECDSA(hexKey)
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", envKey, err)
		}
		cfg.SafeSignerKeys = append(cfg.SafeSignerKeys, privKey)
	}

	// Load service configuration
	cfg.RelayerURL = getEnv("RELAYER_URL", "http://localhost:3000")
	cfg.SolverPort = getEnv("SOLVER_PORT", "3001")
	cfg.RelayerPort = getEnv("RELAYER_PORT", "3000")
	cfg.DBPath = getEnv("DB_PATH", "./data")

	// Load or generate ECIES keypair for recipient encryption
	eciesPrivKeyHex := os.Getenv("ECIES_PRIVATE_KEY")
	if eciesPrivKeyHex == "" {
		// Generate a new ECIES keypair
		privKey, err := ecies.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ECIES keypair: %w", err)
		}
		cfg.ECIESPrivateKey = privKey
		cfg.ECIESPublicKey = privKey.PublicKey

		// Print the generated private key so it can be added to .env
		fmt.Printf("\nWARNING: No ECIES_PRIVATE_KEY found in environment. Generated new keypair.\n")
		fmt.Printf("Add this to your .env file:\n")
		fmt.Printf("ECIES_PRIVATE_KEY=%s\n\n", hex.EncodeToString(privKey.Bytes()))
	} else {
		// Load existing private key from environment
		privKeyBytes, err := hex.DecodeString(eciesPrivKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid ECIES_PRIVATE_KEY hex: %w", err)
		}

		privKey := ecies.NewPrivateKeyFromBytes(privKeyBytes)
		cfg.ECIESPrivateKey = privKey
		cfg.ECIESPublicKey = privKey.PublicKey
	}

	return cfg, nil
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// GetChainByID retrieves a chain configuration by chain ID
func (c *Config) GetChainByID(chainID *big.Int) (types.NetworkConfig, error) {
	chain, exists := c.Chains[chainID.String()]
	if !exists {
		return types.NetworkConfig{}, fmt.Errorf("chain ID %s not configured", chainID.String())
	}
	return chain, nil
}
