package relayer

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// Relayer manages cross-chain intent settlement with privacy preservation
type Relayer struct {
	cfg            *config.Config
	clients        map[string]*blockchain.Client
	recipientStore map[string]*types.RecipientInfo
	mu             sync.RWMutex
}

// NewRelayer creates a new relayer instance
func NewRelayer(cfg *config.Config) (*Relayer, error) {
	clients := make(map[string]*blockchain.Client)

	// Initialize blockchain clients for each network
	for networkKey, networkCfg := range cfg.Networks {
		client, err := blockchain.NewClient(
			networkCfg.RpcURL,
			networkCfg.BridgeAddress,
			cfg.PrivateKey,
			networkCfg.ChainID,
			networkCfg.Name,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create client for %s: %w", networkKey, err)
		}
		clients[networkKey] = client
		log.WithField("network", networkKey).Info("Relayer blockchain client initialized")
	}

	return &Relayer{
		cfg:            cfg,
		clients:        clients,
		recipientStore: make(map[string]*types.RecipientInfo),
	}, nil
}

// StoreRecipients stores recipient information for an intent (simulates TEE)
func (r *Relayer) StoreRecipients(req *types.StoreRecipientsRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate arrays match
	if len(req.Recipients) != len(req.Amounts) {
		return fmt.Errorf("recipients and amounts length mismatch")
	}

	// Calculate total amount
	totalAmount := big.NewInt(0)
	for _, amountStr := range req.Amounts {
		amount, ok := new(big.Int).SetString(amountStr, 10)
		if !ok {
			return fmt.Errorf("invalid amount: %s", amountStr)
		}
		totalAmount.Add(totalAmount, amount)
	}

	// Store recipient information
	r.recipientStore[req.IntentID] = &types.RecipientInfo{
		Success:     true,
		IntentID:    req.IntentID,
		Recipients:  req.Recipients,
		Amounts:     req.Amounts,
		Timestamp:   time.Now().Unix(),
		ChainID:     req.ChainID,
		TotalAmount: totalAmount.String(),
	}

	log.WithFields(log.Fields{
		"intentID":   req.IntentID,
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

// GetRecipientsForWinner retrieves recipients only if the requester is the winning solver
func (r *Relayer) GetRecipientsForWinner(ctx context.Context, intentID string, requesterAddr string) (*types.RecipientInfo, error) {
	// First check if recipients exist
	r.mu.RLock()
	info, ok := r.recipientStore[intentID]
	chainID := info.ChainID
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("recipients not found for intent %s", intentID)
	}

	// Determine source chain to check (opposite of destination chain)
	// The stored chainID is the destination chain, so we need to check the OTHER chain (source)
	// Source chain is where the auction happened and winner deposited
	var sourceNetwork string
	for key, cfg := range r.cfg.Networks {
		if cfg.ChainID.Int64() != chainID {
			sourceNetwork = key
			break
		}
	}

	if sourceNetwork == "" {
		return nil, fmt.Errorf("could not determine source network")
	}

	log.WithFields(log.Fields{
		"intentID":          intentID,
		"destinationChainID": chainID,
		"sourceNetwork":     sourceNetwork,
	}).Debug("Checking intent on source chain")

	client := r.clients[sourceNetwork]
	intentIDBig, ok := new(big.Int).SetString(intentID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid intent ID")
	}

	// Get intent from blockchain
	intent, err := client.GetIntent(ctx, intentIDBig)
	if err != nil {
		return nil, fmt.Errorf("failed to get intent from blockchain: %w", err)
	}

	// Check if the requester is the winning solver and has deposited
	log.WithFields(log.Fields{
		"intentID":      intentID,
		"deposited":     intent.Deposited,
		"winningSolver": intent.WinningSolver,
		"requester":     requesterAddr,
	}).Info("Checking authorization for recipient access")

	if !intent.Deposited {
		log.WithField("intentID", intentID).Warn("Intent not deposited yet")
		return nil, fmt.Errorf("intent not deposited yet - solver must deposit first")
	}

	requesterAddress := common.HexToAddress(requesterAddr)
	winningAddress := common.HexToAddress(intent.WinningSolver)

	log.WithFields(log.Fields{
		"intentID":             intentID,
		"requesterAddr":        requesterAddr,
		"requesterAddressNorm": requesterAddress.Hex(),
		"winningSolver":        intent.WinningSolver,
		"winningAddressNorm":   winningAddress.Hex(),
		"match":                requesterAddress == winningAddress,
	}).Info("Address comparison")

	if requesterAddress != winningAddress {
		log.WithFields(log.Fields{
			"intentID":        intentID,
			"requester":       requesterAddr,
			"winningSolver":   intent.WinningSolver,
		}).Warn("Unauthorized recipient access attempt")
		return nil, fmt.Errorf("access denied: not the winning solver for this intent")
	}

	log.WithFields(log.Fields{
		"intentID": intentID,
		"solver":   requesterAddr,
	}).Info("Authorized recipient access by winning solver")

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
	log.WithField("intentID", intentID).Info("Deleted recipient information")

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

// SettleIntent settles an intent with basic verification (no zkverify)
func (r *Relayer) SettleIntent(ctx context.Context, req *types.SettleRequest) (*types.RelayerResponse, error) {
	// Validate network
	client, ok := r.clients[req.Network]
	if !ok {
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("network %s not found", req.Network),
		}, nil
	}

	// Parse intent IDs
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

	solverAddr := common.HexToAddress(req.SolverAddress)

	// Basic verification: Check if intent is solved on destination chain
	destNetwork := r.getDestinationNetwork(req.Network)
	solved, err := r.VerifyIntentSolved(ctx, destNetwork, req.Chain2IntentID)
	if err != nil {
		log.WithError(err).Error("Failed to verify intent solved")
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("verification failed: %v", err),
		}, nil
	}

	if !solved {
		return &types.RelayerResponse{
			Success: false,
			Error:   "intent not solved on destination chain",
		}, nil
	}

	log.WithFields(log.Fields{
		"chain1IntentID": chain1IntentID,
		"chain2IntentID": chain2IntentID,
		"solver":         solverAddr.Hex(),
	}).Info("Intent verified, settling...")

	// Settle the intent
	tx, err := client.SettleIntent(ctx, chain1IntentID, chain2IntentID, solverAddr)
	if err != nil {
		log.WithError(err).Error("Failed to settle intent")
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("settlement failed: %v", err),
		}, nil
	}

	// Wait for transaction receipt
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

// getDestinationNetwork returns the opposite network
func (r *Relayer) getDestinationNetwork(sourceNetwork string) string {
	if sourceNetwork == "horizen" {
		return "base"
	}
	return "horizen"
}

// GetNetworks returns the list of configured networks
func (r *Relayer) GetNetworks() []string {
	networks := make([]string, 0, len(r.clients))
	for network := range r.clients {
		networks = append(networks, network)
	}
	return networks
}

// GetChainInfo returns chain information for all networks
func (r *Relayer) GetChainInfo() map[string]string {
	info := make(map[string]string)
	for networkKey, cfg := range r.cfg.Networks {
		info[networkKey] = fmt.Sprintf("Chain %s (ID: %s)", cfg.Name, cfg.ChainID.String())
	}
	return info
}

// Close closes all blockchain clients
func (r *Relayer) Close() {
	for _, client := range r.clients {
		client.Close()
	}
}
