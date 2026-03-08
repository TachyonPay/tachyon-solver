package twap

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

const bridgeIntentABI = `[{"inputs":[{"internalType":"address","name":"_tokenA","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_amountA","type":"uint256"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"_reward","type":"uint256"},{"internalType":"uint256","name":"_destChainId","type":"uint256"},{"internalType":"uint256","name":"_auctionDuration","type":"uint256"}],"name":"createIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

const erc20TransferFromABI = `[{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"from","type":"address"},{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"transferFrom","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

// Engine manages TWAP orders and executes tranches on schedule.
// All user funds are pulled upfront in a single transferFrom to hide the TWAP pattern.
// The TEE then drips out individual BridgeIntents per period from its own balance.
type Engine struct {
	cfg        *config.Config
	privateKey *ecdsa.PrivateKey
	address    common.Address
	clients    map[int64]*ethclient.Client // chainID -> ethclient
	bridges    map[int64]common.Address    // chainID -> bridge contract address
	chainIDs   map[int64]*big.Int
	chainNames map[int64]string // chainID -> chain name

	orders map[string]*types.TWAPOrder // orderID -> order
	mu     sync.RWMutex

	relayerURL string

	ctx    context.Context
	cancel context.CancelFunc
}

// NewEngine creates a new TWAP engine
func NewEngine(cfg *config.Config) (*Engine, error) {
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(cfg.PrivateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	address := crypto.PubkeyToAddress(privKey.PublicKey)

	clients := make(map[int64]*ethclient.Client)
	bridges := make(map[int64]common.Address)
	chainIDs := make(map[int64]*big.Int)
	chainNames := make(map[int64]string)

	for _, chain := range cfg.Chains {
		cid := chain.ChainID.Int64()
		client, err := ethclient.Dial(chain.RpcURL)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to chain %s: %w", chain.Name, err)
		}
		clients[cid] = client
		bridges[cid] = common.HexToAddress(chain.BridgeAddress)
		chainIDs[cid] = chain.ChainID
		chainNames[cid] = chain.Name

		log.WithFields(log.Fields{
			"chain":   chain.Name,
			"chainID": cid,
			"bridge":  chain.BridgeAddress,
		}).Info("TWAP engine connected to chain")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Engine{
		cfg:        cfg,
		privateKey: privKey,
		address:    address,
		clients:    clients,
		bridges:    bridges,
		chainIDs:   chainIDs,
		chainNames: chainNames,
		orders:     make(map[string]*types.TWAPOrder),
		relayerURL: cfg.RelayerURL,
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// GetECIESPublicKey returns the hex-encoded ECIES public key
func (e *Engine) GetECIESPublicKey() string {
	pubKeyBytes := e.cfg.ECIESPublicKey.Bytes(false)
	return hex.EncodeToString(pubKeyBytes)
}

// GetAddress returns the TEE wallet address (user approves this for transferFrom)
func (e *Engine) GetAddress() string {
	return e.address.Hex()
}

// DecryptAndCreateOrder decrypts the ECIES-encrypted order params and creates a TWAP order
func (e *Engine) DecryptAndCreateOrder(encryptedHex string) (*types.TWAPOrder, error) {
	encryptedBytes, err := hex.DecodeString(encryptedHex)
	if err != nil {
		return nil, fmt.Errorf("invalid encrypted data hex: %w", err)
	}

	decryptedBytes, err := ecies.Decrypt(e.cfg.ECIESPrivateKey, encryptedBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt TWAP order: %w", err)
	}

	var params types.TWAPOrderParams
	if err := json.Unmarshal(decryptedBytes, &params); err != nil {
		return nil, fmt.Errorf("invalid decrypted order format: %w", err)
	}

	return e.CreateOrder(&params)
}

// CreateOrder validates params, pulls ALL funds upfront, then starts periodic execution
func (e *Engine) CreateOrder(params *types.TWAPOrderParams) (*types.TWAPOrder, error) {
	// Validate chains
	if _, ok := e.clients[params.SourceChainID]; !ok {
		return nil, fmt.Errorf("source chain %d not configured", params.SourceChainID)
	}
	if _, ok := e.clients[params.DestChainID]; !ok {
		return nil, fmt.Errorf("dest chain %d not configured", params.DestChainID)
	}

	// Validate chain names match IDs
	if name, ok := e.chainNames[params.SourceChainID]; ok {
		if params.ChainIn != "" && !strings.EqualFold(params.ChainIn, name) {
			return nil, fmt.Errorf("chainIn '%s' does not match chain ID %d (expected '%s')", params.ChainIn, params.SourceChainID, name)
		}
	}
	if name, ok := e.chainNames[params.DestChainID]; ok {
		if params.ChainOut != "" && !strings.EqualFold(params.ChainOut, name) {
			return nil, fmt.Errorf("chainOut '%s' does not match chain ID %d (expected '%s')", params.ChainOut, params.DestChainID, name)
		}
	}

	// Parse amounts
	totalAmountIn, ok := new(big.Int).SetString(params.TotalAmountIn, 10)
	if !ok || totalAmountIn.Sign() <= 0 {
		return nil, fmt.Errorf("invalid totalAmountIn: %s", params.TotalAmountIn)
	}
	amountPerPeriod, ok := new(big.Int).SetString(params.AmountPerPeriod, 10)
	if !ok || amountPerPeriod.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amountPerPeriod: %s", params.AmountPerPeriod)
	}

	if params.Period < 5 {
		return nil, fmt.Errorf("period must be at least 5 seconds, got %d", params.Period)
	}
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("endTime must be after startTime")
	}
	if len(params.Recipients) == 0 || len(params.Recipients) != len(params.Amounts) {
		return nil, fmt.Errorf("recipients and amounts must be non-empty and same length")
	}

	totalTranches := int(new(big.Int).Div(totalAmountIn, amountPerPeriod).Int64())
	if totalTranches == 0 {
		totalTranches = 1
	}

	// Resolve chain names
	chainIn := params.ChainIn
	if chainIn == "" {
		chainIn = e.chainNames[params.SourceChainID]
	}
	chainOut := params.ChainOut
	if chainOut == "" {
		chainOut = e.chainNames[params.DestChainID]
	}

	// Step 1: Pull ALL funds from user upfront (single on-chain transferFrom)
	client := e.clients[params.SourceChainID]
	tokenAddr := common.HexToAddress(params.AssetIn)
	userAddr := common.HexToAddress(params.User)

	log.WithFields(log.Fields{
		"user":          params.User,
		"token":         params.AssetIn,
		"totalAmountIn": params.TotalAmountIn,
		"chainIn":       chainIn,
	}).Info("Pulling full TWAP amount from user upfront")

	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
	transferErr := e.transferFromUser(ctx, client, params.SourceChainID, tokenAddr, userAddr, totalAmountIn)
	cancel()
	if transferErr != nil {
		return nil, fmt.Errorf("failed to pull funds from user: %w", transferErr)
	}

	log.WithFields(log.Fields{
		"amount": totalAmountIn.String(),
		"token":  params.AssetIn,
	}).Info("Successfully pulled all TWAP funds to TEE")

	orderID := fmt.Sprintf("twap-%s-%d", params.User[:10], time.Now().UnixNano())

	order := &types.TWAPOrder{
		ID:              orderID,
		User:            params.User,
		AssetIn:         params.AssetIn,
		AssetOut:        params.AssetOut,
		ChainIn:         chainIn,
		ChainOut:        chainOut,
		SourceChainID:   params.SourceChainID,
		DestChainID:     params.DestChainID,
		TotalAmountIn:   params.TotalAmountIn,
		AmountPerPeriod: params.AmountPerPeriod,
		Period:          params.Period,
		StartTime:       params.StartTime,
		EndTime:         params.EndTime,
		PermitSignature: params.PermitSignature,
		Recipients:      params.Recipients,
		Amounts:         params.Amounts,
		Status:          "active",
		TranchesFilled:  0,
		TotalTranches:   totalTranches,
		AmountSpent:     "0",
		CreatedAt:       time.Now(),
	}

	e.mu.Lock()
	e.orders[orderID] = order
	e.mu.Unlock()

	log.WithFields(log.Fields{
		"orderID":       orderID,
		"user":          params.User,
		"assetIn":       params.AssetIn,
		"assetOut":      params.AssetOut,
		"chainIn":       chainIn,
		"chainOut":      chainOut,
		"totalTranches": totalTranches,
		"period":        params.Period,
	}).Info("TWAP order created, starting periodic execution")

	go e.executeTWAP(order)

	return order, nil
}

// GetOrder returns an order by ID
func (e *Engine) GetOrder(orderID string) (*types.TWAPOrder, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	order, ok := e.orders[orderID]
	return order, ok
}

// CancelOrder cancels an active TWAP order
func (e *Engine) CancelOrder(orderID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	order, ok := e.orders[orderID]
	if !ok {
		return fmt.Errorf("order not found: %s", orderID)
	}
	if order.Status != "active" {
		return fmt.Errorf("order is not active: %s", order.Status)
	}
	order.Status = "cancelled"
	log.WithField("orderID", orderID).Info("TWAP order cancelled")
	return nil
}

// ListOrders returns all orders for a given user address
func (e *Engine) ListOrders(user string) []*types.TWAPOrder {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var result []*types.TWAPOrder
	for _, order := range e.orders {
		if strings.EqualFold(order.User, user) {
			result = append(result, order)
		}
	}
	return result
}

// executeTWAP runs the periodic execution loop for a TWAP order.
// Funds are already held by the TEE — each tick just approves bridge + creates intent.
func (e *Engine) executeTWAP(order *types.TWAPOrder) {
	now := time.Now().Unix()
	if order.StartTime > now {
		waitDuration := time.Duration(order.StartTime-now) * time.Second
		log.WithFields(log.Fields{
			"orderID": order.ID,
			"waitFor": waitDuration,
		}).Info("Waiting for TWAP start time")

		select {
		case <-time.After(waitDuration):
		case <-e.ctx.Done():
			return
		}
	}

	ticker := time.NewTicker(time.Duration(order.Period) * time.Second)
	defer ticker.Stop()

	// Execute first tranche immediately
	e.executeTranche(order)

	for {
		select {
		case <-ticker.C:
			e.mu.RLock()
			status := order.Status
			e.mu.RUnlock()

			if status != "active" {
				log.WithField("orderID", order.ID).Info("TWAP order no longer active, stopping")
				return
			}

			if time.Now().Unix() > order.EndTime {
				e.mu.Lock()
				order.Status = "completed"
				e.mu.Unlock()
				log.WithField("orderID", order.ID).Info("TWAP order completed (end time reached)")
				return
			}

			e.executeTranche(order)

		case <-e.ctx.Done():
			return
		}
	}
}

// executeTranche creates a single BridgeIntent from TEE-held funds
func (e *Engine) executeTranche(order *types.TWAPOrder) {
	e.mu.RLock()
	if order.TranchesFilled >= order.TotalTranches {
		e.mu.RUnlock()
		e.mu.Lock()
		order.Status = "completed"
		e.mu.Unlock()
		log.WithField("orderID", order.ID).Info("All tranches filled, TWAP complete")
		return
	}
	e.mu.RUnlock()

	amountPerPeriod, _ := new(big.Int).SetString(order.AmountPerPeriod, 10)
	sourceChainID := order.SourceChainID

	client := e.clients[sourceChainID]
	bridgeAddr := e.bridges[sourceChainID]

	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
	defer cancel()

	log.WithFields(log.Fields{
		"orderID":  order.ID,
		"tranche":  order.TranchesFilled + 1,
		"total":    order.TotalTranches,
		"amount":   amountPerPeriod.String(),
		"chainIn":  order.ChainIn,
		"chainOut": order.ChainOut,
	}).Info("Executing TWAP tranche")

	// Step 1: Approve bridge contract to spend this tranche from TEE balance
	tokenAddr := common.HexToAddress(order.AssetIn)
	err := e.approveToken(ctx, client, sourceChainID, tokenAddr, bridgeAddr, amountPerPeriod)
	if err != nil {
		log.WithError(err).WithField("orderID", order.ID).Error("Failed to approve bridge")
		return
	}

	// Step 2: Calculate reward (5% of tranche as solver incentive)
	reward := new(big.Int).Div(amountPerPeriod, big.NewInt(20))
	intentAmount := new(big.Int).Sub(amountPerPeriod, reward)

	// Step 3: Calculate expectedAmountB from recipient amounts
	expectedAmountB := e.calculateExpectedAmountB(order)

	// Step 4: Create intent on BridgeIntent contract
	intentID, err := e.createIntent(ctx, client, sourceChainID, bridgeAddr, order, intentAmount, expectedAmountB, reward)
	if err != nil {
		log.WithError(err).WithField("orderID", order.ID).Error("Failed to create intent")
		return
	}

	// Step 5: Store recipients in relayer (encrypted)
	err = e.storeRecipientsInRelayer(order, intentID, sourceChainID)
	if err != nil {
		log.WithError(err).WithField("orderID", order.ID).Warn("Failed to store recipients in relayer")
	}

	// Update order state
	e.mu.Lock()
	order.TranchesFilled++
	spent, _ := new(big.Int).SetString(order.AmountSpent, 10)
	spent.Add(spent, amountPerPeriod)
	order.AmountSpent = spent.String()
	order.LastExecutedAt = time.Now()
	e.mu.Unlock()

	log.WithFields(log.Fields{
		"orderID":  order.ID,
		"intentID": intentID,
		"tranche":  order.TranchesFilled,
		"total":    order.TotalTranches,
		"spent":    order.AmountSpent,
	}).Info("TWAP tranche executed successfully")
}

// --- On-chain helpers ---

func (e *Engine) getTransactOpts(ctx context.Context, client *ethclient.Client, chainID int64) (*bind.TransactOpts, error) {
	nonce, err := client.PendingNonceAt(ctx, e.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to suggest gas price: %w", err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(e.privateKey, e.chainIDs[chainID])
	if err != nil {
		return nil, fmt.Errorf("failed to create transactor: %w", err)
	}

	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(0)
	auth.GasLimit = 300000
	auth.GasPrice = gasPrice
	auth.Context = ctx

	return auth, nil
}

// retryTransaction retries a transaction function up to maxRetries times on nonce errors.
// Same pattern as solver/relayer — re-fetches nonce on each retry attempt.
func (e *Engine) retryTransaction(operation string, txFunc func() (*ethtypes.Transaction, error)) (*ethtypes.Transaction, error) {
	maxRetries := 10
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 500 * time.Millisecond
			log.WithFields(log.Fields{
				"operation": operation,
				"attempt":   attempt + 1,
				"backoff":   backoff,
			}).Warn("Retrying transaction after nonce conflict")
			time.Sleep(backoff)
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

	return nil, fmt.Errorf("%s failed after %d retries: %w", operation, maxRetries, lastErr)
}

// transferFromUser pulls tokens from user to TEE address (called once at order creation)
func (e *Engine) transferFromUser(ctx context.Context, client *ethclient.Client, chainID int64, token, user common.Address, amount *big.Int) error {
	parsedABI, _ := abi.JSON(strings.NewReader(erc20TransferFromABI))
	contract := bind.NewBoundContract(token, parsedABI, client, client, client)

	tx, err := e.retryTransaction("transferFrom", func() (*ethtypes.Transaction, error) {
		auth, err := e.getTransactOpts(ctx, client, chainID)
		if err != nil {
			return nil, err
		}
		return contract.Transact(auth, "transferFrom", user, e.address, amount)
	})
	if err != nil {
		return fmt.Errorf("transferFrom failed: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, client, tx)
	if err != nil {
		return fmt.Errorf("failed waiting for transferFrom: %w", err)
	}
	if receipt.Status == 0 {
		return fmt.Errorf("transferFrom reverted: %s", tx.Hash().Hex())
	}

	log.WithFields(log.Fields{
		"token":  token.Hex(),
		"amount": amount.String(),
		"from":   user.Hex(),
		"txHash": tx.Hash().Hex(),
	}).Info("Pulled funds from user to TEE")

	return nil
}

func (e *Engine) approveToken(ctx context.Context, client *ethclient.Client, chainID int64, token, spender common.Address, amount *big.Int) error {
	parsedABI, _ := abi.JSON(strings.NewReader(erc20TransferFromABI))
	contract := bind.NewBoundContract(token, parsedABI, client, client, client)

	tx, err := e.retryTransaction("approve", func() (*ethtypes.Transaction, error) {
		auth, err := e.getTransactOpts(ctx, client, chainID)
		if err != nil {
			return nil, err
		}
		return contract.Transact(auth, "approve", spender, amount)
	})
	if err != nil {
		return fmt.Errorf("approve failed: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, client, tx)
	if err != nil {
		return fmt.Errorf("failed waiting for approve: %w", err)
	}
	if receipt.Status == 0 {
		return fmt.Errorf("approve reverted: %s", tx.Hash().Hex())
	}

	return nil
}

func (e *Engine) createIntent(ctx context.Context, client *ethclient.Client, chainID int64, bridgeAddr common.Address, order *types.TWAPOrder, amountA, expectedAmountB, reward *big.Int) (string, error) {
	parsedABI, _ := abi.JSON(strings.NewReader(bridgeIntentABI))
	bridge := bind.NewBoundContract(bridgeAddr, parsedABI, client, client, client)

	auctionDuration := order.Period / 2
	if auctionDuration < 20 {
		auctionDuration = 20
	}

	tx, err := e.retryTransaction("createIntent", func() (*ethtypes.Transaction, error) {
		auth, err := e.getTransactOpts(ctx, client, chainID)
		if err != nil {
			return nil, err
		}
		return bridge.Transact(auth, "createIntent",
			common.HexToAddress(order.AssetIn),
			common.HexToAddress(order.AssetOut),
			amountA,
			expectedAmountB,
			reward,
			big.NewInt(order.DestChainID),
			big.NewInt(auctionDuration),
		)
	})
	if err != nil {
		return "", fmt.Errorf("createIntent failed: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, client, tx)
	if err != nil {
		return "", fmt.Errorf("failed waiting for createIntent: %w", err)
	}
	if receipt.Status == 0 {
		return "", fmt.Errorf("createIntent reverted: %s", tx.Hash().Hex())
	}

	// Get the intent ID by calling getLatestIntentId on the bridge contract
	var result []interface{}
	err = bridge.Call(&bind.CallOpts{Context: ctx}, &result, "getLatestIntentId")
	if err != nil {
		return "", fmt.Errorf("failed to get latest intent ID: %w", err)
	}
	intentID := result[0].(*big.Int).String()

	log.WithFields(log.Fields{
		"intentID": intentID,
		"txHash":   tx.Hash().Hex(),
		"amountA":  amountA.String(),
		"reward":   reward.String(),
	}).Info("Created BridgeIntent for TWAP tranche")

	return intentID, nil
}

func (e *Engine) calculateExpectedAmountB(order *types.TWAPOrder) *big.Int {
	total := big.NewInt(0)
	for _, amtStr := range order.Amounts {
		amt, ok := new(big.Int).SetString(amtStr, 10)
		if ok {
			total.Add(total, amt)
		}
	}
	return total
}

func (e *Engine) storeRecipientsInRelayer(order *types.TWAPOrder, intentID string, sourceChainID int64) error {
	recipientData := struct {
		Recipients []string `json:"recipients"`
		Amounts    []string `json:"amounts"`
	}{
		Recipients: order.Recipients,
		Amounts:    order.Amounts,
	}

	plaintext, err := json.Marshal(recipientData)
	if err != nil {
		return fmt.Errorf("failed to marshal recipients: %w", err)
	}

	encrypted, err := ecies.Encrypt(e.cfg.ECIESPublicKey, plaintext)
	if err != nil {
		return fmt.Errorf("failed to encrypt recipients: %w", err)
	}

	reqBody := types.StoreRecipientsRequest{
		IntentID:      intentID,
		ChainID:       sourceChainID,
		EncryptedData: hex.EncodeToString(encrypted),
	}

	bodyBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(e.relayerURL+"/store-recipients", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to store recipients: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("relayer returned status %d", resp.StatusCode)
	}

	return nil
}

// Close shuts down the TWAP engine
func (e *Engine) Close() {
	e.cancel()
	for _, client := range e.clients {
		client.Close()
	}
}
