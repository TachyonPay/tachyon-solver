package relayer

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// Relayer manages cross-chain intent settlement with privacy preservation
type Relayer struct {
	cfg              *config.Config
	clients          map[string]*blockchain.Client
	recipientStore   map[string]*types.RecipientInfo
	registeredSolvers map[string]*types.SolverInfo
	mu               sync.RWMutex
	txMutexes        map[string]*sync.Mutex
}

// NewRelayer creates a new relayer instance
func NewRelayer(cfg *config.Config) (*Relayer, error) {
	clients := make(map[string]*blockchain.Client)
	txMutexes := make(map[string]*sync.Mutex)

	// Initialize clients for each configured chain
	for chainID, chainCfg := range cfg.Chains {
		client, err := blockchain.NewClient(
			chainCfg.RpcURL,
			chainCfg.BridgeAddress,
			cfg.PrivateKey,
			chainCfg.ChainID,
			chainCfg.Name,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create client for chain %s (%s): %w", chainCfg.Name, chainID, err)
		}
		clients[chainID] = client
		txMutexes[chainID] = &sync.Mutex{}
		log.WithFields(log.Fields{
			"chain":   chainCfg.Name,
			"chainID": chainID,
		}).Info("Relayer blockchain client initialized")
	}

	return &Relayer{
		cfg:              cfg,
		clients:          clients,
		recipientStore:   make(map[string]*types.RecipientInfo),
		registeredSolvers: make(map[string]*types.SolverInfo),
		txMutexes:        txMutexes,
	}, nil
}

// StoreRecipients stores recipient information for an intent (simulates TEE)
// Uses composite key (sourceChainID-intentID) to handle same intent IDs across different chains
func (r *Relayer) StoreRecipients(req *types.StoreRecipientsRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(req.Recipients) != len(req.Amounts) {
		return fmt.Errorf("recipients and amounts length mismatch")
	}

	totalAmount := big.NewInt(0)
	for _, amountStr := range req.Amounts {
		amount, ok := new(big.Int).SetString(amountStr, 10)
		if !ok {
			return fmt.Errorf("invalid amount: %s", amountStr)
		}
		totalAmount.Add(totalAmount, amount)
	}

	// Find the source chain for this chainID to create composite key
	var sourceChainID int64
	for _, cfg := range r.cfg.Chains {
		if cfg.ChainID.Int64() == req.ChainID {
			sourceChainID = cfg.ChainID.Int64()
			break
		}
	}
	if sourceChainID == 0 {
		return fmt.Errorf("unknown chain ID: %d", req.ChainID)
	}

	// Create composite key: sourceChainID-intentID
	// This ensures intents with same ID on different chains don't collide
	storeKey := fmt.Sprintf("%d-%s", sourceChainID, req.IntentID)

	r.recipientStore[storeKey] = &types.RecipientInfo{
		Success:     true,
		IntentID:    req.IntentID,
		Recipients:  req.Recipients,
		Amounts:     req.Amounts,
		Timestamp:   time.Now().Unix(),
		ChainID:     req.ChainID,
		TotalAmount: totalAmount.String(),
	}

	log.WithFields(log.Fields{
		"storeKey":   storeKey,
		"intentID":   req.IntentID,
		"chainID":    req.ChainID,
		"recipients": len(req.Recipients),
		"totalAmount": totalAmount.String(),
	}).Info("Stored recipient information")

	return nil
}

// GetRecipients retrieves stored recipient information
func (r *Relayer) GetRecipients(intentID string) (*types.RecipientInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, ok := r.recipientStore[intentID]
	if !ok {
		return nil, fmt.Errorf("recipients not found for intent %s", intentID)
	}

	return info, nil
}

