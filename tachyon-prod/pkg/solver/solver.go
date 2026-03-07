package solver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
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

type bidJob struct {
	ctx        context.Context
	client     *blockchain.Client
	intentID   *big.Int
	bidAmount  *big.Int
	networkKey string
}

// Solver manages the intent solving process
type Solver struct {
	cfg                *config.Config
	clients            map[string]*blockchain.Client
	activeIntents      map[string]*types.IntentStatus
	lastProcessedBlock map[string]*big.Int
	isRunning          bool
	mu                 sync.RWMutex
	stopChan           chan struct{}
	bidWorkerPools     map[string]chan bidJob
	bidWorkerWg        sync.WaitGroup
	txMutexes          map[string]*sync.Mutex
	handlingWins       map[string]bool
	chainPairMap       map[string]string
}

// NewSolver creates a new solver instance
func NewSolver(cfg *config.Config) (*Solver, error) {
	clients := make(map[string]*blockchain.Client)
	txMutexes := make(map[string]*sync.Mutex)
	bidWorkerPools := make(map[string]chan bidJob)

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
		bidWorkerPools[chainID] = make(chan bidJob, 50)
		log.WithFields(log.Fields{
			"chain":   chainCfg.Name,
			"chainID": chainID,
		}).Info("Blockchain client initialized")
	}

	// No need for pair map - we use destChainId from intents dynamically
	chainPairMap := make(map[string]string)

	solver := &Solver{
		cfg:                cfg,
		clients:            clients,
		activeIntents:      make(map[string]*types.IntentStatus),
		lastProcessedBlock: make(map[string]*big.Int),
		stopChan:           make(chan struct{}),
		bidWorkerPools:     bidWorkerPools,
		txMutexes:          txMutexes,
		handlingWins:       make(map[string]bool),
		chainPairMap:       chainPairMap,
	}

	for networkKey, workerPool := range bidWorkerPools {
		solver.bidWorkerWg.Add(1)
		go solver.bidWorker(networkKey, workerPool)
	}

	log.WithFields(log.Fields{
		"chainCount": len(cfg.Chains),
		"clients":    len(clients),
	}).Info("Solver initialized with chain-based configuration")

	return solver, nil
}

// Start starts the solver event polling
func (s *Solver) Start() error {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return fmt.Errorf("solver is already running")
	}
	s.isRunning = true
	s.stopChan = make(chan struct{})
	s.mu.Unlock()

	log.Info("Starting solver...")

	if err := s.RegisterWithRelayer(); err != nil {
		log.WithError(err).Warn("Failed to register with relayer, will continue without registration")
	}

	for networkKey := range s.cfg.Chains {
		go s.pollEvents(networkKey)
	}

	return nil
}

// RegisterWithRelayer registers this solver's routes with the relayer
func (s *Solver) RegisterWithRelayer() error {
	var solverAddr string
	for _, client := range s.clients {
		solverAddr = client.GetAddress().Hex()
		break
	}

	// Generate all possible routes (every chain can be source or destination)
	routes := []types.Route{}
	chainIDs := make([]string, 0, len(s.cfg.Chains))
	for chainID := range s.cfg.Chains {
		chainIDs = append(chainIDs, chainID)
	}

	// Create bidirectional routes between all chain pairs
	for i, sourceChainID := range chainIDs {
		sourceCfg := s.cfg.Chains[sourceChainID]
		for j, destChainID := range chainIDs {
			if i != j { // Don't create route to same chain
				destCfg := s.cfg.Chains[destChainID]
				route := types.Route{
					SourceChain: sourceCfg.Name,
					DestChain:   destCfg.Name,
					TokenA:      "", // Support all tokens
					TokenB:      "",
				}
				routes = append(routes, route)
			}
		}
	}

	req := types.RegisterSolverRequest{
		SolverAddress: solverAddr,
		Routes:        routes,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal registration request: %w", err)
	}

	url := fmt.Sprintf("%s/register-solver", s.cfg.RelayerURL)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to send registration request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var response types.RegisterSolverResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !response.Success {
		return fmt.Errorf("registration failed: %s", response.Error)
	}

	log.WithFields(log.Fields{
		"solver":       solverAddr,
		"routesCount":  response.RoutesCount,
		"balanceCount": len(response.Balances),
	}).Info("Successfully registered with relayer")

	for _, balance := range response.Balances {
		log.WithFields(log.Fields{
			"chain":   balance.Chain,
			"token":   balance.Token,
			"balance": balance.Balance,
		}).Debug("Token balance checked")
	}

	return nil
}

