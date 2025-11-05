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
	// Network configurations (maintained for backward compatibility)
	Networks map[string]types.NetworkConfig

	// Chain pairs (new multi-pair support)
	ChainPairs []types.ChainPair

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
	godotenv.Load()

	cfg := &Config{
		Networks:   make(map[string]types.NetworkConfig),
		ChainPairs: []types.ChainPair{},
	}

	pairCountStr := getEnv("PAIR_COUNT", "1")
	pairCount, err := strconv.Atoi(pairCountStr)
	if err != nil || pairCount < 1 || pairCount > 10 {
		return nil, fmt.Errorf("PAIR_COUNT must be between 1 and 10, got: %s", pairCountStr)
	}

	for i := 1; i <= pairCount; i++ {
		prefix := fmt.Sprintf("PAIR_%d_", i)

		sourceChainID, ok := new(big.Int).SetString(getEnv(prefix+"SOURCE_CHAIN_ID", ""), 10)
		if !ok {
			return nil, fmt.Errorf("invalid %sSOURCE_CHAIN_ID", prefix)
		}

		destChainID, ok := new(big.Int).SetString(getEnv(prefix+"DEST_CHAIN_ID", ""), 10)
		if !ok {
			return nil, fmt.Errorf("invalid %sDEST_CHAIN_ID", prefix)
		}

		sourceBridge := getEnv(prefix+"SOURCE_BRIDGE", "")
		if !common.IsHexAddress(sourceBridge) {
			return nil, fmt.Errorf("invalid %sSOURCE_BRIDGE address: %s", prefix, sourceBridge)
		}

		destBridge := getEnv(prefix+"DEST_BRIDGE", "")
		if !common.IsHexAddress(destBridge) {
			return nil, fmt.Errorf("invalid %sDEST_BRIDGE address: %s", prefix, destBridge)
		}

		sourceChain := types.NetworkConfig{
			Name:          getEnv(prefix+"SOURCE_CHAIN", ""),
			RpcURL:        getEnv(prefix+"SOURCE_RPC", ""),
			ChainID:       sourceChainID,
			BridgeAddress: sourceBridge,
			Explorer:      getEnv(prefix+"SOURCE_EXPLORER", ""),
		}

		destChain := types.NetworkConfig{
			Name:          getEnv(prefix+"DEST_CHAIN", ""),
			RpcURL:        getEnv(prefix+"DEST_RPC", ""),
			ChainID:       destChainID,
			BridgeAddress: destBridge,
			Explorer:      getEnv(prefix+"DEST_EXPLORER", ""),
		}

		cfg.ChainPairs = append(cfg.ChainPairs, types.ChainPair{
			SourceChain: sourceChain,
			DestChain:   destChain,
		})

		cfg.Networks[sourceChain.Name] = sourceChain
		cfg.Networks[destChain.Name] = destChain
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