// GetRecipientsForWinner retrieves recipients only if the requester is the winning solver.
// Verifies that the solver has deposited before granting access to recipient information.
func (r *Relayer) GetRecipientsForWinner(ctx context.Context, intentID string, requesterAddr string) (*types.RecipientInfo, error) {
	intentIDBig, ok := new(big.Int).SetString(intentID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid intent ID")
	}

	// The intent could be on any source chain, so we need to check all chains
	// to find where this intent was created and verify the winning solver
	var intent *types.Intent
	var sourceChainID int64
	var intentErr error

	for networkKey, client := range r.clients {
		tempIntent, err := client.GetIntent(ctx, intentIDBig)
		if err == nil && tempIntent.User != "" {
			// Found the intent on this chain
			intent = tempIntent
			sourceChainID = r.cfg.Chains[networkKey].ChainID.Int64()
			intentErr = nil
			log.WithFields(log.Fields{
				"intentID":      intentID,
				"network":       networkKey,
				"sourceChainID": sourceChainID,
			}).Debug("Found intent on source chain")
			break
		}
		if err != nil {
			intentErr = err
		}
	}

	if intent == nil {
		if intentErr != nil {
			return nil, fmt.Errorf("failed to get intent from blockchain: %w", intentErr)
		}
		return nil, fmt.Errorf("intent %s not found on any chain", intentID)
	}

	if !intent.Deposited {
		log.WithField("intentID", intentID).Warn("Intent not deposited yet")
		return nil, fmt.Errorf("intent not deposited yet - solver must deposit first")
	}

	requesterAddress := common.HexToAddress(requesterAddr)
	winningAddress := common.HexToAddress(intent.WinningSolver)

	if requesterAddress != winningAddress {
		log.WithFields(log.Fields{
			"intentID":        intentID,
			"requester":       requesterAddr,
			"winningSolver":   intent.WinningSolver,
		}).Warn("Unauthorized recipient access attempt")
		return nil, fmt.Errorf("access denied: not the winning solver for this intent")
	}

	// Retrieve from store using composite key
	storeKey := fmt.Sprintf("%d-%s", sourceChainID, intentID)
	r.mu.RLock()
	info, ok := r.recipientStore[storeKey]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("recipients not found for intent %s on chain %d", intentID, sourceChainID)
	}

	log.WithFields(log.Fields{
		"intentID":      intentID,
		"sourceChainID": sourceChainID,
		"storeKey":      storeKey,
	}).Debug("Retrieved recipients for winning solver")

	return info, nil
}

// ListStoredIntents returns all stored intent IDs
func (r *Relayer) ListStoredIntents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	intents := make([]string, 0, len(r.recipientStore))
	for intentID := range r.recipientStore {
		intents = append(intents, intentID)
	}

	return intents
}

// DeleteRecipients removes stored recipient information
func (r *Relayer) DeleteRecipients(intentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.recipientStore[intentID]; !ok {
		return fmt.Errorf("intent %s not found", intentID)
	}

	delete(r.recipientStore, intentID)
	log.WithField("intentID", intentID).Debug("Deleted recipient information")

	return nil
}

// VerifyIntentSolved verifies if an intent has been solved on the destination chain
func (r *Relayer) VerifyIntentSolved(ctx context.Context, network string, intentID string) (bool, error) {
	client, ok := r.clients[network]
	if !ok {
		return false, fmt.Errorf("network %s not found", network)
	}

	intentIDBig, ok := new(big.Int).SetString(intentID, 10)
	if !ok {
		return false, fmt.Errorf("invalid intent ID: %s", intentID)
	}

	solved, err := client.VerifyIntentSolved(ctx, intentIDBig)
	if err != nil {
		return false, fmt.Errorf("failed to verify intent: %w", err)
	}

	return solved, nil
}