// Stop stops the solver
func (s *Solver) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return
	}

	log.Info("Stopping solver...")
	close(s.stopChan)

	for networkKey, workerPool := range s.bidWorkerPools {
		close(workerPool)
		log.WithField("network", networkKey).Debug("Closed bid worker pool")
	}

	s.isRunning = false

	go func() {
		s.bidWorkerWg.Wait()
		log.Info("All bid workers stopped")
	}()
}

func (s *Solver) bidWorker(networkKey string, workerPool chan bidJob) {
	defer s.bidWorkerWg.Done()

	log.WithField("network", networkKey).Debug("Bid worker started")

	for job := range workerPool {
		s.txMutexes[job.networkKey].Lock()

		tx, err := s.retryTransaction(job.ctx, job.networkKey, "place-bid", func() (*ethtypes.Transaction, error) {
			return job.client.PlaceBid(job.ctx, job.intentID, job.bidAmount)
		})

		s.txMutexes[job.networkKey].Unlock()

		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"intentID": job.intentID,
				"network":  job.networkKey,
			}).Error("Failed to place bid after retries")
			continue
		}

		log.WithFields(log.Fields{
			"intentID":  job.intentID,
			"bidAmount": job.bidAmount.String(),
			"network":   job.networkKey,
			"txHash":    tx.Hash().Hex(),
		}).Info("Bid placed successfully")
	}

	log.WithField("network", networkKey).Debug("Bid worker stopped")
}

// IsRunning returns whether the solver is currently running
func (s *Solver) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// GetActiveIntents returns the current active intents
func (s *Solver) GetActiveIntents() map[string]types.IntentStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]types.IntentStatus)
	for k, v := range s.activeIntents {
		result[k] = *v
	}
	return result
}

// GetIntentStatus returns the status of a specific intent
func (s *Solver) GetIntentStatus(intentID, network string) (*types.IntentStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := fmt.Sprintf("%s-%s", network, intentID)
	status, ok := s.activeIntents[key]
	if !ok {
		return nil, fmt.Errorf("intent not found")
	}

	return status, nil
}

// GetWalletAddress returns the solver's wallet address
func (s *Solver) GetWalletAddress() string {
	for _, client := range s.clients {
		return client.GetAddress().Hex()
	}
	return ""
}

// GetBalances returns token balances for all networks
func (s *Solver) GetBalances(ctx context.Context) map[string]string {
	balances := make(map[string]string)
	for networkKey, client := range s.clients {
		balance, err := client.GetBalance(ctx, common.Address{})
		if err != nil {
			log.WithError(err).WithField("network", networkKey).Error("Failed to get balance")
			balances[networkKey] = "0"
			continue
		}
		balances[networkKey] = balance.String()
	}
	return balances
}

// pollEvents polls for blockchain events
func (s *Solver) pollEvents(networkKey string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	client := s.clients[networkKey]
	ctx := context.Background()

	currentBlock, err := client.GetCurrentBlock(ctx)
	if err != nil {
		log.WithError(err).WithField("network", networkKey).Error("Failed to get current block")
		return
	}

	// Start from current block (no lookback)
	s.mu.Lock()
	s.lastProcessedBlock[networkKey] = new(big.Int).Set(currentBlock)
	s.mu.Unlock()

	log.WithFields(log.Fields{
		"network":      networkKey,
		"currentBlock": currentBlock,
	}).Info("Starting event polling")

	for {
		select {
		case <-s.stopChan:
			log.WithField("network", networkKey).Info("Stopping event polling")
			return
		case <-ticker.C:
			if err := s.processEvents(ctx, networkKey); err != nil {
				log.WithError(err).WithField("network", networkKey).Error("Failed to process events")
			}
		}
	}
}

// processEvents processes new blockchain events
func (s *Solver) processEvents(ctx context.Context, networkKey string) error {
	client := s.clients[networkKey]

	currentBlock, err := client.GetCurrentBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current block: %w", err)
	}

	s.mu.RLock()
	fromBlock := s.lastProcessedBlock[networkKey]
	s.mu.RUnlock()

	if fromBlock == nil {
		fromBlock = currentBlock
	}

	if fromBlock.Cmp(currentBlock) >= 0 {
		return nil
	}

	logs, err := client.GetLogs(ctx, fromBlock, currentBlock, []string{"IntentCreated"})
	if err != nil {
		return fmt.Errorf("failed to get logs: %w", err)
	}

	log.WithFields(log.Fields{
		"network":   networkKey,
		"fromBlock": fromBlock,
		"toBlock":   currentBlock,
		"events":    len(logs),
	}).Debug("Processed blocks")

	for _, eventLog := range logs {
		intentID, err := client.ParseIntentCreatedEvent(eventLog)
		if err != nil {
			log.WithError(err).Error("Failed to parse IntentCreated event")
			continue
		}

		log.WithFields(log.Fields{
			"network":  networkKey,
			"intentID": intentID,
		}).Info("New intent detected")

		go s.handleNewIntent(ctx, networkKey, intentID)
	}

	// Update last processed block with write lock
	s.mu.Lock()
	s.lastProcessedBlock[networkKey] = new(big.Int).Set(currentBlock)
	s.mu.Unlock()

	return nil
}

