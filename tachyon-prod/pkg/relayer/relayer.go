package relayer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

type Relayer struct {
	cfg              *config.Config
	clients          map[string]*blockchain.Client
	store            *storage.Store
	recipientStore   map[string]*types.RecipientInfo   // In-memory cache
	intentDetailsStore map[string]*types.IntentDetails // In-memory cache
	registeredSolvers map[string]*types.SolverInfo     // In-memory cache
	mu               sync.RWMutex
	txMutexes        map[string]*sync.Mutex
}

func NewRelayer(cfg *config.Config) (*Relayer, error) {
	clients := make(map[string]*blockchain.Client)
	txMutexes := make(map[string]*sync.Mutex)

	// Initialize PebbleDB storage
	dbPath := filepath.Join(cfg.DBPath, "relayer")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	for chainID, chainCfg := range cfg.Chains {
		client, err := blockchain.NewClient(
			chainCfg.RpcURL,
			chainCfg.BridgeAddress,
			cfg.PrivateKey,
			chainCfg.ChainID,
			chainCfg.Name,
			"",  // relayer uses its own key directly, no Safe
			nil, // no Safe signing keys needed for relayer
			chainCfg.Multicall3Address,
		)
		if err != nil {
			store.Close()
			return nil, fmt.Errorf("failed to create client for chain %s (%s): %w", chainCfg.Name, chainID, err)
		}
		clients[chainID] = client
		txMutexes[chainID] = &sync.Mutex{}
		log.WithFields(log.Fields{
			"chain":   chainCfg.Name,
			"chainID": chainID,
		}).Info("Relayer blockchain client initialized")
	}

	relayer := &Relayer{
		cfg:                cfg,
		clients:            clients,
		store:              store,
		recipientStore:     make(map[string]*types.RecipientInfo),
		intentDetailsStore: make(map[string]*types.IntentDetails),
		registeredSolvers:  make(map[string]*types.SolverInfo),
		txMutexes:          txMutexes,
	}

	// Load persisted data from database
	if err := relayer.loadPersistedData(); err != nil {
		log.WithError(err).Warn("Failed to load persisted data, starting fresh")
	}

	log.WithField("dbPath", dbPath).Info("Relayer initialized with persistent storage")

	return relayer, nil
}

// loadPersistedData loads previously saved data from the database
func (r *Relayer) loadPersistedData() error {
	// Load solvers
	solvers, err := r.store.GetAllSolvers()
	if err != nil {
		return fmt.Errorf("failed to load solvers: %w", err)
	}
	r.mu.Lock()
	for addr, info := range solvers {
		r.registeredSolvers[addr] = info
	}
	r.mu.Unlock()

	log.WithField("solverCount", len(solvers)).Info("Loaded persisted solvers from DB")
	return nil
}

func (r *Relayer) GetECIESPublicKey() string {
	pubKeyBytes := r.cfg.ECIESPublicKey.Bytes(false)
	return hex.EncodeToString(pubKeyBytes)
}