// SettleIntent settles an intent with basic verification on the source chain.
// Verifies the intent is solved on the destination chain before settling.
func (r *Relayer) SettleIntent(ctx context.Context, req *types.SettleRequest) (*types.RelayerResponse, error) {
	// Get client by chain ID (req.Network is chain ID string like "845320009")
	client, ok := r.clients[req.Network]
	if !ok {
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("network %s not found", req.Network),
		}, nil
	}

	chain1IntentID, ok := new(big.Int).SetString(req.IntentID, 10)
	if !ok {
		return &types.RelayerResponse{
			Success: false,
			Error:   "invalid chain1 intent ID",
		}, nil
	}

	chain2IntentID, ok := new(big.Int).SetString(req.Chain2IntentID, 10)
	if !ok {
		return &types.RelayerResponse{
			Success: false,
			Error:   "invalid chain2 intent ID",
		}, nil
	}

	// Read the intent from source chain to get the actual destination chain ID
	intent, err := client.GetIntent(ctx, chain1IntentID)
	if err != nil {
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to read intent from source chain: %v", err),
		}, nil
	}

	// Use the intent's destChainId to find the correct destination network
	destChainIDStr := intent.DestChainId.String()
	destNetwork := destChainIDStr

	log.WithFields(log.Fields{
		"sourceNetwork": req.Network,
		"destNetwork":   destNetwork,
		"destChainID":   destChainIDStr,
		"intentID":      req.IntentID,
	}).Info("Settling intent with correct destination chain")

	var solved bool
	var verifyErr error
	// Increase retries for slow RPC propagation on Sepolia and Base
	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			// Exponential backoff: 3s, 6s, 9s, 12s, 15s...
			backoff := time.Duration(i) * 3 * time.Second
			log.WithFields(log.Fields{
				"attempt": i + 1,
				"backoff": backoff,
				"intentID": req.Chain2IntentID,
				"destNetwork": destNetwork,
			}).Debug("Retrying verification after backoff")
			time.Sleep(backoff)
		}

		solved, verifyErr = r.VerifyIntentSolved(ctx, destNetwork, req.Chain2IntentID)
		if verifyErr != nil {
			log.WithFields(log.Fields{
				"attempt": i + 1,
				"error": verifyErr.Error(),
			}).Warn("Verification attempt failed")
			continue
		}

		if solved {
			log.WithFields(log.Fields{
				"attempt": i + 1,
				"intentID": req.Chain2IntentID,
			}).Info("Intent verified as solved")
			break
		}

		log.WithFields(log.Fields{
			"attempt": i + 1,
			"intentID": req.Chain2IntentID,
		}).Debug("Intent not solved yet, will retry")
	}

	if verifyErr != nil {
		log.WithError(verifyErr).Error("Failed to verify intent solved after retries")
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("verification failed: %v", verifyErr),
		}, nil
	}

	log.WithFields(log.Fields{
		"destNetwork": destNetwork,
		"intentID":    req.Chain2IntentID,
		"solved":      solved,
	}).Info("Verification result")

	if !solved {
		return &types.RelayerResponse{
			Success: false,
			Error:   "intent not solved on destination chain",
		}, nil
	}

	r.txMutexes[req.Network].Lock()
	defer r.txMutexes[req.Network].Unlock()

	tx, err := r.retryTransaction(ctx, req.Network, "settle-intent", func() (*ethtypes.Transaction, error) {
		return client.SettleIntent(ctx, chain1IntentID, chain2IntentID)
	})
	if err != nil {
		log.WithError(err).Error("Failed to settle intent after retries")
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("settlement failed: %v", err),
		}, nil
	}

	time.Sleep(2 * time.Second)

	return &types.RelayerResponse{
		Success:         true,
		TransactionHash: tx.Hash().Hex(),
		Network:         req.Network,
		ChainID:         client.GetAddress().Big().Int64(),
		IntentID:        req.IntentID,
		Chain2IntentID:  req.Chain2IntentID,
		SolverAddress:   req.SolverAddress,
	}, nil
}

