package solver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// Solver manages the intent solving process
type Solver struct {
	cfg                *config.Config
	clients            map[string]*blockchain.Client
	activeIntents      map[string]*types.IntentStatus
	lastProcessedBlock map[string]*big.Int
	isRunning          bool
	mu                 sync.RWMutex
	stopChan           chan struct{}
}

// NewSolver creates a new solver instance
func NewSolver(cfg *config.Config) (*Solver, error) {
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
		log.WithField("network", networkKey).Info("Blockchain client initialized")
	}

	return &Solver{
		cfg:                cfg,
		clients:            clients,
		activeIntents:      make(map[string]*types.IntentStatus),
		lastProcessedBlock: make(map[string]*big.Int),
		stopChan:           make(chan struct{}),
	}, nil
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

	// Start event polling for each network
	for networkKey := range s.cfg.Networks {
		go s.pollEvents(networkKey)
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
	s.isRunning = false
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
		// For now, get ETH balance (tokenAddress zero address)
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

	// Initialize last processed block
	currentBlock, err := client.GetCurrentBlock(ctx)
	if err != nil {
		log.WithError(err).WithField("network", networkKey).Error("Failed to get current block")
		return
	}
	s.lastProcessedBlock[networkKey] = currentBlock

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

	fromBlock := s.lastProcessedBlock[networkKey]
	if fromBlock == nil {
		fromBlock = currentBlock
	}

	// Don't process if we're at the same block
	if fromBlock.Cmp(currentBlock) >= 0 {
		return nil
	}

	// Get logs for IntentCreated events
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

	// Process each IntentCreated event
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

	// Update last processed block
	s.lastProcessedBlock[networkKey] = currentBlock

	return nil
}

// handleNewIntent handles a new intent event
func (s *Solver) handleNewIntent(ctx context.Context, networkKey string, intentID *big.Int) {
	client := s.clients[networkKey]

	// Get intent details
	intent, err := client.GetIntent(ctx, intentID)
	if err != nil {
		log.WithError(err).WithField("intentID", intentID).Error("Failed to get intent")
		return
	}

	// Check if intent is still active
	if intent.Completed {
		log.WithField("intentID", intentID).Debug("Intent already completed")
		return
	}

	// Calculate if this is a profitable intent
	profitable, bidAmount := s.calculateProfitableBid(ctx, networkKey, intent)
	if !profitable {
		log.WithFields(log.Fields{
			"intentID": intentID,
			"amountA":  intent.AmountA,
			"reward":   intent.Reward,
		}).Info("Intent not profitable, skipping")
		return
	}

	// Track this intent
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

	// Start bidding process
	go s.startBiddingLoop(ctx, networkKey, intentID, bidAmount)
}

