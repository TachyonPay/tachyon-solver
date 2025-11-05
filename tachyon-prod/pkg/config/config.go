package config

import (
	"fmt"
	"math/big"
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/joho/godotenv"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// Config holds all application configuration
type Config struct {
	// Network configurations
	Networks map[string]types.NetworkConfig

	// Solver configuration
	SolverConfig types.SolverConfig
	RelayerURL   string

	// Wallet configuration
	PrivateKey string

	// Service ports
	SolverPort  string
	RelayerPort string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	// Load .env file if it exists
	godotenv.Load()

	cfg := &Config{
		Networks: make(map[string]types.NetworkConfig),
	}

	// Load network configurations
	horizenChainID := big.NewInt(845320009)
	baseChainID := big.NewInt(84532)

	cfg.Networks["horizen"] = types.NetworkConfig{
		Name:          "Horizen Testnet",
		RpcURL:        getEnv("HORIZEN_TESTNET_RPC_URL", "https://horizen-rpc-testnet.appchain.base.org"),
		ChainID:       horizenChainID,
		BridgeAddress: getEnv("BRIDGE_CONTRACT_HORIZEN_LATEST", ""),
		Explorer:      "https://horizen-explorer-testnet.appchain.base.org",
	}

	cfg.Networks["base"] = types.NetworkConfig{
		Name:          "Base Sepolia",
		RpcURL:        getEnv("BASE_SEPOLIA_RPC_URL", "https://sepolia.base.org"),
		ChainID:       baseChainID,
		BridgeAddress: getEnv("BRIDGE_CONTRACT_BASE_LATEST", ""),
		Explorer:      "https://sepolia-explorer.base.org",
	}

	// Validate critical addresses
	if !common.IsHexAddress(cfg.Networks["horizen"].BridgeAddress) {
		return nil, fmt.Errorf("invalid Horizen bridge address: %s", cfg.Networks["horizen"].BridgeAddress)
	}
	if !common.IsHexAddress(cfg.Networks["base"].BridgeAddress) {
		return nil, fmt.Errorf("invalid Base bridge address: %s", cfg.Networks["base"].BridgeAddress)
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

	// Load service configuration
	cfg.RelayerURL = getEnv("RELAYER_URL", "http://localhost:3000")
	cfg.SolverPort = getEnv("SOLVER_PORT", "3001")
	cfg.RelayerPort = getEnv("RELAYER_PORT", "3000")

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
