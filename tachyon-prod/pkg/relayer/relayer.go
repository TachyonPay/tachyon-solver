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
	cfg            *config.Config
	clients        map[string]*blockchain.Client
	recipientStore map[string]*types.RecipientInfo
	mu             sync.RWMutex
	txMutexes      map[string]*sync.Mutex
}

// NewRelayer creates a new relayer instance
func NewRelayer(cfg *config.Config) (*Relayer, error) {
	clients := make(map[string]*blockchain.Client)
	txMutexes := make(map[string]*sync.Mutex)

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
		txMutexes[networkKey] = &sync.Mutex{}
		log.WithField("network", networkKey).Info("Relayer blockchain client initialized")
	}

	return &Relayer{
		cfg:            cfg,
		clients:        clients,
		recipientStore: make(map[string]*types.RecipientInfo),
		txMutexes:      txMutexes,
	}, nil
}

// StoreRecipients stores recipient information for an intent (simulates TEE)
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

// GetRecipientsForWinner retrieves recipients only if the requester is the winning solver.
// Verifies that the solver has deposited before granting access to recipient information.
func (r *Relayer) GetRecipientsForWinner(ctx context.Context, intentID string, requesterAddr string) (*types.RecipientInfo, error) {
	r.mu.RLock()
	info, ok := r.recipientStore[intentID]
	chainID := info.ChainID
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("recipients not found for intent %s", intentID)
	}

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

	client := r.clients[sourceNetwork]
	intentIDBig, ok := new(big.Int).SetString(intentID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid intent ID")
	}

	intent, err := client.GetIntent(ctx, intentIDBig)
	if err != nil {
		return nil, fmt.Errorf("failed to get intent from blockchain: %w", err)
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


	destNetwork := r.getDestinationNetwork(req.Network)

	var solved bool
	var err error
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}

		solved, err = r.VerifyIntentSolved(ctx, destNetwork, req.Chain2IntentID)
		if err != nil {
			log.WithError(err).Warn("Verification attempt failed")
			continue
		}

		if solved {
			break
		}
	}

	if err != nil {
		log.WithError(err).Error("Failed to verify intent solved after retries")
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("verification failed: %v", err),
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

func (r *Relayer) getDestinationNetwork(sourceNetwork string) string {
	if sourceNetwork == "horizen" {
		return "base"
	}
	return "horizen"
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

func (r *Relayer) GetNetworks() []string {
	networks := make([]string, 0, len(r.clients))
	for network := range r.clients {
		networks = append(networks, network)
	}
	return networks
}

func (r *Relayer) GetChainInfo() map[string]string {
	info := make(map[string]string)
	for networkKey, cfg := range r.cfg.Networks {
		info[networkKey] = fmt.Sprintf("Chain %s (ID: %s)", cfg.Name, cfg.ChainID.String())
	}
	return info
}

func (r *Relayer) Close() {
	for _, client := range r.clients {
		client.Close()
	}
}