// calculateProfitableBid calculates a profitable bid amount
func (s *Solver) calculateProfitableBid(ctx context.Context, networkKey string, intent *types.Intent) (bool, *big.Int) {
	// Calculate minimum profitable bid: amountA + reward
	minBid := new(big.Int).Add(intent.AmountA, intent.Reward)

	// Apply profit margin from config (set MIN_PROFIT_MARGIN=0 in .env to accept all intents)
	profitMargin := new(big.Float).SetFloat64(s.cfg.SolverConfig.MinProfitMargin)
	minBidFloat := new(big.Float).SetInt(minBid)
	marginAmount := new(big.Float).Mul(minBidFloat, profitMargin)

	// Target bid = minBid - margin (we bid less to make profit)
	targetBidFloat := new(big.Float).Sub(minBidFloat, marginAmount)

	// Subtract 2 tokens to make our bid slightly more competitive
	twoTokens := new(big.Float).SetInt(new(big.Int).Mul(big.NewInt(2), big.NewInt(1e18)))
	targetBidFloat = new(big.Float).Sub(targetBidFloat, twoTokens)

	targetBid, _ := targetBidFloat.Int(nil)

	log.WithFields(log.Fields{
		"minBid":       minBid.String(),
		"profitMargin": s.cfg.SolverConfig.MinProfitMargin,
		"targetBid":    targetBid.String(),
	}).Debug("Calculated bid amount")

	// Check if we can afford it (check TokenA balance, not ETH)
	client := s.clients[networkKey]
	tokenAAddr := common.HexToAddress(intent.TokenA)
	log.WithFields(log.Fields{
		"tokenAddress": tokenAAddr.Hex(),
		"intentID":     intent.IntentID.String(),
	}).Info("Checking TokenA balance")
	balance, err := client.GetBalance(ctx, tokenAAddr)
	if err != nil {
		log.WithError(err).Error("Failed to get TokenA balance")
		return false, nil
	}
	log.WithFields(log.Fields{
		"balance":      balance.String(),
		"tokenAddress": tokenAAddr.Hex(),
	}).Info("Retrieved TokenA balance")

	// Apply balance buffer
	bufferFloat := new(big.Float).SetFloat64(1.0 - s.cfg.SolverConfig.BalanceBuffer)
	balanceFloat := new(big.Float).SetInt(balance)
	maxAffordable := new(big.Float).Mul(balanceFloat, bufferFloat)
	maxAffordableInt, _ := maxAffordable.Int(nil)

	// Check affordability
	if targetBid.Cmp(maxAffordableInt) > 0 {
		log.WithFields(log.Fields{
			"targetBid":     targetBid.String(),
			"maxAffordable": maxAffordableInt.String(),
			"balance":       balance.String(),
		}).Info("Cannot afford this intent")
		return false, nil
	}

	// Check against max bid amount
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
			// Check if intent is still active
			intent, err := client.GetIntent(ctx, intentID)
			if err != nil {
				log.WithError(err).Error("Failed to get intent")
				continue
			}

			if intent.Completed {
				log.WithField("intentID", intentID).Info("Intent completed")
				return
			}

			// Check if auction has ended
			currentTime := time.Now().Unix()
			if big.NewInt(currentTime).Cmp(intent.EndTime) > 0 {
				log.WithField("intentID", intentID).Info("Auction ended")

				// Finalize the auction to set the winner (if not already finalized)
				if intent.WinningSolver == (common.Address{}.Hex()) {
					log.WithField("intentID", intentID).Info("Finalizing auction...")
					tx, err := client.FinalizeAuction(ctx, intentID)
					if err != nil {
						log.WithError(err).Warn("Failed to finalize auction (may already be finalized)")
						// Don't return - continue to check if we won
					} else {
						// Wait for finalization transaction to be confirmed
						_, err = client.WaitForTransaction(ctx, tx)
						if err != nil {
							log.WithError(err).Error("Failed to confirm finalization transaction")
							return
						}
						log.WithField("intentID", intentID).Info("Auction finalized and confirmed")
					}

					// Re-fetch intent to get the winner
					intent, err = client.GetIntent(ctx, intentID)
					if err != nil {
						log.WithError(err).Error("Failed to get intent after finalization")
						return
					}
				}

				// If we won, proceed with deposit and pickup
				if intent.WinningSolver == client.GetAddress().Hex() {
					log.WithFields(log.Fields{
						"intentID": intentID,
						"winner":   intent.WinningSolver,
					}).Info("We won the auction!")
					go s.handleAuctionWon(ctx, networkKey, intentID)
				} else {
					log.WithFields(log.Fields{
						"intentID": intentID,
						"winner":   intent.WinningSolver,
						"ourAddr":  client.GetAddress().Hex(),
					}).Info("We did not win the auction")
				}
				return
			}

			// Get current highest bid
			highestBid, highestBidder, err := client.GetHighestBid(ctx, intentID)
			if err != nil {
				log.WithError(err).Error("Failed to get highest bid")
				continue
			}

			// If no bids yet or our bid is higher, place our bid
			if !hasPlacedBid && (highestBid.Cmp(big.NewInt(0)) == 0 || ourBid.Cmp(highestBid) > 0) {
				_, err := client.PlaceBid(ctx, intentID, ourBid)
				if err != nil {
					log.WithError(err).Error("Failed to place bid")
					continue
				}
				hasPlacedBid = true
				log.WithFields(log.Fields{
					"intentID": intentID,
					"bidAmount": ourBid,
				}).Info("Bid placed successfully")
			} else if highestBid.Cmp(big.NewInt(0)) > 0 && highestBidder != client.GetAddress() {
				// Someone else has a higher bid
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

// handleAuctionWon handles the case when we win an auction
func (s *Solver) handleAuctionWon(ctx context.Context, networkKey string, intentID *big.Int) {
	client := s.clients[networkKey]

	// Get intent details to know the token and bid amount
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
	}).Info("Won auction, approving tokens for deposit...")

	// Approve tokens for the bridge contract
	tokenAddr := common.HexToAddress(intent.TokenA)
	bidAmount := intent.WinningBid

	approveTx, err := client.ApproveToken(ctx, tokenAddr, bidAmount)
	if err != nil {
		log.WithError(err).Error("Failed to approve tokens")
		return
	}

	// Wait for approval to be confirmed
	_, err = client.WaitForTransaction(ctx, approveTx)
	if err != nil {
		log.WithError(err).Error("Failed to confirm token approval")
		return
	}

	log.WithField("intentID", intentID).Info("Tokens approved, depositing...")

	// Deposit tokens
	tx, err := client.DepositAndPickup(ctx, intentID)
	if err != nil {
		log.WithError(err).Error("Failed to deposit and pickup")
		return
	}

	// Wait for transaction to be confirmed
	_, err = client.WaitForTransaction(ctx, tx)
	if err != nil {
		log.WithError(err).Error("Failed to confirm deposit transaction")
		return
	}

	// Update intent status
	key := fmt.Sprintf("%s-%s", networkKey, intentID.String())
	s.mu.Lock()
	if status, ok := s.activeIntents[key]; ok {
		status.IsWinner = true
		status.Deposited = true
		status.Status = "deposited"
		status.LastUpdated = time.Now()
	}
	s.mu.Unlock()

	// Now solve the intent on the destination chain
	go s.solveIntentOnDestinationChain(ctx, networkKey, intentID)
}