func (r *Relayer) getDestinationNetwork(sourceNetworkKey string) string {
	// sourceNetworkKey is a chain ID string like "845320009"
	// We need to find the destination chain ID for verification

	// Get source chain config to find its destination
	sourceChainCfg, ok := r.cfg.Chains[sourceNetworkKey]
	if !ok {
		log.WithField("sourceNetworkKey", sourceNetworkKey).Warn("Source network not found in config")
		return ""
	}

	// For now, use simple logic: if source is horizen, dest is base, otherwise dest is horizen
	// In future, this should be more flexible based on intent.DestChainId
	if sourceChainCfg.Name == "horizen" {
		// Find base chain ID
		for chainID, cfg := range r.cfg.Chains {
			if cfg.Name == "base" {
				return chainID
			}
		}
	} else if sourceChainCfg.Name == "base" {
		// Find horizen chain ID
		for chainID, cfg := range r.cfg.Chains {
			if cfg.Name == "horizen" {
				return chainID
			}
		}
	} else if sourceChainCfg.Name == "sepolia" {
		// Find horizen chain ID (most intents from sepolia go to horizen)
		for chainID, cfg := range r.cfg.Chains {
			if cfg.Name == "horizen" {
				return chainID
			}
		}
	}

	// Default fallback: return first available chain that's not the source
	for chainID := range r.cfg.Chains {
		if chainID != sourceNetworkKey {
			return chainID
		}
	}

	return ""
}

// retryTransaction retries a transaction function up to maxRetries times on nonce errors
func (r *Relayer) retryTransaction(ctx context.Context, network string, operation string, txFunc func() (*ethtypes.Transaction, error)) (*ethtypes.Transaction, error) {
	maxRetries := 10
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoffDuration := time.Duration(attempt) * 500 * time.Millisecond
			log.WithFields(log.Fields{
				"operation": operation,
				"attempt":   attempt + 1,
				"backoff":   backoffDuration,
			}).Warn("Retrying transaction after nonce conflict")
			time.Sleep(backoffDuration)
		}

		tx, err := txFunc()
		if err == nil {
			if attempt > 0 {
				log.WithFields(log.Fields{
					"operation": operation,
					"attempt":   attempt + 1,
				}).Info("Transaction succeeded after retry")
			}
			return tx, nil
		}

		lastErr = err
		errStr := err.Error()

		isNonceError := strings.Contains(errStr, "nonce") ||
			strings.Contains(errStr, "replacement transaction underpriced") ||
			strings.Contains(errStr, "already known")

		if !isNonceError {
			return nil, err
		}

		log.WithFields(log.Fields{
			"operation": operation,
			"attempt":   attempt + 1,
			"error":     errStr,
		}).Debug("Nonce conflict detected, will retry")
	}

	log.WithFields(log.Fields{
		"operation":   operation,
		"maxRetries":  maxRetries,
		"finalError":  lastErr.Error(),
	}).Error("Transaction failed after all retries")

	return nil, fmt.Errorf("transaction failed after %d retries: %w", maxRetries, lastErr)
}

// RegisterSolver registers a solver and checks its token balances on supported chains
func (r *Relayer) RegisterSolver(req *types.RegisterSolverRequest) (*types.RegisterSolverResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !common.IsHexAddress(req.SolverAddress) {
		return &types.RegisterSolverResponse{
			Success: false,
			Error:   "invalid solver address",
		}, fmt.Errorf("invalid solver address: %s", req.SolverAddress)
	}

	balances := []types.SolverBalance{}

	for _, route := range req.Routes {
		sourceBalance, err := r.checkTokenBalance(route.SourceChain, req.SolverAddress, route.TokenA)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"solver": req.SolverAddress,
				"chain":  route.SourceChain,
				"token":  route.TokenA,
			}).Warn("Failed to check token balance")
		} else {
			balances = append(balances, *sourceBalance)
		}

		destBalance, err := r.checkTokenBalance(route.DestChain, req.SolverAddress, route.TokenB)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"solver": req.SolverAddress,
				"chain":  route.DestChain,
				"token":  route.TokenB,
			}).Warn("Failed to check token balance")
		} else {
			balances = append(balances, *destBalance)
		}
	}

	now := time.Now()
	r.registeredSolvers[req.SolverAddress] = &types.SolverInfo{
		Address:      req.SolverAddress,
		Routes:       req.Routes,
		Balances:     balances,
		RegisteredAt: now,
		LastSeen:     now,
	}

	log.WithFields(log.Fields{
		"solver":      req.SolverAddress,
		"routesCount": len(req.Routes),
		"balances":    len(balances),
	}).Info("Solver registered successfully")

	return &types.RegisterSolverResponse{
		Success:      true,
		SolverAddress: req.SolverAddress,
		RoutesCount:  len(req.Routes),
		Balances:     balances,
	}, nil
}