// handleNewIntent handles a new intent event
func (s *Solver) handleNewIntent(ctx context.Context, networkKey string, intentID *big.Int) {
	client := s.clients[networkKey]

	key := fmt.Sprintf("%s-%s", networkKey, intentID.String())

	s.mu.Lock()
	if _, exists := s.activeIntents[key]; exists {
		s.mu.Unlock()
		log.WithFields(log.Fields{
			"intentID": intentID,
			"network":  networkKey,
		}).Debug("Intent already being processed, skipping duplicate")
		return
	}
	s.activeIntents[key] = &types.IntentStatus{
		IntentID:    intentID.String(),
		Network:     networkKey,
		Status:      "evaluating",
		LastUpdated: time.Now(),
	}
	s.mu.Unlock()

	intent, err := client.GetIntent(ctx, intentID)
	if err != nil {
		log.WithError(err).WithField("intentID", intentID).Error("Failed to get intent")
		s.mu.Lock()
		delete(s.activeIntents, key)
		s.mu.Unlock()
		return
	}

	if intent.Completed {
		log.WithField("intentID", intentID).Debug("Intent already completed")
		s.mu.Lock()
		delete(s.activeIntents, key)
		s.mu.Unlock()
		return
	}

	profitable, bidAmount := s.calculateProfitableBid(ctx, networkKey, intent)
	if !profitable {
		log.WithFields(log.Fields{
			"intentID": intentID,
			"amountA":  intent.AmountA,
			"reward":   intent.Reward,
		}).Info("Intent not profitable, skipping")
		s.mu.Lock()
		delete(s.activeIntents, key)
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	s.activeIntents[key].Status = "bidding"
	s.activeIntents[key].OurBid = bidAmount.String()
	s.activeIntents[key].LastUpdated = time.Now()
	s.mu.Unlock()

	go s.startBiddingLoop(ctx, networkKey, intentID, bidAmount)
}

// calculateProfitableBid calculates a profitable bid amount.
// minBid = amountA + reward. Profit margin and balance buffer are applied to calculate target bid.
func (s *Solver) calculateProfitableBid(ctx context.Context, networkKey string, intent *types.Intent) (bool, *big.Int) {
	minBid := new(big.Int).Add(intent.AmountA, intent.Reward)

	profitMargin := new(big.Float).SetFloat64(s.cfg.SolverConfig.MinProfitMargin)
	minBidFloat := new(big.Float).SetInt(minBid)
	marginAmount := new(big.Float).Mul(minBidFloat, profitMargin)

	targetBidFloat := new(big.Float).Sub(minBidFloat, marginAmount)

	// Get token decimals to calculate appropriate gas buffer
	client := s.clients[networkKey]
	tokenAAddr := common.HexToAddress(intent.TokenA)
	decimals, err := client.GetTokenDecimals(ctx, tokenAAddr)
	if err != nil {
		log.WithError(err).Warn("Failed to get token decimals, defaulting to 18")
		decimals = 18
	}

	// Subtract a small gas buffer (e.g., 2 tokens) based on actual token decimals
	gasBuffer := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	gasBuffer = new(big.Int).Mul(gasBuffer, big.NewInt(2)) // 2 tokens
	gasBufferFloat := new(big.Float).SetInt(gasBuffer)
	targetBidFloat = new(big.Float).Sub(targetBidFloat, gasBufferFloat)

	targetBid, _ := targetBidFloat.Int(nil)

	// Ensure bid is not negative (which would wrap around to MAX_UINT256)
	if targetBid.Sign() <= 0 {
		log.WithFields(log.Fields{
			"minBid":       minBid.String(),
			"profitMargin": s.cfg.SolverConfig.MinProfitMargin,
			"decimals":     decimals,
			"gasBuffer":    gasBuffer.String(),
			"targetBid":    targetBid.String(),
		}).Info("Calculated bid is negative or zero, not profitable")
		return false, nil
	}

	log.WithFields(log.Fields{
		"minBid":       minBid.String(),
		"profitMargin": s.cfg.SolverConfig.MinProfitMargin,
		"decimals":     decimals,
		"gasBuffer":    gasBuffer.String(),
		"targetBid":    targetBid.String(),
	}).Debug("Calculated bid amount")
	balance, err := client.GetBalance(ctx, tokenAAddr)
	if err != nil {
		log.WithError(err).Error("Failed to get TokenA balance")
		return false, nil
	}

	bufferFloat := new(big.Float).SetFloat64(1.0 - s.cfg.SolverConfig.BalanceBuffer)
	balanceFloat := new(big.Float).SetInt(balance)
	maxAffordable := new(big.Float).Mul(balanceFloat, bufferFloat)
	maxAffordableInt, _ := maxAffordable.Int(nil)

	if targetBid.Cmp(maxAffordableInt) > 0 {
		log.WithFields(log.Fields{
			"targetBid":     targetBid.String(),
			"maxAffordable": maxAffordableInt.String(),
			"balance":       balance.String(),
		}).Info("Cannot afford this intent")
		return false, nil
	}

	if targetBid.Cmp(s.cfg.SolverConfig.MaxBidAmount) > 0 {
		log.WithFields(log.Fields{
			"targetBid":    targetBid.String(),
			"maxBidAmount": s.cfg.SolverConfig.MaxBidAmount.String(),
		}).Info("Bid exceeds max bid amount")
		return false, nil
	}

	log.WithField("targetBid", targetBid.String()).Info("Intent is profitable, will bid")
	return true, targetBid
}

// startBiddingLoop starts the bidding loop for an intent with competitive re-bidding
func (s *Solver) startBiddingLoop(ctx context.Context, networkKey string, intentID *big.Int, initialBid *big.Int) {
	client := s.clients[networkKey]
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	currentBid := new(big.Int).Set(initialBid)
	hasPlacedBid := false

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			intent, err := client.GetIntent(ctx, intentID)
			if err != nil {
				log.WithError(err).Error("Failed to get intent")
				continue
			}

			if intent.Completed {
				log.WithField("intentID", intentID).Debug("Intent completed")
				return
			}

			currentTime := time.Now().Unix()
			if big.NewInt(currentTime).Cmp(intent.EndTime) > 0 {
				log.WithField("intentID", intentID).Debug("Auction ended")

				if intent.WinningSolver == (common.Address{}.Hex()) {
					log.WithField("intentID", intentID).Debug("Finalizing auction")

					s.txMutexes[networkKey].Lock()

					tx, err := s.retryTransaction(ctx, networkKey, "finalize-auction", func() (*ethtypes.Transaction, error) {
						return client.FinalizeAuction(ctx, intentID)
					})

					if err != nil {
						s.txMutexes[networkKey].Unlock()
						log.WithError(err).Warn("Failed to finalize auction after retries")
						time.Sleep(2 * time.Second)
					} else {
						_, waitErr := client.WaitForTransaction(ctx, tx)
						s.txMutexes[networkKey].Unlock()

						if waitErr != nil {
							log.WithError(waitErr).Warn("Failed to confirm finalization transaction")
							time.Sleep(2 * time.Second)
						} else {
							log.WithField("intentID", intentID).Debug("Auction finalized and confirmed")
						}
					}

					// Wait for blockchain state to propagate, then re-fetch intent
					time.Sleep(3 * time.Second)
					intent, err = client.GetIntent(ctx, intentID)
					if err != nil {
						log.WithError(err).Warn("Failed to get intent after finalization, will retry")
						time.Sleep(2 * time.Second)
						continue
					}
				}

				// If winner is still zero after trying to finalize, wait and retry a few times
				// This handles the case where another party is finalizing the auction
				if intent.WinningSolver == (common.Address{}.Hex()) {
					maxRetries := 5
					for i := 0; i < maxRetries; i++ {
						log.WithFields(log.Fields{
							"intentID": intentID,
							"attempt":  i + 1,
						}).Debug("Winner still not set, waiting for finalization")

						time.Sleep(3 * time.Second)

						intent, err = client.GetIntent(ctx, intentID)
						if err != nil {
							log.WithError(err).Warn("Failed to get intent while waiting for finalization")
							continue
						}

						if intent.WinningSolver != (common.Address{}.Hex()) {
							log.WithField("intentID", intentID).Debug("Winner set after waiting")
							break
						}
					}
				}

				if intent.WinningSolver == client.GetAddress().Hex() {
					key := fmt.Sprintf("%s-%s", networkKey, intentID.String())

					s.mu.Lock()
					alreadyHandling := s.handlingWins[key]
					if !alreadyHandling {
						s.handlingWins[key] = true
					}
					s.mu.Unlock()

					if !alreadyHandling {
						log.WithFields(log.Fields{
							"intentID": intentID,
							"winner":   intent.WinningSolver,
						}).Info("Auction won by solver")
						go s.handleAuctionWon(ctx, networkKey, intentID)
					}
				} else {
					log.WithFields(log.Fields{
						"intentID":     intentID,
						"winner":       intent.WinningSolver,
						"ourAddr":      client.GetAddress().Hex(),
						"winningBid":   intent.WinningBid.String(),
						"ourBid":       currentBid.String(),
						"hasPlacedBid": hasPlacedBid,
					}).Info("Auction not won")
				}
				return
			}

			highestBid, highestBidder, err := client.GetHighestBid(ctx, intentID)
			if err != nil {
				log.WithError(err).Error("Failed to get highest bid")
				continue
			}

			shouldBid := false
			isWinning := highestBidder == client.GetAddress()

			if highestBid.Cmp(big.NewInt(0)) == 0 {
				shouldBid = true
			} else if !isWinning {
				newBid := new(big.Int).Add(highestBid, s.cfg.SolverConfig.BidIncrement)

				minBid := new(big.Int).Add(intent.AmountA, intent.Reward)
				profitMargin := new(big.Float).SetFloat64(s.cfg.SolverConfig.MinProfitMargin)
				minBidFloat := new(big.Float).SetInt(minBid)
				marginAmount := new(big.Float).Mul(minBidFloat, profitMargin)
				minProfitableBidFloat := new(big.Float).Sub(minBidFloat, marginAmount)
				minProfitableBid, _ := minProfitableBidFloat.Int(nil)

				if newBid.Cmp(minProfitableBid) >= 0 && newBid.Cmp(s.cfg.SolverConfig.MaxBidAmount) <= 0 {
					currentBid = newBid
					shouldBid = true
					log.WithFields(log.Fields{
						"intentID":      intentID,
						"newBid":        newBid.String(),
						"highestBid":    highestBid.String(),
						"minProfitable": minProfitableBid.String(),
					}).Info("Calculated competitive counter-bid")
				} else {
					log.WithFields(log.Fields{
						"intentID":      intentID,
						"wouldBid":      newBid.String(),
						"highestBid":    highestBid.String(),
						"minProfitable": minProfitableBid.String(),
						"reason":        "not profitable or exceeds max",
					}).Info("Cannot outbid profitably, giving up")
					return
				}
			}

			if shouldBid {
				select {
				case s.bidWorkerPools[networkKey] <- bidJob{
					ctx:        ctx,
					client:     client,
					intentID:   intentID,
					bidAmount:  currentBid,
					networkKey: networkKey,
				}:
					hasPlacedBid = true
					log.WithFields(log.Fields{
						"intentID":  intentID,
						"bidAmount": currentBid.String(),
						"network":   networkKey,
					}).Info("Bid submitted to worker pool")
				case <-time.After(time.Second):
					log.WithFields(log.Fields{
						"intentID": intentID,
						"network":  networkKey,
					}).Warn("Bid worker pool full, will retry next cycle")
				}
			}
		}
	}
}