// solveIntentOnDestinationChain solves the intent on the destination network
func (s *Solver) solveIntentOnDestinationChain(ctx context.Context, sourceNetwork string, intentID *big.Int) {
	// Determine destination network (opposite of source)
	var destNetwork string
	if sourceNetwork == "horizen" {
		destNetwork = "base"
	} else {
		destNetwork = "horizen"
	}

	// Get intent details from source chain
	sourceClient := s.clients[sourceNetwork]
	intent, err := sourceClient.GetIntent(ctx, intentID)
	if err != nil {
		log.WithError(err).Error("Failed to get intent details from source chain")
		return
	}

	// Get recipient information from relayer (deposit is already confirmed)
	recipients, amounts, err := s.getRecipientsFromRelayer(intentID.String())
	if err != nil {
		log.WithError(err).Error("Failed to get recipients from relayer")
		return
	}

	log.WithFields(log.Fields{
		"intentID":       intentID,
		"recipientCount": len(recipients),
	}).Info("Successfully retrieved recipients from relayer")

	// Convert to proper types
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

	// Approve tokenB for the bridge contract on destination chain
	tokenBAddr := common.HexToAddress(intent.TokenB)
	log.WithFields(log.Fields{
		"intentID":    intentID,
		"tokenB":      intent.TokenB,
		"totalAmount": totalAmount.String(),
	}).Info("Approving tokenB for solving on destination chain...")

	approveTx, err := destClient.ApproveToken(ctx, tokenBAddr, totalAmount)
	if err != nil {
		log.WithError(err).Error("Failed to approve tokenB")
		return
	}

	// Wait for approval to be confirmed
	_, err = destClient.WaitForTransaction(ctx, approveTx)
	if err != nil {
		log.WithError(err).Error("Failed to confirm tokenB approval")
		return
	}

	log.WithField("intentID", intentID).Info("TokenB approved, solving intent...")

	// Solve intent on destination chain with all required parameters
	userAddr := common.HexToAddress(intent.User)
	expectedAmountB := intent.ExpectedAmountB

	tx, err := destClient.SolveIntentOnChain2(ctx, intentID, userAddr, tokenBAddr, expectedAmountB, recipientAddrs, amountBigInts)
	if err != nil {
		log.WithError(err).Error("Failed to solve intent on chain2")
		return
	}

	// Wait for solve transaction to be confirmed
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

	// Call relayer to settle the intent
	go s.settleIntent(ctx, sourceNetwork, intentID, intentID, destClient.GetAddress())
}

// getRecipientsFromRelayer fetches recipient information from the relayer
func (s *Solver) getRecipientsFromRelayer(intentID string) ([]string, []string, error) {
	url := fmt.Sprintf("%s/get-recipients/%s", s.cfg.RelayerURL, intentID)

	// Get solver's address (use any client, they all have the same address)
	var solverAddr string
	for _, client := range s.clients {
		solverAddr = client.GetAddress().Hex()
		break
	}

	// Create request with solver address header
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
	}).Info("Retrieved recipients from relayer")

	return recipientInfo.Recipients, recipientInfo.Amounts, nil
}

// settleIntent calls the relayer to settle an intent
func (s *Solver) settleIntent(ctx context.Context, network string, chain1IntentID, chain2IntentID *big.Int, solverAddr common.Address) {
	settleReq := types.SettleRequest{
		IntentID:         chain1IntentID.String(),
		Network:          network,
		Chain2IntentID:   chain2IntentID.String(),
		SolverAddress:    solverAddr.Hex(),
		DestinationChain: "base", // For now, hardcoded
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

// Close closes all blockchain clients
func (s *Solver) Close() {
	for _, client := range s.clients {
		client.Close()
	}
}