// getClientByName retrieves a blockchain client by chain name
func (r *Relayer) getClientByName(chainName string) (*blockchain.Client, error) {
	// Look up client by matching chain name in config
	for chainID, chainCfg := range r.cfg.Chains {
		if chainCfg.Name == chainName {
			if client, ok := r.clients[chainID]; ok {
				return client, nil
			}
		}
	}
	return nil, fmt.Errorf("chain %s not found", chainName)
}

// checkTokenBalance checks the token balance of a solver on a specific chain
func (r *Relayer) checkTokenBalance(chainName, solverAddress, tokenAddress string) (*types.SolverBalance, error) {
	client, err := r.getClientByName(chainName)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	balance, err := client.GetTokenBalance(ctx, common.HexToAddress(solverAddress), common.HexToAddress(tokenAddress))
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	return &types.SolverBalance{
		Chain:   chainName,
		Token:   tokenAddress,
		Balance: balance.String(),
	}, nil
}

// GetRoutes returns available routes based on filter criteria.
// Supports flexible querying:
//   - All routes: no parameters
//   - All routes from chain: ?sourceChain=horizen or ?origin=horizen
//   - Routes by origin asset: ?sourceChain=horizen&tokenA=0x...
//   - Specific route: all parameters provided
func (r *Relayer) GetRoutes(filter *types.GetRoutesRequest) *types.GetRoutesResponse {
	r.mu.RLock()
	defer r.mu.RUnlock()

	uniqueRoutes := make(map[string]types.Route)
	solversWithRoutes := []types.SolverInfo{}

	for _, solverInfo := range r.registeredSolvers {
		matchedRoutes := []types.Route{}

		for _, route := range solverInfo.Routes {
			if r.matchesFilter(route, filter) {
				routeKey := fmt.Sprintf("%s-%s-%s-%s", route.SourceChain, route.DestChain, route.TokenA, route.TokenB)
				uniqueRoutes[routeKey] = route
				matchedRoutes = append(matchedRoutes, route)
			}
		}

		if len(matchedRoutes) > 0 {
			solverWithMatchedRoutes := *solverInfo
			solverWithMatchedRoutes.Routes = matchedRoutes
			solversWithRoutes = append(solversWithRoutes, solverWithMatchedRoutes)
		}
	}

	routes := make([]types.Route, 0, len(uniqueRoutes))
	for _, route := range uniqueRoutes {
		routes = append(routes, route)
	}

	return &types.GetRoutesResponse{
		Success: true,
		Routes:  routes,
		Solvers: solversWithRoutes,
	}
}

// matchesFilter checks if a route matches the filter criteria
func (r *Relayer) matchesFilter(route types.Route, filter *types.GetRoutesRequest) bool {
	if filter.SourceChain != "" && route.SourceChain != filter.SourceChain {
		return false
	}
	if filter.DestChain != "" && route.DestChain != filter.DestChain {
		return false
	}
	if filter.TokenA != "" && route.TokenA != filter.TokenA {
		return false
	}
	if filter.TokenB != "" && route.TokenB != filter.TokenB {
		return false
	}
	return true
}

func (r *Relayer) GetNetworks() []string {
	networks := make([]string, 0, len(r.clients))
	for network := range r.clients {
		networks = append(networks, network)
	}
	return networks
}

func (r *Relayer) GetChainInfo() map[string]string {
	info := make(map[string]string)
	for networkKey, cfg := range r.cfg.Chains {
		info[networkKey] = fmt.Sprintf("Chain %s (ID: %s)", cfg.Name, cfg.ChainID.String())
	}
	return info
}

func (r *Relayer) Close() {
	for _, client := range r.clients {
		client.Close()
	}
}