// retryTransaction retries a transaction function up to maxRetries times on nonce errors
func (s *Solver) retryTransaction(ctx context.Context, networkKey string, operation string, txFunc func() (*ethtypes.Transaction, error)) (*ethtypes.Transaction, error) {
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

// handleAuctionWon handles the case when we win an auction
func (s *Solver) handleAuctionWon(ctx context.Context, networkKey string, intentID *big.Int) {
	client := s.clients[networkKey]

	intent, err := client.GetIntent(ctx, intentID)
	if err != nil {
		log.WithError(err).Error("Failed to get intent details")
		return
	}

	log.WithFields(log.Fields{
		"network":    networkKey,
		"intentID":   intentID,
		"bidAmount":  intent.WinningBid,
		"tokenA":     intent.TokenA,
	}).Info("Won auction, approving tokens for deposit")

	tokenAddr := common.HexToAddress(intent.TokenA)
	bidAmount := intent.WinningBid

	s.txMutexes[networkKey].Lock()
	defer s.txMutexes[networkKey].Unlock()

	approveTx, err := s.retryTransaction(ctx, networkKey, "approve-tokens", func() (*ethtypes.Transaction, error) {
		return client.ApproveToken(ctx, tokenAddr, bidAmount)
	})
	if err != nil {
		log.WithError(err).Error("Failed to approve tokens after retries")
		return
	}

	_, err = client.WaitForTransaction(ctx, approveTx)
	if err != nil {
		log.WithError(err).Error("Failed to confirm token approval")
		return
	}

	log.WithField("intentID", intentID).Debug("Tokens approved, depositing")

	tx, err := s.retryTransaction(ctx, networkKey, "deposit-and-pickup", func() (*ethtypes.Transaction, error) {
		return client.DepositAndPickup(ctx, intentID)
	})

	var depositTxHash string

	if err != nil {
		// Check if deposit already went through on-chain (e.g. from a prior retry)
		intent, checkErr := client.GetIntent(ctx, intentID)
		if checkErr == nil && intent.Deposited {
			log.WithField("intentID", intentID).Warn("Deposit tx failed but intent already deposited on-chain, proceeding")
			depositTxHash = "already-deposited"
		} else {
			log.WithError(err).Error("Failed to deposit and pickup after retries")
			return
		}
	} else {
		_, waitErr := client.WaitForTransaction(ctx, tx)
		if waitErr != nil {
			// Tx reverted — check if deposit already went through from an earlier attempt
			intent, checkErr := client.GetIntent(ctx, intentID)
			if checkErr == nil && intent.Deposited {
				log.WithField("intentID", intentID).Warn("Deposit tx reverted but intent already deposited on-chain, proceeding")
				depositTxHash = tx.Hash().Hex()
			} else {
				log.WithError(waitErr).Error("Failed to confirm deposit transaction")
				return
			}
		} else {
			depositTxHash = tx.Hash().Hex()
		}
	}

	key := fmt.Sprintf("%s-%s", networkKey, intentID.String())
	s.mu.Lock()
	if status, ok := s.activeIntents[key]; ok {
		status.IsWinner = true
		status.Deposited = true
		status.Status = "deposited"
		status.DepositTxHash = depositTxHash
		status.LastUpdated = time.Now()
	}
	s.mu.Unlock()

	log.WithFields(log.Fields{
		"intentID":      intentID,
		"depositTxHash": depositTxHash,
	}).Info("Deposit confirmed and tx hash stored")

	go s.solveIntentOnDestinationChain(ctx, networkKey, intentID)
}

// solveIntentOnDestinationChain solves the intent on the destination network.
// Uses the destChainId from the intent to determine destination chain.
func (s *Solver) solveIntentOnDestinationChain(ctx context.Context, sourceNetwork string, intentID *big.Int) {
	sourceClient := s.clients[sourceNetwork]
	intent, err := sourceClient.GetIntent(ctx, intentID)
	if err != nil {
		log.WithError(err).Error("Failed to get intent details from source chain")
		return
	}

	// Use destChainId from intent to find destination network
	destChainID := intent.DestChainId.String()
	destClient, exists := s.clients[destChainID]
	if !exists {
		log.WithFields(log.Fields{
			"intentID":    intentID,
			"destChainId": destChainID,
		}).Warn("Destination chain not configured for this solver, skipping")
		return
	}

	destChainCfg, err := s.cfg.GetChainByID(intent.DestChainId)
	if err != nil {
		log.WithError(err).Error("Failed to get destination chain config")
		return
	}
	destNetwork := destChainCfg.Name

	// Get source chain config to log chain ID
	sourceChainCfg, err := s.cfg.GetChainByID(s.cfg.Chains[sourceNetwork].ChainID)
	if err != nil {
		log.WithError(err).Error("Failed to get source chain config")
		return
	}

	log.WithFields(log.Fields{
		"sourceNetwork": sourceNetwork,
		"sourceChainID": sourceChainCfg.ChainID.String(),
		"destNetwork":   destNetwork,
		"destChainID":   destChainID,
		"intentID":      intentID,
		"tokenB":        intent.TokenB,
	}).Info("Solving intent on destination chain")

	// Wait for blockchain state to propagate before first attempt
	// This is critical for slow chains like Sepolia and Base
	log.WithField("intentID", intentID).Debug("Waiting for deposit state to propagate")
	time.Sleep(5 * time.Second)

	// Retry getting recipients with backoff to handle RPC propagation delays
	var recipients []string
	var amounts []string
	var recipientsErr error
	maxRetries := 12
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			// Exponential backoff: 3s, 6s, 9s, 12s, 15s...
			backoff := time.Duration(i) * 3 * time.Second
			log.WithFields(log.Fields{
				"intentID": intentID,
				"attempt":  i + 1,
				"backoff":  backoff,
			}).Info("Retrying get recipients from relayer")
			time.Sleep(backoff)
		}

		recipients, amounts, recipientsErr = s.getRecipientsFromRelayer(intentID.String(), sourceChainCfg.ChainID.String())
		if recipientsErr == nil {
			break
		}

		log.WithFields(log.Fields{
			"intentID": intentID,
			"attempt":  i + 1,
			"error":    recipientsErr.Error(),
		}).Warn("Failed to get recipients, will retry")
	}

	if recipientsErr != nil {
		log.WithError(recipientsErr).Error("Failed to get recipients from relayer after all retries")
		return
	}

	log.WithFields(log.Fields{
		"intentID":       intentID,
		"recipientCount": len(recipients),
	}).Info("Retrieved recipients from relayer")

	recipientAddrs := make([]common.Address, len(recipients))
	amountBigInts := make([]*big.Int, len(amounts))
	totalAmount := big.NewInt(0)
	for i, addr := range recipients {
		recipientAddrs[i] = common.HexToAddress(addr)
		amt, ok := new(big.Int).SetString(amounts[i], 10)
		if !ok {
			log.WithField("amount", amounts[i]).Error("Failed to parse amount")
			return
		}
		amountBigInts[i] = amt
		totalAmount = new(big.Int).Add(totalAmount, amt)
	}

	// destClient already declared earlier in this function, reuse it

	tokenBAddr := common.HexToAddress(intent.TokenB)
	log.WithFields(log.Fields{
		"intentID":    intentID,
		"tokenB":      intent.TokenB,
		"totalAmount": totalAmount.String(),
	}).Debug("Approving tokenB for solving on destination chain")

	s.txMutexes[destChainID].Lock()
	defer s.txMutexes[destChainID].Unlock()

	approveTx, err := s.retryTransaction(ctx, destNetwork, "approve-tokenB", func() (*ethtypes.Transaction, error) {
		return destClient.ApproveToken(ctx, tokenBAddr, totalAmount)
	})
	if err != nil {
		log.WithError(err).Error("Failed to approve tokenB after retries")
		return
	}

	_, err = destClient.WaitForTransaction(ctx, approveTx)
	if err != nil {
		log.WithError(err).Error("Failed to confirm tokenB approval")
		return
	}

	log.WithField("intentID", intentID).Debug("TokenB approved, solving intent")

	userAddr := common.HexToAddress(intent.User)
	expectedAmountB := intent.ExpectedAmountB
	sourceChainID := sourceChainCfg.ChainID

	tx, err := s.retryTransaction(ctx, destNetwork, "solve-intent", func() (*ethtypes.Transaction, error) {
		return destClient.SolveIntentOnChain2(ctx, sourceChainID, intentID, userAddr, tokenBAddr, expectedAmountB, recipientAddrs, amountBigInts)
	})
	if err != nil {
		log.WithError(err).Error("Failed to solve intent on chain2 after retries")
		return
	}

	_, err = destClient.WaitForTransaction(ctx, tx)
	if err != nil {
		log.WithError(err).Error("Failed to confirm solve transaction")
		return
	}

	solveTxHash := tx.Hash().Hex()
	log.WithFields(log.Fields{
		"intentID":    intentID,
		"destNetwork": destNetwork,
		"txHash":      solveTxHash,
	}).Info("Intent solved on destination chain and confirmed")

	// Get deposit tx hash from the intent status
	key := fmt.Sprintf("%s-%s", sourceNetwork, intentID.String())
	s.mu.RLock()
	var depositTxHash string
	if status, ok := s.activeIntents[key]; ok {
		depositTxHash = status.DepositTxHash
	}
	s.mu.RUnlock()

	go s.settleIntent(ctx, sourceNetwork, intentID, intentID, destClient.GetAddress(), depositTxHash, solveTxHash)
}

