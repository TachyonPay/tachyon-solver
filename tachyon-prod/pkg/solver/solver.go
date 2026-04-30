package solver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
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
	store              *storage.Store
	activeIntents      map[string]*types.IntentStatus // In-memory cache
	lastProcessedBlock map[string]*big.Int
	isRunning          bool
	mu                 sync.RWMutex
	stopChan           chan struct{}
	bidWorkerPools     map[string]chan bidJob
	bidWorkerWg        sync.WaitGroup
	txMutexes          map[string]*sync.Mutex
	handlingWins       map[string]bool
	chainPairMap       map[string]string
	oracle             *priceOracle
}

// NewSolver creates a new solver instance
func NewSolver(cfg *config.Config) (*Solver, error) {
	clients := make(map[string]*blockchain.Client)
	txMutexes := make(map[string]*sync.Mutex)
	bidWorkerPools := make(map[string]chan bidJob)

	// Initialize PebbleDB storage
	dbPath := filepath.Join(cfg.DBPath, "solver")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	// Initialize clients for each configured chain
	for chainID, chainCfg := range cfg.Chains {
		client, err := blockchain.NewClient(
			chainCfg.RpcURL,
			chainCfg.BridgeAddress,
			cfg.PrivateKey,
			chainCfg.ChainID,
			chainCfg.Name,
			chainCfg.SafeAddress,
			cfg.SafeSignerKeys,
		)
		if err != nil {
			store.Close()
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
		store:              store,
		activeIntents:      make(map[string]*types.IntentStatus),
		lastProcessedBlock: make(map[string]*big.Int),
		stopChan:           make(chan struct{}),
		bidWorkerPools:     bidWorkerPools,
		txMutexes:          txMutexes,
		handlingWins:       make(map[string]bool),
		chainPairMap:       chainPairMap,
		oracle:             newPriceOracle(),
	}

	// Load persisted state from DB
	if err := solver.loadPersistedState(); err != nil {
		log.WithError(err).Warn("Failed to load persisted state, starting fresh")
	}

	for networkKey, workerPool := range bidWorkerPools {
		solver.bidWorkerWg.Add(1)
		go solver.bidWorker(networkKey, workerPool)
	}

	log.WithFields(log.Fields{
		"chainCount": len(cfg.Chains),
		"clients":    len(clients),
		"dbPath":     dbPath,
	}).Info("Solver initialized with chain-based configuration and persistent storage")

	return solver, nil
}

// loadPersistedState loads previously saved state from the database
func (s *Solver) loadPersistedState() error {
	// Load active intents
	intents, err := s.store.GetAllIntentStatuses()
	if err != nil {
		return fmt.Errorf("failed to load intent statuses: %w", err)
	}

	s.mu.Lock()
	for key, status := range intents {
		s.activeIntents[key] = status
	}
	s.mu.Unlock()

	// Load last processed blocks
	for networkKey := range s.cfg.Chains {
		blockNum, err := s.store.GetLastProcessedBlock(networkKey)
		if err != nil {
			log.WithError(err).WithField("network", networkKey).Warn("Failed to load last processed block")
			continue
		}
		if blockNum != nil {
			s.mu.Lock()
			s.lastProcessedBlock[networkKey] = blockNum
			s.mu.Unlock()
			log.WithFields(log.Fields{
				"network": networkKey,
				"block":   blockNum.String(),
			}).Info("Restored last processed block from DB")
		}
	}

	// Load handling wins state
	for key := range s.activeIntents {
		parts := strings.SplitN(key, "-", 2)
		if len(parts) == 2 {
			handling, _ := s.store.IsHandlingWin(parts[0], parts[1])
			if handling {
				s.mu.Lock()
				s.handlingWins[key] = true
				s.mu.Unlock()
			}
		}
	}

	log.WithField("intentCount", len(intents)).Info("Loaded persisted state from DB")
	return nil
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
		solverAddr = client.GetSolverAddress().Hex()
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
			if job.client.HasSafe() {
				return job.client.PlaceBidViaSafe(job.ctx, job.intentID, job.bidAmount)
			}
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

// GetWalletAddress returns the solver's effective on-chain address (Safe if configured, else EOA).
func (s *Solver) GetWalletAddress() string {
	for _, client := range s.clients {
		return client.GetSolverAddress().Hex()
	}
	return ""
}

// Clients exposes the per-chain blockchain clients keyed by chain ID string.
// Used by auxiliary services (rebalancer, monitoring) that need to issue calls
// against the same RPC + Safe wiring the solver already owns.
func (s *Solver) Clients() map[string]*blockchain.Client {
	return s.clients
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

	// Persist to database
	s.persistLastProcessedBlock(networkKey, currentBlock)

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
	status := &types.IntentStatus{
		IntentID:    intentID.String(),
		Network:     networkKey,
		Status:      "evaluating",
		LastUpdated: time.Now(),
	}
	s.activeIntents[key] = status
	s.mu.Unlock()

	// Persist to database
	s.persistIntentStatus(networkKey, intentID.String(), status)

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

	// Record intent as pending in transaction history as soon as it is detected
	s.saveTransactionRecord(intent, networkKey)

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
	statusCopy := *s.activeIntents[key]
	s.mu.Unlock()

	// Persist to database
	s.persistIntentStatus(networkKey, intentID.String(), &statusCopy)

	go s.startBiddingLoop(ctx, networkKey, intentID, bidAmount)
}

// calculateProfitableBid calculates a profitable bid amount.
//
// The bid amount is set to (amountA + reward) * (1 - profitMargin). On top of
// the raw margin check the function fetches live USD prices for TokenA and
// TokenB from four public APIs (CoinGecko, CoinCap, CryptoCompare, Binance)
// and verifies that the solver's net gain in TokenA — converted to USD — exceeds
// the cost of providing expectedAmountB of TokenB on the destination chain.
// If price data is unavailable from at least two sources the check is skipped and
// the existing raw-amount logic governs.
func (s *Solver) calculateProfitableBid(ctx context.Context, networkKey string, intent *types.Intent) (bool, *big.Int) {
	sourceClient := s.clients[networkKey]
	tokenAAddr := common.HexToAddress(intent.TokenA)

	// --- 1. Calculate target bid via profit margin ---
	minBid := new(big.Int).Add(intent.AmountA, intent.Reward)
	profitMarginFloat := new(big.Float).SetFloat64(s.cfg.SolverConfig.MinProfitMargin)
	minBidFloat := new(big.Float).SetInt(minBid)
	marginAmount := new(big.Float).Mul(minBidFloat, profitMarginFloat)
	targetBidFloat := new(big.Float).Sub(minBidFloat, marginAmount)
	targetBid, _ := targetBidFloat.Int(nil)

	if targetBid.Sign() <= 0 {
		log.WithFields(log.Fields{
			"minBid":       minBid.String(),
			"profitMargin": s.cfg.SolverConfig.MinProfitMargin,
		}).Info("Calculated bid is negative or zero, not profitable")
		return false, nil
	}

	// --- 2. USD profitability check ---
	// Skip the intent if we can't resolve token symbols or fetch oracle prices
	// for both legs. Without verified USD pricing we have no safe way to know
	// the bid is actually profitable, so let the intent expire and allow the
	// user to claim back their deposit.
	destChainID := intent.DestChainId.String()
	destClient, destExists := s.clients[destChainID]
	if !destExists {
		log.WithFields(log.Fields{
			"intentID":    intent.IntentID.String(),
			"destChainID": destChainID,
		}).Info("Destination chain not configured, skipping intent")
		return false, nil
	}

	tokenBAddr := common.HexToAddress(intent.TokenB)

	symbolA, err := sourceClient.GetTokenSymbol(ctx, tokenAAddr)
	if err != nil {
		log.WithError(err).WithField("token", tokenAAddr.Hex()).Info("Could not resolve TokenA symbol, skipping intent")
		return false, nil
	}
	symbolB, err := destClient.GetTokenSymbol(ctx, tokenBAddr)
	if err != nil {
		log.WithError(err).WithField("token", tokenBAddr.Hex()).Info("Could not resolve TokenB symbol, skipping intent")
		return false, nil
	}

	priceA, err := s.oracle.FetchConsensusPrice(symbolA)
	if err != nil {
		log.WithError(err).WithField("symbol", symbolA).Info("Could not fetch TokenA price from oracles, skipping intent")
		return false, nil
	}
	priceB, err := s.oracle.FetchConsensusPrice(symbolB)
	if err != nil {
		log.WithError(err).WithField("symbol", symbolB).Info("Could not fetch TokenB price from oracles, skipping intent")
		return false, nil
	}

	decimalsA, err := sourceClient.GetTokenDecimals(ctx, tokenAAddr)
	if err != nil {
		decimalsA = 18
	}
	decimalsB, err := destClient.GetTokenDecimals(ctx, tokenBAddr)
	if err != nil {
		decimalsB = 18
	}

	// Solver net gain on source chain: amountA + reward - targetBid (= margin * minBid)
	netGainA := new(big.Int).Sub(minBid, targetBid)
	netGainAFloat, _ := new(big.Float).SetInt(netGainA).Float64()
	gainUSD := (netGainAFloat / math.Pow(10, float64(decimalsA))) * priceA

	// Cost of providing expectedAmountB of TokenB on dest chain
	expectedBFloat, _ := new(big.Float).SetInt(intent.ExpectedAmountB).Float64()
	costUSD := (expectedBFloat / math.Pow(10, float64(decimalsB))) * priceB

	profitUSD := gainUSD - costUSD

	log.WithFields(log.Fields{
		"symbolA":   symbolA,
		"symbolB":   symbolB,
		"priceA":    priceA,
		"priceB":    priceB,
		"decimalsA": decimalsA,
		"decimalsB": decimalsB,
		"gainUSD":   gainUSD,
		"costUSD":   costUSD,
		"profitUSD": profitUSD,
	}).Info("USD profitability analysis")

	if profitUSD < 0 {
		log.WithFields(log.Fields{
			"profitUSD": profitUSD,
			"symbolA":   symbolA,
			"symbolB":   symbolB,
		}).Info("Intent not profitable in USD terms, skipping")
		return false, nil
	}

	// --- 3. Balance and max-bid guards ---
	balance, err := sourceClient.GetBalance(ctx, tokenAAddr)
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

	// --- 4. Destination-chain TokenB balance guard ---
	// Even if the source-side bid is profitable, skip the intent unless we hold
	// enough TokenB on the destination chain to fulfill expectedAmountB.
	destBalance, err := destClient.GetBalance(ctx, tokenBAddr)
	if err != nil {
		log.WithError(err).WithField("destChainID", destChainID).Error("Failed to get TokenB balance on dest chain")
		return false, nil
	}

	destBalanceFloat := new(big.Float).SetInt(destBalance)
	destMaxAffordableFloat := new(big.Float).Mul(destBalanceFloat, bufferFloat)
	destMaxAffordable, _ := destMaxAffordableFloat.Int(nil)

	if intent.ExpectedAmountB.Cmp(destMaxAffordable) > 0 {
		log.WithFields(log.Fields{
			"intentID":          intent.IntentID.String(),
			"expectedAmountB":   intent.ExpectedAmountB.String(),
			"destBalance":       destBalance.String(),
			"destMaxAffordable": destMaxAffordable.String(),
			"destChainID":       destChainID,
		}).Info("Insufficient TokenB on destination chain to solve")
		return false, nil
	}

	if targetBid.Cmp(s.cfg.SolverConfig.MaxBidAmount) > 0 {
		log.WithFields(log.Fields{
			"targetBid":    targetBid.String(),
			"maxBidAmount": s.cfg.SolverConfig.MaxBidAmount.String(),
		}).Info("Bid exceeds max bid amount")
		return false, nil
	}

	log.WithFields(log.Fields{
		"targetBid":    targetBid.String(),
		"profitMargin": s.cfg.SolverConfig.MinProfitMargin,
	}).Info("Intent is profitable, will bid")
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

				if intent.WinningSolver == client.GetSolverAddress().Hex() {
					key := fmt.Sprintf("%s-%s", networkKey, intentID.String())

					s.mu.Lock()
					alreadyHandling := s.handlingWins[key]
					if !alreadyHandling {
						s.handlingWins[key] = true
						// Persist to database
						s.store.SetHandlingWin(networkKey, intentID.String(), true)
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
						"ourAddr":      client.GetSolverAddress().Hex(),
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
			isWinning := highestBidder == client.GetSolverAddress()

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

	// Stale RPC nodes lag behind confirmed state; a prior Safe tx on this chain
	// (e.g. a concurrent solve flow) may not yet be reflected, causing the nonce
	// read inside ApproveTokenViaSafe to return a stale value and the tx to revert.
	time.Sleep(5 * time.Second)

	approveTx, err := s.retryTransaction(ctx, networkKey, "approve-tokens", func() (*ethtypes.Transaction, error) {
		if client.HasSafe() {
			return client.ApproveTokenViaSafe(ctx, tokenAddr, bidAmount)
		}
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

	// Stale RPC nodes lag behind confirmed state; wait for Safe nonce to reflect
	// the approve tx before signing depositAndPickup with the updated nonce.
	time.Sleep(5 * time.Second)

	log.WithField("intentID", intentID).Debug("Tokens approved, depositing")

	tx, err := s.retryTransaction(ctx, networkKey, "deposit-and-pickup", func() (*ethtypes.Transaction, error) {
		if client.HasSafe() {
			return client.DepositAndPickupViaSafe(ctx, intentID)
		}
		return client.DepositAndPickup(ctx, intentID)
	})
	if err != nil {
		log.WithError(err).Error("Failed to deposit and pickup after retries")
		return
	}

	_, err = client.WaitForTransaction(ctx, tx)
	if err != nil {
		// Check if deposit already went through (e.g. from a retry that landed on-chain)
		log.WithError(err).Warn("Deposit transaction reverted, checking if already deposited on-chain")
		onChainIntent, checkErr := client.GetIntent(ctx, intentID)
		if checkErr != nil || !onChainIntent.Deposited {
			log.WithError(err).Error("Failed to confirm deposit transaction and intent not deposited on-chain")
			return
		}
		log.WithField("intentID", intentID).Info("Intent already deposited on-chain despite tx revert, continuing")
	}

	depositTxHash := tx.Hash().Hex()
	key := fmt.Sprintf("%s-%s", networkKey, intentID.String())
	s.mu.Lock()
	if status, ok := s.activeIntents[key]; ok {
		status.IsWinner = true
		status.Deposited = true
		status.Status = "deposited"
		status.DepositTxHash = depositTxHash
		status.LastUpdated = time.Now()
		// Persist to database
		s.persistIntentStatus(networkKey, intentID.String(), status)
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
	maxRetries := 8
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
		if destClient.HasSafe() {
			return destClient.ApproveTokenViaSafe(ctx, tokenBAddr, totalAmount)
		}
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

	// Wait for RPC state to reflect the approval before simulating solveIntentOnChain2.
	// Slow testnet nodes (e.g. Base Sepolia public RPC) lag behind confirmed state,
	// causing eth_estimateGas to see stale allowance = 0 and revert the simulation.
	time.Sleep(5 * time.Second)

	log.WithField("intentID", intentID).Debug("TokenB approved, solving intent")

	userAddr := common.HexToAddress(intent.User)
	expectedAmountB := intent.ExpectedAmountB
	sourceChainID := sourceChainCfg.ChainID

	tx, err := s.retryTransaction(ctx, destNetwork, "solve-intent", func() (*ethtypes.Transaction, error) {
		if destClient.HasSafe() {
			return destClient.SolveIntentOnChain2ViaSafe(ctx, sourceChainID, intentID, userAddr, tokenBAddr, expectedAmountB, recipientAddrs, amountBigInts)
		}
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

	go s.settleIntent(ctx, sourceNetwork, intentID, intentID, destClient.GetSolverAddress(), destNetwork, depositTxHash, solveTxHash)
}

// getRecipientsFromRelayer fetches recipient information from the relayer.
// Includes solver address in request header for authorization.
// sourceChainID helps disambiguate when the same intent ID exists on multiple chains.
func (s *Solver) getRecipientsFromRelayer(intentID string, sourceChainID string) ([]string, []string, error) {
	url := fmt.Sprintf("%s/get-recipients/%s?sourceChainId=%s", s.cfg.RelayerURL, intentID, sourceChainID)

	// Use the source chain's solver address — must match winningSolver on-chain.
	// Map iteration order is random, so we must look up by sourceChainID explicitly.
	var solverAddr string
	if srcClient, exists := s.clients[sourceChainID]; exists {
		solverAddr = srcClient.GetSolverAddress().Hex()
	} else {
		for _, c := range s.clients {
			solverAddr = c.GetSolverAddress().Hex()
			break
		}
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

	if resp.StatusCode == http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("recipients not found: %s", string(body))
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
func (s *Solver) settleIntent(ctx context.Context, network string, chain1IntentID, chain2IntentID *big.Int, solverAddr common.Address, destChainName string, depositTxHash, solveTxHash string) {
	settleReq := types.SettleRequest{
		IntentID:         chain1IntentID.String(),
		Network:          network,
		Chain2IntentID:   chain2IntentID.String(),
		SolverAddress:    solverAddr.Hex(),
		DestinationChain: destChainName,
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

			// Update transaction history record to completed
			settleTxHash := settleResp.SettleTxHash
			if settleTxHash == "" {
				settleTxHash = settleResp.TransactionHash
			}
			s.completeTransactionRecord(chain1IntentID.String(), depositTxHash, solveTxHash, settleTxHash)

			// Update status
			key := fmt.Sprintf("%s-%s", network, chain1IntentID.String())
			s.mu.Lock()
			if status, ok := s.activeIntents[key]; ok {
				status.Completed = true
				status.Status = "completed"
				status.LastUpdated = time.Now()
				// Persist to database
				s.persistIntentStatus(network, chain1IntentID.String(), status)
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
	// Close blockchain clients
	for _, client := range s.clients {
		client.Close()
	}

	// Close database
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			log.WithError(err).Error("Failed to close database")
		} else {
			log.Info("Database closed successfully")
		}
	}
}

// persistIntentStatus saves intent status to the database
func (s *Solver) persistIntentStatus(networkKey, intentID string, status *types.IntentStatus) {
	if err := s.store.SaveIntentStatus(networkKey, intentID, status); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"network":  networkKey,
			"intentID": intentID,
		}).Error("Failed to persist intent status")
	}
}

// persistLastProcessedBlock saves the last processed block to the database
func (s *Solver) persistLastProcessedBlock(networkKey string, blockNum *big.Int) {
	if err := s.store.SaveLastProcessedBlock(networkKey, blockNum); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"network": networkKey,
			"block":   blockNum.String(),
		}).Error("Failed to persist last processed block")
	}
}

// saveTransactionRecord writes a new "pending" transaction record for a detected intent.
// Skips silently if a record already exists for this intent ID.
func (s *Solver) saveTransactionRecord(intent *types.Intent, networkKey string) {
	if existing, _ := s.store.GetTransactionRecord(intent.IntentID.String()); existing != nil {
		return
	}
	now := time.Now()
	record := &types.TransactionRecord{
		IntentID:        intent.IntentID.String(),
		UserAddress:     intent.User,
		SourceChainID:   networkKey,
		DestChainID:     intent.DestChainId.String(),
		TokenA:          intent.TokenA,
		TokenB:          intent.TokenB,
		AmountA:         intent.AmountA.String(),
		ExpectedAmountB: intent.ExpectedAmountB.String(),
		Status:          "pending",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.store.SaveTransactionRecord(record); err != nil {
		log.WithError(err).WithField("intentID", intent.IntentID).Error("Failed to save transaction record")
	}
}

// completeTransactionRecord marks a transaction record as completed and attaches all tx hashes.
func (s *Solver) completeTransactionRecord(intentID, depositTxHash, solveTxHash, settleTxHash string) {
	record, err := s.store.GetTransactionRecord(intentID)
	if err != nil || record == nil {
		return
	}
	now := time.Now()
	record.Status = "completed"
	record.DepositTxHash = depositTxHash
	record.SolveTxHash = solveTxHash
	record.SettleTxHash = settleTxHash
	record.UpdatedAt = now
	record.CompletedAt = &now
	if err := s.store.SaveTransactionRecord(record); err != nil {
		log.WithError(err).WithField("intentID", intentID).Error("Failed to update transaction record to completed")
	}
}

// GetTransactionsByUser exposes paginated transaction history for a user address.
func (s *Solver) GetTransactionsByUser(userAddress, status string, page, limit int) ([]*types.TransactionRecord, int, error) {
	return s.store.GetTransactionsByUser(userAddress, status, page, limit)
}

// ---------------------------------------------------------------------------
// Price oracle — fetches USD prices from four public APIs and returns a
// consensus value (median, rounded to 2 decimal places).
// ---------------------------------------------------------------------------

type priceOracle struct {
	client *http.Client
}

func newPriceOracle() *priceOracle {
	return &priceOracle{client: &http.Client{Timeout: 5 * time.Second}}
}

var coinGeckoIDs = map[string]string{
	"USDC": "usd-coin",
	"WETH": "weth",
	"ETH":  "ethereum",
	"ZEN":  "zencash",
}

var coinCapIDs = map[string]string{
	"USDC": "usd-coin",
	"WETH": "ethereum",
	"ETH":  "ethereum",
	"ZEN":  "zencash",
}

var cryptoCompareSymbols = map[string]string{
	"USDC": "USDC",
	"WETH": "ETH",
	"ETH":  "ETH",
	"ZEN":  "ZEN",
}

// binancePairs omits ZEN — no USDT pair exists on Binance.
var binancePairs = map[string]string{
	"USDC": "USDCUSDT",
	"WETH": "ETHUSDT",
	"ETH":  "ETHUSDT",
}

// FetchConsensusPrice queries up to four APIs in parallel and returns the median
// USD price rounded to two decimal places. Requires at least two sources to agree.
func (o *priceOracle) FetchConsensusPrice(symbol string) (float64, error) {
	upper := strings.ToUpper(symbol)

	type result struct {
		source string
		price  float64
		err    error
	}

	fetchers := []struct {
		name string
		fn   func(string) (float64, error)
	}{
		{"coingecko", o.fetchCoinGecko},
		{"coincap", o.fetchCoinCap},
		{"cryptocompare", o.fetchCryptoCompare},
		{"binance", o.fetchBinance},
	}

	ch := make(chan result, len(fetchers))
	for _, f := range fetchers {
		go func(name string, fn func(string) (float64, error)) {
			price, err := fn(upper)
			ch <- result{name, price, err}
		}(f.name, f.fn)
	}

	var prices []float64
	for range fetchers {
		r := <-ch
		if r.err != nil {
			log.WithFields(log.Fields{"source": r.source, "symbol": upper, "error": r.err}).Debug("Price source unavailable")
			continue
		}
		if r.price > 0 {
			log.WithFields(log.Fields{"source": r.source, "symbol": upper, "price": r.price}).Debug("Fetched price from source")
			prices = append(prices, r.price)
		}
	}

	if len(prices) < 2 {
		return 0, fmt.Errorf("insufficient price data for %s: only %d source(s) responded", symbol, len(prices))
	}

	sorted := make([]float64, len(prices))
	copy(sorted, prices)
	sort.Float64s(sorted)
	n := len(sorted)
	var median float64
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	} else {
		median = sorted[n/2]
	}
	consensus := math.Round(median*100) / 100

	log.WithFields(log.Fields{"symbol": upper, "sources": len(prices), "consensus": consensus}).Info("Computed consensus price")
	return consensus, nil
}

func (o *priceOracle) fetchCoinGecko(symbol string) (float64, error) {
	id, ok := coinGeckoIDs[symbol]
	if !ok {
		return 0, fmt.Errorf("no CoinGecko ID for symbol %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd", id))
	if err != nil {
		return 0, fmt.Errorf("coingecko request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("coingecko returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read coingecko response: %w", err)
	}
	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, fmt.Errorf("failed to parse coingecko response: %w", err)
	}
	tokenData, ok := data[id]
	if !ok {
		return 0, fmt.Errorf("token %s not found in coingecko response", id)
	}
	price, ok := tokenData["usd"]
	if !ok {
		return 0, fmt.Errorf("USD price not found for %s in coingecko response", id)
	}
	return price, nil
}

func (o *priceOracle) fetchCoinCap(symbol string) (float64, error) {
	id, ok := coinCapIDs[symbol]
	if !ok {
		return 0, fmt.Errorf("no CoinCap ID for symbol %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://api.coincap.io/v2/assets/%s", id))
	if err != nil {
		return 0, fmt.Errorf("coincap request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("coincap returned status %d", resp.StatusCode)
	}
	var data struct {
		Data struct {
			PriceUsd string `json:"priceUsd"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("failed to parse coincap response: %w", err)
	}
	price, err := strconv.ParseFloat(data.Data.PriceUsd, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse coincap price %q: %w", data.Data.PriceUsd, err)
	}
	return price, nil
}

func (o *priceOracle) fetchCryptoCompare(symbol string) (float64, error) {
	fsym, ok := cryptoCompareSymbols[symbol]
	if !ok {
		return 0, fmt.Errorf("no CryptoCompare symbol for %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://min-api.cryptocompare.com/data/price?fsym=%s&tsyms=USD", fsym))
	if err != nil {
		return 0, fmt.Errorf("cryptocompare request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("cryptocompare returned status %d", resp.StatusCode)
	}
	var data map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("failed to parse cryptocompare response: %w", err)
	}
	price, ok := data["USD"]
	if !ok {
		return 0, fmt.Errorf("USD price not found in cryptocompare response for %s", fsym)
	}
	return price, nil
}

func (o *priceOracle) fetchBinance(symbol string) (float64, error) {
	pair, ok := binancePairs[symbol]
	if !ok {
		return 0, fmt.Errorf("no Binance pair for symbol %s", symbol)
	}
	resp, err := o.client.Get(fmt.Sprintf("https://api.binance.com/api/v3/ticker/price?symbol=%s", pair))
	if err != nil {
		return 0, fmt.Errorf("binance request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("binance returned status %d", resp.StatusCode)
	}
	var data struct {
		Price string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("failed to parse binance response: %w", err)
	}
	price, err := strconv.ParseFloat(data.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse binance price %q: %w", data.Price, err)
	}
	return price, nil
}
