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
		bidWorkerPools[networkKey] = make(chan bidJob, 50)
		log.WithField("network", networkKey).Info("Blockchain client initialized")
	}

	chainPairMap := make(map[string]string)
	for _, pair := range cfg.ChainPairs {
		chainPairMap[pair.SourceChain.Name] = pair.DestChain.Name
		chainPairMap[pair.DestChain.Name] = pair.SourceChain.Name
	}

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
		"pairCount": len(cfg.ChainPairs),
		"networks":  len(clients),
	}).Info("Solver initialized with chain pairs")

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

	for networkKey := range s.cfg.Networks {
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

	routes := []types.Route{}
	for _, pair := range s.cfg.ChainPairs {
		route := types.Route{
			SourceChain: pair.SourceChain.Name,
			DestChain:   pair.DestChain.Name,
			TokenA:      "",
			TokenB:      "",
		}
		routes = append(routes, route)
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

	s.mu.Lock()
	s.lastProcessedBlock[networkKey] = currentBlock
	s.mu.Unlock()

	log.WithFields(log.Fields{
		"network":     networkKey,
		"startBlock": currentBlock,
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

	s.mu.Lock()
	s.lastProcessedBlock[networkKey] = currentBlock
	s.mu.Unlock()

	return nil
}

// handleNewIntent handles a new intent event
func (s *Solver) handleNewIntent(ctx context.Context, networkKey string, intentID *big.Int) {
	client := s.clients[networkKey]

	intent, err := client.GetIntent(ctx, intentID)
	if err != nil {
		log.WithError(err).WithField("intentID", intentID).Error("Failed to get intent")
		return
	}

	if intent.Completed {
		log.WithField("intentID", intentID).Debug("Intent already completed")
		return
	}

	profitable, bidAmount := s.calculateProfitableBid(ctx, networkKey, intent)
	if !profitable {
		log.WithFields(log.Fields{
			"intentID": intentID,
			"amountA":  intent.AmountA,
			"reward":   intent.Reward,
		}).Info("Intent not profitable, skipping")
		return
	}

	key := fmt.Sprintf("%s-%s", networkKey, intentID.String())
	s.mu.Lock()
	s.activeIntents[key] = &types.IntentStatus{
		IntentID:    intentID.String(),
		Network:     networkKey,
		Status:      "bidding",
		OurBid:      bidAmount.String(),
		LastUpdated: time.Now(),
	}
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

	twoTokens := new(big.Float).SetInt(new(big.Int).Mul(big.NewInt(2), big.NewInt(1e18)))
	targetBidFloat = new(big.Float).Sub(targetBidFloat, twoTokens)

	targetBid, _ := targetBidFloat.Int(nil)

	log.WithFields(log.Fields{
		"minBid":       minBid.String(),
		"profitMargin": s.cfg.SolverConfig.MinProfitMargin,
		"targetBid":    targetBid.String(),
	}).Debug("Calculated bid amount")

	client := s.clients[networkKey]
	tokenAAddr := common.HexToAddress(intent.TokenA)
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

// startBiddingLoop starts the bidding loop for an intent
func (s *Solver) startBiddingLoop(ctx context.Context, networkKey string, intentID *big.Int, ourBid *big.Int) {
	client := s.clients[networkKey]
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

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

					intent, err = client.GetIntent(ctx, intentID)
					if err != nil {
						log.WithError(err).Warn("Failed to get intent after finalization, will retry")
						time.Sleep(2 * time.Second)
						continue
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
						"intentID": intentID,
						"winner":   intent.WinningSolver,
						"ourAddr":  client.GetAddress().Hex(),
					}).Debug("Auction not won")
				}
				return
			}

			highestBid, highestBidder, err := client.GetHighestBid(ctx, intentID)
			if err != nil {
				log.WithError(err).Error("Failed to get highest bid")
				continue
			}

			if !hasPlacedBid && (highestBid.Cmp(big.NewInt(0)) == 0 || ourBid.Cmp(highestBid) > 0) {
				select {
				case s.bidWorkerPools[networkKey] <- bidJob{
					ctx:        ctx,
					client:     client,
					intentID:   intentID,
					bidAmount:  ourBid,
					networkKey: networkKey,
				}:
					hasPlacedBid = true
					log.WithFields(log.Fields{
						"intentID":  intentID,
						"bidAmount": ourBid.String(),
						"network":   networkKey,
					}).Debug("Bid submitted to worker pool")
				case <-time.After(time.Second):
					log.WithFields(log.Fields{
						"intentID": intentID,
						"network":  networkKey,
					}).Warn("Bid worker pool full, skipping bid")
				}
			} else if highestBid.Cmp(big.NewInt(0)) > 0 && highestBidder != client.GetAddress() {
				log.WithFields(log.Fields{
					"intentID":    intentID,
					"ourBid":      ourBid,
					"highestBid":  highestBid,
					"highestBidder": highestBidder.Hex(),
				}).Debug("Outbid by another solver")
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
	if err != nil {
		log.WithError(err).Error("Failed to deposit and pickup after retries")
		return
	}

	_, err = client.WaitForTransaction(ctx, tx)
	if err != nil {
		log.WithError(err).Error("Failed to confirm deposit transaction")
		return
	}

	key := fmt.Sprintf("%s-%s", networkKey, intentID.String())
	s.mu.Lock()
	if status, ok := s.activeIntents[key]; ok {
		status.IsWinner = true
		status.Deposited = true
		status.Status = "deposited"
		status.LastUpdated = time.Now()
	}
	s.mu.Unlock()

	go s.solveIntentOnDestinationChain(ctx, networkKey, intentID)
}

// solveIntentOnDestinationChain solves the intent on the destination network.
// Determines destination chain using the chain pair mapping and executes solve transaction.
func (s *Solver) solveIntentOnDestinationChain(ctx context.Context, sourceNetwork string, intentID *big.Int) {
	destNetwork, ok := s.chainPairMap[sourceNetwork]
	if !ok {
		log.WithFields(log.Fields{
			"sourceNetwork": sourceNetwork,
			"intentID":      intentID,
		}).Error("No destination chain configured for source network")
		return
	}

	sourceClient := s.clients[sourceNetwork]
	intent, err := sourceClient.GetIntent(ctx, intentID)
	if err != nil {
		log.WithError(err).Error("Failed to get intent details from source chain")
		return
	}

	recipients, amounts, err := s.getRecipientsFromRelayer(intentID.String())
	if err != nil {
		log.WithError(err).Error("Failed to get recipients from relayer")
		return
	}

	log.WithFields(log.Fields{
		"intentID":       intentID,
		"recipientCount": len(recipients),
	}).Debug("Retrieved recipients from relayer")

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

	destClient := s.clients[destNetwork]

	tokenBAddr := common.HexToAddress(intent.TokenB)
	log.WithFields(log.Fields{
		"intentID":    intentID,
		"tokenB":      intent.TokenB,
		"totalAmount": totalAmount.String(),
	}).Debug("Approving tokenB for solving on destination chain")

	s.txMutexes[destNetwork].Lock()
	defer s.txMutexes[destNetwork].Unlock()

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

	tx, err := s.retryTransaction(ctx, destNetwork, "solve-intent", func() (*ethtypes.Transaction, error) {
		return destClient.SolveIntentOnChain2(ctx, intentID, userAddr, tokenBAddr, expectedAmountB, recipientAddrs, amountBigInts)
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

	log.WithFields(log.Fields{
		"intentID":    intentID,
		"destNetwork": destNetwork,
		"txHash":      tx.Hash().Hex(),
	}).Info("Intent solved on destination chain and confirmed")

	go s.settleIntent(ctx, sourceNetwork, intentID, intentID, destClient.GetAddress())
}

// getRecipientsFromRelayer fetches recipient information from the relayer.
// Includes solver address in request header for authorization.
func (s *Solver) getRecipientsFromRelayer(intentID string) ([]string, []string, error) {
	url := fmt.Sprintf("%s/get-recipients/%s", s.cfg.RelayerURL, intentID)

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
func (s *Solver) settleIntent(ctx context.Context, network string, chain1IntentID, chain2IntentID *big.Int, solverAddr common.Address) {
	settleReq := types.SettleRequest{
		IntentID:         chain1IntentID.String(),
		Network:          network,
		Chain2IntentID:   chain2IntentID.String(),
		SolverAddress:    solverAddr.Hex(),
		DestinationChain: "base",
	}

	body, err := json.Marshal(settleReq)
	if err != nil {
		log.WithError(err).Error("Failed to marshal settle request")
		return
	}

	url := fmt.Sprintf("%s/settle", s.cfg.RelayerURL)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.WithError(err).Error("Failed to call settle endpoint")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{
			"status":   resp.StatusCode,
			"response": string(bodyBytes),
		}).Error("Settle request failed")
		return
	}

	var settleResp types.RelayerResponse
	if err := json.NewDecoder(resp.Body).Decode(&settleResp); err != nil {
		log.WithError(err).Error("Failed to decode settle response")
		return
	}

	if settleResp.Success {
		log.WithFields(log.Fields{
			"intentID": chain1IntentID,
			"txHash":   settleResp.TransactionHash,
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
	}
}

func (s *Solver) Close() {
	for _, client := range s.clients {
		client.Close()
	}
}