// getRecipientsFromRelayer fetches recipient information from the relayer.
// Includes solver address in request header for authorization.
// sourceChainID helps disambiguate when the same intent ID exists on multiple chains.
func (s *Solver) getRecipientsFromRelayer(intentID string, sourceChainID string) ([]string, []string, error) {
	url := fmt.Sprintf("%s/get-recipients/%s?sourceChainId=%s", s.cfg.RelayerURL, intentID, sourceChainID)

	var solverAddr string
	for _, client := range s.clients {
		solverAddr = client.GetAddress().Hex()
		break
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Solver-Address", solverAddr)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get recipients: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, nil, fmt.Errorf("access denied: not the winning solver for this intent")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("relayer returned status %d", resp.StatusCode)
	}

	var recipientInfo types.RecipientInfo
	if err := json.NewDecoder(resp.Body).Decode(&recipientInfo); err != nil {
		return nil, nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !recipientInfo.Success {
		return nil, nil, fmt.Errorf("relayer returned error")
	}

	log.WithFields(log.Fields{
		"intentID":       intentID,
		"recipientCount": len(recipientInfo.Recipients),
	}).Debug("Retrieved recipients from relayer")

	return recipientInfo.Recipients, recipientInfo.Amounts, nil
}

// settleIntent calls the relayer to settle an intent on the source chain
// Retries with exponential backoff to handle RPC propagation delays on slow chains like Sepolia
func (s *Solver) settleIntent(ctx context.Context, network string, chain1IntentID, chain2IntentID *big.Int, solverAddr common.Address, depositTxHash, solveTxHash string) {
	settleReq := types.SettleRequest{
		IntentID:         chain1IntentID.String(),
		Network:          network,
		Chain2IntentID:   chain2IntentID.String(),
		SolverAddress:    solverAddr.Hex(),
		DestinationChain: "base",
		DepositTxHash:    depositTxHash,
		SolveTxHash:      solveTxHash,
	}

	log.WithFields(log.Fields{
		"intentID":      chain1IntentID,
		"depositTxHash": depositTxHash,
		"solveTxHash":   solveTxHash,
	}).Info("Sending settle request with transaction hashes")

	body, err := json.Marshal(settleReq)
	if err != nil {
		log.WithError(err).Error("Failed to marshal settle request")
		return
	}

	url := fmt.Sprintf("%s/settle", s.cfg.RelayerURL)

	// Retry settle with exponential backoff (Sepolia and Base can be slow)
	maxRetries := 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 5 * time.Second
			log.WithFields(log.Fields{
				"intentID": chain1IntentID,
				"attempt":  attempt + 1,
				"backoff":  backoff,
			}).Info("Retrying settle request after backoff")
			time.Sleep(backoff)
		}

		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			log.WithFields(log.Fields{
				"intentID": chain1IntentID,
				"attempt":  attempt + 1,
				"error":    err.Error(),
			}).Warn("Failed to call settle endpoint, will retry")
			continue
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			log.WithFields(log.Fields{
				"intentID": chain1IntentID,
				"attempt":  attempt + 1,
				"status":   resp.StatusCode,
				"response": string(bodyBytes),
			}).Warn("Settle request failed, will retry")

			// If it's the last attempt, log as error
			if attempt == maxRetries-1 {
				log.WithFields(log.Fields{
					"intentID": chain1IntentID,
					"status":   resp.StatusCode,
					"response": string(bodyBytes),
				}).Error("Settle request failed after all retries")
			}
			continue
		}

		var settleResp types.RelayerResponse
		if err := json.NewDecoder(resp.Body).Decode(&settleResp); err != nil {
			resp.Body.Close()
			log.WithFields(log.Fields{
				"intentID": chain1IntentID,
				"attempt":  attempt + 1,
				"error":    err.Error(),
			}).Warn("Failed to decode settle response, will retry")
			continue
		}
		resp.Body.Close()

		if settleResp.Success {
			log.WithFields(log.Fields{
				"intentID": chain1IntentID,
				"txHash":   settleResp.TransactionHash,
				"attempts": attempt + 1,
			}).Info("Intent settled successfully")

			// Update status
			key := fmt.Sprintf("%s-%s", network, chain1IntentID.String())
			s.mu.Lock()
			if status, ok := s.activeIntents[key]; ok {
				status.Completed = true
				status.Status = "completed"
				status.LastUpdated = time.Now()
			}
			s.mu.Unlock()
			return
		}

		// If response indicates failure but we got a response, log and retry
		log.WithFields(log.Fields{
			"intentID": chain1IntentID,
			"attempt":  attempt + 1,
			"error":    settleResp.Error,
		}).Warn("Settle returned unsuccessful response, will retry")
	}

	log.WithFields(log.Fields{
		"intentID":   chain1IntentID,
		"maxRetries": maxRetries,
	}).Error("Failed to settle intent after all retries")
}

func (s *Solver) Close() {
	for _, client := range s.clients {
		client.Close()
	}
}