func (r *Relayer) StoreRecipients(req *types.StoreRecipientsRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var recipients []string
	var amounts []string

	if req.EncryptedData != "" {
		encryptedBytes, err := hex.DecodeString(req.EncryptedData)
		if err != nil {
			return fmt.Errorf("invalid encrypted data hex: %w", err)
		}

		decryptedBytes, err := ecies.Decrypt(r.cfg.ECIESPrivateKey, encryptedBytes)
		if err != nil {
			return fmt.Errorf("failed to decrypt recipient data: %w", err)
		}

		var decryptedData struct {
			Recipients []string `json:"recipients"`
			Amounts    []string `json:"amounts"`
		}
		if err := json.Unmarshal(decryptedBytes, &decryptedData); err != nil {
			return fmt.Errorf("invalid decrypted data format: %w", err)
		}

		recipients = decryptedData.Recipients
		amounts = decryptedData.Amounts

		log.WithFields(log.Fields{
			"intentID": req.IntentID,
			"encrypted": true,
		}).Info("Successfully decrypted recipient information")
	} else {
		recipients = req.Recipients
		amounts = req.Amounts

		log.WithFields(log.Fields{
			"intentID": req.IntentID,
			"encrypted": false,
		}).Warn("Received unencrypted recipient data")
	}

	if len(recipients) != len(amounts) {
		return fmt.Errorf("recipients and amounts length mismatch")
	}

	totalAmount := big.NewInt(0)
	for _, amountStr := range amounts {
		amount, ok := new(big.Int).SetString(amountStr, 10)
		if !ok {
			return fmt.Errorf("invalid amount: %s", amountStr)
		}
		totalAmount.Add(totalAmount, amount)
	}

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

	storeKey := fmt.Sprintf("%d-%s", sourceChainID, req.IntentID)

	recipientInfo := &types.RecipientInfo{
		Success:     true,
		IntentID:    req.IntentID,
		Recipients:  recipients,
		Amounts:     amounts,
		Timestamp:   time.Now().Unix(),
		ChainID:     req.ChainID,
		TotalAmount: totalAmount.String(),
	}

	r.recipientStore[storeKey] = recipientInfo

	// Persist to database
	if err := r.store.SaveRecipientInfo(sourceChainID, req.IntentID, recipientInfo); err != nil {
		log.WithError(err).Warn("Failed to persist recipient info to database")
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

// CreatePendingTransaction records a pending TransactionRecord. Idempotent: no-op if already exists.
func (r *Relayer) CreatePendingTransaction(record *types.TransactionRecord) error {
	existing, _ := r.store.GetTransactionRecord(record.SourceChainID, record.IntentID)
	if existing != nil {
		return nil
	}
	now := time.Now()
	record.Status = "pending"
	record.CreatedAt = now
	record.UpdatedAt = now
	return r.store.SaveTransactionRecord(record)
}

// CompleteTransaction upserts a TransactionRecord to completed status.
// If the record already exists it is updated with the provided tx hashes.
// If it does not exist (relayer never saw the IntentCreated event) a new
// completed record is created from the full payload the solver sent.
func (r *Relayer) CompleteTransaction(req *types.CompleteTransactionRequest) error {
	record, err := r.store.GetTransactionRecord(req.SourceChainID, req.IntentID)
	if err != nil {
		return fmt.Errorf("failed to load transaction record: %w", err)
	}

	now := time.Now()
	if record == nil {
		record = &types.TransactionRecord{
			IntentID:        req.IntentID,
			UserAddress:     req.UserAddress,
			SourceChainID:   req.SourceChainID,
			DestChainID:     req.DestChainID,
			TokenA:          req.TokenA,
			TokenB:          req.TokenB,
			AmountA:         req.AmountA,
			ExpectedAmountB: req.ExpectedAmountB,
			CreatedAt:       now,
		}
	}

	record.Status = "completed"
	if req.DepositTxHash != "" {
		record.DepositTxHash = req.DepositTxHash
	}
	if req.SolveTxHash != "" {
		record.SolveTxHash = req.SolveTxHash
	}
	if req.SettleTxHash != "" {
		record.SettleTxHash = req.SettleTxHash
	}
	record.UpdatedAt = now
	record.CompletedAt = &now
	return r.store.SaveTransactionRecord(record)
}

// GetTransactionsByUser returns paginated transaction history for a user.
func (r *Relayer) GetTransactionsByUser(userAddress, status string, page, limit int) ([]*types.TransactionRecord, int, error) {
	return r.store.GetTransactionsByUser(userAddress, status, page, limit)
}

func (r *Relayer) GetRecipients(intentID string) (*types.RecipientInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, ok := r.recipientStore[intentID]
	if !ok {
		return nil, fmt.Errorf("recipients not found for intent %s", intentID)
	}

	return info, nil
}

func (r *Relayer) GetRecipientsForWinner(ctx context.Context, intentID string, sourceChainIDStr string, requesterAddr string) (*types.RecipientInfo, error) {
	intentIDBig, ok := new(big.Int).SetString(intentID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid intent ID")
	}

	var intent *types.Intent
	var sourceChainID int64
	var sourceNetworkKey string
	var intentErr error

	requesterAddress := common.HexToAddress(requesterAddr)

	if sourceChainIDStr != "" {
		sourceChainIDBig, ok := new(big.Int).SetString(sourceChainIDStr, 10)
		if ok {
			// Find the network key for this chain ID
			for networkKey, cfg := range r.cfg.Chains {
				if cfg.ChainID.Cmp(sourceChainIDBig) == 0 {
					client, exists := r.clients[networkKey]
					if exists {
						tempIntent, err := client.GetIntent(ctx, intentIDBig)
						if err == nil && tempIntent.User != "" {
							winningAddress := common.HexToAddress(tempIntent.WinningSolver)
							if winningAddress == requesterAddress {
								intent = tempIntent
								sourceChainID = sourceChainIDBig.Int64()
								sourceNetworkKey = networkKey
								log.WithFields(log.Fields{
									"intentID":      intentID,
									"sourceChainID": sourceChainID,
									"network":       networkKey,
								}).Debug("Found intent using provided source chain ID")
								break
							}
						}
					}
					break
				}
			}
		}
	}

	if intent == nil {
		for networkKey, client := range r.clients {
			tempIntent, err := client.GetIntent(ctx, intentIDBig)
			if err == nil && tempIntent.User != "" {
				winningAddress := common.HexToAddress(tempIntent.WinningSolver)

				if winningAddress == requesterAddress {
					intent = tempIntent
					sourceChainID = r.cfg.Chains[networkKey].ChainID.Int64()
					sourceNetworkKey = networkKey
					intentErr = nil
					log.WithFields(log.Fields{
						"intentID":       intentID,
						"network":        networkKey,
						"sourceChainID":  sourceChainID,
						"winningSolver":  tempIntent.WinningSolver,
					}).Debug("Found correct intent on source chain")
					break
				} else {
					log.WithFields(log.Fields{
						"intentID":      intentID,
						"network":       networkKey,
						"winningSolver": tempIntent.WinningSolver,
						"requester":     requesterAddr,
					}).Debug("Found intent with same ID but different winner, skipping")
				}
			}
			if err != nil {
				intentErr = err
			}
		}
	}

	if intent == nil {
		if intentErr != nil {
			return nil, fmt.Errorf("failed to get intent from blockchain: %w", intentErr)
		}
		return nil, fmt.Errorf("intent %s not found on any chain", intentID)
	}

	// If deposit not yet visible, retry a few times to handle RPC propagation delays
	// This is critical for Base and Sepolia which have very slow state propagation (2+ minutes)
	if !intent.Deposited {
		maxRetries := 15
		client := r.clients[sourceNetworkKey]

		log.WithFields(log.Fields{
			"intentID":  intentID,
			"network":   sourceNetworkKey,
			"deposited": intent.Deposited,
		}).Info("Deposit not visible yet, starting retry loop")

		for i := 0; i < maxRetries; i++ {
			// Exponential backoff with cap: 3s, 6s, 9s, 12s, 15s, 15s, 15s...
			backoff := time.Duration(i+1) * 3 * time.Second
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}

			log.WithFields(log.Fields{
				"intentID": intentID,
				"attempt":  i + 1,
				"network":  sourceNetworkKey,
				"backoff":  backoff,
			}).Info("Waiting for deposit to become visible")
			time.Sleep(backoff)

			freshIntent, err := client.GetIntent(ctx, intentIDBig)
			if err != nil {
				log.WithFields(log.Fields{
					"intentID": intentID,
					"attempt":  i + 1,
					"error":    err.Error(),
				}).Warn("Failed to re-fetch intent")
				continue
			}

			log.WithFields(log.Fields{
				"intentID":  intentID,
				"attempt":   i + 1,
				"deposited": freshIntent.Deposited,
			}).Debug("Re-fetched intent state")

			if freshIntent.Deposited {
				log.WithFields(log.Fields{
					"intentID": intentID,
					"attempt":  i + 1,
				}).Info("Deposit became visible after retry")
				intent = freshIntent
				break
			}
		}

		// Final check after retries
		if !intent.Deposited {
			log.WithFields(log.Fields{
				"intentID":   intentID,
				"network":    sourceNetworkKey,
				"maxRetries": maxRetries,
			}).Error("Intent not deposited after all retries")
			return nil, fmt.Errorf("intent not deposited yet - solver must deposit first")
		}
	}

	// Final verification that requester is the winning solver
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
// The network parameter is the destination chain where the intent was solved
// The sourceChainID parameter is the chain ID where the intent was originally created
func (r *Relayer) VerifyIntentSolved(ctx context.Context, network string, sourceChainID string, intentID string) (bool, error) {
	client, ok := r.clients[network]
	if !ok {
		return false, fmt.Errorf("network %s not found", network)
	}

	sourceChainIDBig, ok := new(big.Int).SetString(sourceChainID, 10)
	if !ok {
		return false, fmt.Errorf("invalid source chain ID: %s", sourceChainID)
	}

	intentIDBig, ok := new(big.Int).SetString(intentID, 10)
	if !ok {
		return false, fmt.Errorf("invalid intent ID: %s", intentID)
	}

	solved, err := client.VerifyIntentSolved(ctx, sourceChainIDBig, intentIDBig)
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

		solved, verifyErr = r.VerifyIntentSolved(ctx, destNetwork, req.Network, req.Chain2IntentID)
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

	settleTxHash := tx.Hash().Hex()

	log.WithFields(log.Fields{
		"intentID":      req.IntentID,
		"settleTxHash":  settleTxHash,
		"depositTxHash": req.DepositTxHash,
		"solveTxHash":   req.SolveTxHash,
	}).Info("Returning all transaction hashes to solver")

	intentKey := fmt.Sprintf("%s-%s", req.Network, req.IntentID)
	intentDetails := &types.IntentDetails{
		IntentID:      req.IntentID,
		SourceChainID: req.Network,
		DestChainID:   destChainIDStr,
		SolverAddress: req.SolverAddress,
		DepositTxHash: req.DepositTxHash,
		SolveTxHash:   req.SolveTxHash,
		SettleTxHash:  settleTxHash,
		Status:        "settled",
		Timestamp:     time.Now(),
		SettledAt:     time.Now(),
	}

	r.mu.Lock()
	r.intentDetailsStore[intentKey] = intentDetails
	r.mu.Unlock()

	// Persist to database
	if err := r.store.SaveIntentDetails(req.Network, req.IntentID, intentDetails); err != nil {
		log.WithError(err).Warn("Failed to persist intent details to database")
	}

	log.WithField("intentKey", intentKey).Info("Stored intent details for frontend retrieval")

	return &types.RelayerResponse{
		Success:         true,
		TransactionHash: settleTxHash,
		SettleTxHash:    settleTxHash,
		DepositTxHash:   req.DepositTxHash,
		SolveTxHash:     req.SolveTxHash,
		Network:         req.Network,
		ChainID:         client.GetAddress().Big().Int64(),
		IntentID:        req.IntentID,
		Chain2IntentID:  req.Chain2IntentID,
		SolverAddress:   req.SolverAddress,
	}, nil
}

func (r *Relayer) getDestinationNetwork(sourceNetworkKey string) string {
	sourceChainCfg, ok := r.cfg.Chains[sourceNetworkKey]
	if !ok {
		log.WithField("sourceNetworkKey", sourceNetworkKey).Warn("Source network not found in config")
		return ""
	}

	if sourceChainCfg.Name == "horizen" {
		for chainID, cfg := range r.cfg.Chains {
			if cfg.Name == "base" {
				return chainID
			}
		}
	} else if sourceChainCfg.Name == "base" {
		for chainID, cfg := range r.cfg.Chains {
			if cfg.Name == "horizen" {
				return chainID
			}
		}
	} else if sourceChainCfg.Name == "sepolia" {
		for chainID, cfg := range r.cfg.Chains {
			if cfg.Name == "horizen" {
				return chainID
			}
		}
	}

	for chainID := range r.cfg.Chains {
		if chainID != sourceNetworkKey {
			return chainID
		}
	}

	return ""
}

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
	solverInfo := &types.SolverInfo{
		Address:      req.SolverAddress,
		Routes:       req.Routes,
		Balances:     balances,
		RegisteredAt: now,
		LastSeen:     now,
	}

	r.registeredSolvers[req.SolverAddress] = solverInfo

	// Persist to database
	if err := r.store.SaveSolverInfo(req.SolverAddress, solverInfo); err != nil {
		log.WithError(err).Warn("Failed to persist solver info to database")
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

func (r *Relayer) getClientByName(chainName string) (*blockchain.Client, error) {
	for chainID, chainCfg := range r.cfg.Chains {
		if chainCfg.Name == chainName {
			if client, ok := r.clients[chainID]; ok {
				return client, nil
			}
		}
	}
	return nil, fmt.Errorf("chain %s not found", chainName)
}

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

// GetIntentDetails retrieves stored intent details including all transaction hashes
func (r *Relayer) GetIntentDetails(sourceChainID, intentID string) (*types.IntentDetails, error) {
	intentKey := fmt.Sprintf("%s-%s", sourceChainID, intentID)

	// Check in-memory cache first
	r.mu.RLock()
	details, ok := r.intentDetailsStore[intentKey]
	r.mu.RUnlock()

	if ok {
		return details, nil
	}

	// Try loading from database
	details, err := r.store.GetIntentDetails(sourceChainID, intentID)
	if err != nil {
		return nil, fmt.Errorf("failed to load intent details from database: %w", err)
	}

	if details != nil {
		// Cache in memory
		r.mu.Lock()
		r.intentDetailsStore[intentKey] = details
		r.mu.Unlock()
		return details, nil
	}

	return nil, fmt.Errorf("intent details not found for intentID=%s on chain=%s", intentID, sourceChainID)
}

func (r *Relayer) Close() {
	for _, client := range r.clients {
		client.Close()
	}

	// Close database
	if r.store != nil {
		if err := r.store.Close(); err != nil {
			log.WithError(err).Error("Failed to close database")
		} else {
			log.Info("Database closed successfully")
		}
	}
}
