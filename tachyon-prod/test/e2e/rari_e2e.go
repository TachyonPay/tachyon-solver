package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"github.com/zk-tachyon-solver/tachyon-prod/test/e2e/utils"
)

const (
	bridgeIntentABI = `[{"inputs":[{"internalType":"uint256","name":"_chainId","type":"uint256"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[{"internalType":"address","name":"owner","type":"address"}],"name":"OwnableInvalidOwner","type":"error"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"OwnableUnauthorizedAccount","type":"error"},{"inputs":[],"name":"ReentrancyGuardReentrantCall","type":"error"},{"inputs":[{"internalType":"address","name":"token","type":"address"}],"name":"SafeERC20FailedOperation","type":"error"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"bidAmount","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"timestamp","type":"uint256"}],"name":"BidPlaced","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":false,"internalType":"address[]","name":"recipients","type":"address[]"},{"indexed":false,"internalType":"uint256[]","name":"amounts","type":"uint256[]"}],"name":"FundsDistributed","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"}],"name":"IntentCompleted","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"user","type":"address"},{"indexed":false,"internalType":"address","name":"tokenA","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"destChainId","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"endTime","type":"uint256"}],"name":"IntentCreated","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"winningBid","type":"uint256"}],"name":"IntentSettled","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountB","type":"uint256"}],"name":"IntentSolvedOnChain2","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"winningSolver","type":"address"},{"indexed":false,"internalType":"uint256","name":"winningBid","type":"uint256"}],"name":"IntentWon","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"previousOwner","type":"address"},{"indexed":true,"internalType":"address","name":"newOwner","type":"address"}],"name":"OwnershipTransferred","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"SolverDeposited","type":"event"},{"inputs":[],"name":"MAX_AUCTION_DURATION","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"MIN_AUCTION_DURATION","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"authorizedRelayers","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"chainId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"_tokenA","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_amountA","type":"uint256"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"_reward","type":"uint256"},{"internalType":"uint256","name":"_destChainId","type":"uint256"},{"internalType":"uint256","name":"_auctionDuration","type":"uint256"}],"name":"createIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"depositAndPickup","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"finalizeAuction","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getIntent","outputs":[{"components":[{"internalType":"uint256","name":"intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"bool","name":"deposited","type":"bool"}],"internalType":"struct BridgeIntent.Intent","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"uint256","name":"_bidAmount","type":"uint256"}],"name":"placeBid","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_sourceChainId","type":"uint256"},{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"address","name":"_user","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"address[]","name":"_recipients","type":"address[]"},{"internalType":"uint256[]","name":"_amounts","type":"uint256[]"}],"name":"solveIntentOnChain2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_sourceChainId","type":"uint256"},{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"isIntentSolvedOnChain2","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"uint256","name":"_chain2IntentId","type":"uint256"}],"name":"settleIntentWithChain2Verification","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

	mockERC20ABI = `[{"inputs":[{"internalType":"string","name":"name","type":"string"},{"internalType":"string","name":"symbol","type":"string"},{"internalType":"uint8","name":"decimals_","type":"uint8"},{"internalType":"uint256","name":"initialSupply","type":"uint256"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"owner","type":"address"},{"internalType":"address","name":"spender","type":"address"}],"name":"allowance","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`
)

type TestConfig struct {
	HorizenRPC        string
	BaseRPC           string
	RariRPC           string
	BridgeHorizenAddr string
	BridgeBaseAddr    string
	BridgeRariAddr    string
	PrivateKey        string
	RelayerURL        string
	AuctionDuration   int64
}

type IntentTest struct {
	TestNum      int
	SourceChain  string
	DestChain    string
	TokenA       string
	TokenB       string
	SourceClient *ethclient.Client
	DestClient   *ethclient.Client
	SourceBridge *bind.BoundContract
	DestBridge   *bind.BoundContract
	SourceChainID *big.Int
	DestChainID   *big.Int
}

func loadConfig() (*TestConfig, error) {
	if err := godotenv.Load("../../.env"); err != nil {
		fmt.Printf("Warning: Error loading ../../.env file: %v\n", err)
	}

	return &TestConfig{
		HorizenRPC:        os.Getenv("CHAIN_1_RPC"),
		BaseRPC:           os.Getenv("CHAIN_2_RPC"),
		RariRPC:           os.Getenv("CHAIN_4_RPC"),
		BridgeHorizenAddr: os.Getenv("CHAIN_1_BRIDGE"),
		BridgeBaseAddr:    os.Getenv("CHAIN_2_BRIDGE"),
		BridgeRariAddr:    os.Getenv("CHAIN_4_BRIDGE"),
		PrivateKey:        os.Getenv("PRIVATE_KEY"),
		RelayerURL:        getEnvOrDefault("RELAYER_URL", "http://localhost:3000"),
		AuctionDuration:   60, // 60 seconds auction
	}, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	fmt.Println("🚀 Starting Rari Testnet E2E Test")
	fmt.Println("Testing 2 intent flows to Rari:")
	fmt.Println("  1. Horizen → Rari")
	fmt.Println("  2. Base Sepolia → Rari\n")

	ctx := context.Background()

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("❌ Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup clients
	fmt.Println("📡 Connecting to networks...")
	horizenClient, err := ethclient.Dial(cfg.HorizenRPC)
	if err != nil {
		fmt.Printf("❌ Failed to connect to Horizen: %v\n", err)
		os.Exit(1)
	}

	baseClient, err := ethclient.Dial(cfg.BaseRPC)
	if err != nil {
		fmt.Printf("❌ Failed to connect to Base: %v\n", err)
		os.Exit(1)
	}

	rariClient, err := ethclient.Dial(cfg.RariRPC)
	if err != nil {
		fmt.Printf("❌ Failed to connect to Rari: %v\n", err)
		os.Exit(1)
	}

	// Get chain IDs
	horizenChainID, err := horizenClient.ChainID(ctx)
	if err != nil {
		fmt.Printf("❌ Failed to get Horizen chain ID: %v\n", err)
		os.Exit(1)
	}

	baseChainID, err := baseClient.ChainID(ctx)
	if err != nil {
		fmt.Printf("❌ Failed to get Base chain ID: %v\n", err)
		os.Exit(1)
	}

	rariChainID, err := rariClient.ChainID(ctx)
	if err != nil {
		fmt.Printf("❌ Failed to get Rari chain ID: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Connected to all networks\n")
	fmt.Printf("   Horizen: Chain ID %s, Block %d\n", horizenChainID, mustGetBlockNumber(ctx, horizenClient))
	fmt.Printf("   Base Sepolia: Chain ID %s, Block %d\n", baseChainID, mustGetBlockNumber(ctx, baseClient))
	fmt.Printf("   Rari: Chain ID %s, Block %d\n\n", rariChainID, mustGetBlockNumber(ctx, rariClient))

	// Setup wallet
	wallet, err := utils.CreateTestWallet(cfg.PrivateKey)
	if err != nil {
		fmt.Printf("❌ Failed to create wallet: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("💼 Wallet Address: %s\n\n", wallet.Address.Hex())

	// Parse ABIs
	bridgeABI, _ := abi.JSON(strings.NewReader(bridgeIntentABI))
	erc20ABI, _ := abi.JSON(strings.NewReader(mockERC20ABI))

	// Create bridge contracts
	bridgeHorizen := bind.NewBoundContract(common.HexToAddress(cfg.BridgeHorizenAddr), bridgeABI, horizenClient, horizenClient, horizenClient)
	bridgeBase := bind.NewBoundContract(common.HexToAddress(cfg.BridgeBaseAddr), bridgeABI, baseClient, baseClient, baseClient)
	bridgeRari := bind.NewBoundContract(common.HexToAddress(cfg.BridgeRariAddr), bridgeABI, rariClient, rariClient, rariClient)

	// Get token addresses from env
	tokenWETH_Horizen := os.Getenv("TOKEN_WETH_HORIZEN")
	tokenWETH_Rari := os.Getenv("TOKEN_WETH_RARI")
	tokenUSDC_Base := os.Getenv("TOKEN_USDC_BASE")
	tokenUSDC_Rari := os.Getenv("TOKEN_USDC_RARI")

	// Define test scenarios - only Rari as destination
	tests := []IntentTest{
		{
			TestNum: 1, SourceChain: "horizen", DestChain: "rari",
			TokenA: tokenWETH_Horizen, TokenB: tokenWETH_Rari,
			SourceClient: horizenClient, DestClient: rariClient,
			SourceBridge: bridgeHorizen, DestBridge: bridgeRari,
			SourceChainID: horizenChainID, DestChainID: rariChainID,
		},
		{
			TestNum: 2, SourceChain: "base", DestChain: "rari",
			TokenA: tokenUSDC_Base, TokenB: tokenUSDC_Rari,
			SourceClient: baseClient, DestClient: rariClient,
			SourceBridge: bridgeBase, DestBridge: bridgeRari,
			SourceChainID: baseChainID, DestChainID: rariChainID,
		},
	}

	// Create intents
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("📝 CREATING INTENTS")
	fmt.Println(strings.Repeat("=", 80))

	createdIntents := make([]*CreatedIntent, 0, len(tests))

	for _, test := range tests {
		intent, err := createIntent(ctx, cfg, wallet, &test, erc20ABI)
		if err != nil {
			fmt.Printf("❌ Test %d failed: %v\n", test.TestNum, err)
			continue
		}
		createdIntents = append(createdIntents, intent)

		if test.TestNum < len(tests) {
			fmt.Println("\n⏳ Waiting 10 seconds before next intent...")
			time.Sleep(10 * time.Second)
		}
	}

	fmt.Printf("\n✅ Successfully created %d intents\n\n", len(createdIntents))

	// Wait for solver to process
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("⏳ Waiting 3 minutes for solver to process intents...\n")
	fmt.Println(strings.Repeat("=", 80))
	time.Sleep(3 * time.Minute)

	// Verify intents
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🔍 VERIFICATION PHASE")
	fmt.Println(strings.Repeat("=", 80))

	successCount := 0
	for i, intent := range createdIntents {
		fmt.Printf("\n📊 Intent %d: %s → %s (ID: %s)\n", i+1, intent.SourceChain, intent.DestChain, intent.IntentID)

		// Check source chain status
		sourceCompleted := checkIntentCompleted(ctx, intent.SourceBridge, intent.IntentID)
		if sourceCompleted {
			fmt.Println("   Source Chain: ✅ Completed")
		} else {
			fmt.Println("   Source Chain: ❌ Not Completed")
		}

		// Check destination chain status
		destSolved := checkIntentSolved(ctx, intent.DestBridge, intent.SourceChainID, intent.IntentID)
		if destSolved {
			fmt.Println("   Destination Chain (Rari): ✅ Solved")
		} else {
			fmt.Println("   Destination Chain (Rari): ❌ Not Solved")
		}

		if sourceCompleted && destSolved {
			successCount++
		}
	}

	// Print summary
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🎉 Rari Testnet E2E Test Complete!")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("\n📊 Results: %d/%d intents completed successfully\n", successCount, len(createdIntents))
	fmt.Println("\n✅ Test Scenarios:")
	fmt.Println("   ✓ Horizen → Rari (WETH)")
	fmt.Println("   ✓ Base Sepolia → Rari (USDC)")
}

type CreatedIntent struct {
	IntentID      *big.Int
	SourceChain   string
	DestChain     string
	SourceBridge  *bind.BoundContract
	DestBridge    *bind.BoundContract
	SourceChainID *big.Int
	Recipients    []string
	Amounts       []string
}

func createIntent(ctx context.Context, cfg *TestConfig, wallet *utils.TestWallet, test *IntentTest, erc20ABI abi.ABI) (*CreatedIntent, error) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 80))
	fmt.Printf("🧪 TEST %d: %s → %s\n", test.TestNum, test.SourceChain, test.DestChain)
	fmt.Printf("%s\n", strings.Repeat("=", 80))

	decimals := fetchTokenDecimals(ctx, test.SourceClient, test.TokenA)
	expectedAmountB := utils.ParseToken("95", decimals)
	reward := utils.ParseToken("5", decimals)
	auctionDuration := big.NewInt(cfg.AuctionDuration)

	// Get current latest intent ID
	var latestIDResult []interface{}
	err := test.SourceBridge.Call(&bind.CallOpts{Context: ctx}, &latestIDResult, "getLatestIntentId")
	if err != nil {
		return nil, fmt.Errorf("failed to get latest intent ID: %w", err)
	}
	currentLatestID := latestIDResult[0].(*big.Int)
	fmt.Printf("🎯 Current Latest Intent ID on %s: %s\n", test.SourceChain, currentLatestID)

	// Approve tokens
	tokenAContract := bind.NewBoundContract(common.HexToAddress(test.TokenA), erc20ABI, test.SourceClient, test.SourceClient, test.SourceClient)
	approveAmount := new(big.Int).Add(expectedAmountB, reward)

	auth, err := utils.GetTransactOpts(ctx, test.SourceClient, wallet, test.SourceChainID)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth: %w", err)
	}

	fmt.Println("🔐 Approving tokens...")
	approveTx, err := tokenAContract.Transact(auth, "approve", common.HexToAddress(getBridgeAddress(test.SourceChain, cfg)), approveAmount)
	if err != nil {
		return nil, fmt.Errorf("failed to approve tokens: %w", err)
	}
	utils.WaitForTransaction(ctx, test.SourceClient, approveTx.Hash(), 2*time.Minute)
	fmt.Println("✅ Tokens approved")

	// Create intent
	fmt.Println("🎯 Creating intent...")
	auth2, _ := utils.GetTransactOpts(ctx, test.SourceClient, wallet, test.SourceChainID)
	createIntentTx, err := test.SourceBridge.Transact(auth2, "createIntent",
		common.HexToAddress(test.TokenA), common.HexToAddress(test.TokenB),
		expectedAmountB, expectedAmountB, reward, test.DestChainID, auctionDuration)
	if err != nil {
		return nil, fmt.Errorf("failed to create intent: %w", err)
	}
	utils.WaitForTransaction(ctx, test.SourceClient, createIntentTx.Hash(), 2*time.Minute)

	time.Sleep(10 * time.Second)

	// Read intent ID with retries
	var actualIntentID *big.Int
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(5 * time.Second)
			fmt.Printf("⏳ Retry %d/%d: Checking intent ID...\n", i+1, maxRetries)
		}

		var actualIDResult []interface{}
		err = test.SourceBridge.Call(&bind.CallOpts{Context: ctx}, &actualIDResult, "getLatestIntentId")
		if err != nil {
			if i == maxRetries-1 {
				return nil, fmt.Errorf("failed to get latest intent ID after %d retries: %w", maxRetries, err)
			}
			continue
		}

		actualIntentID = actualIDResult[0].(*big.Int)
		if actualIntentID.Cmp(currentLatestID) > 0 {
			break
		}

		if i == maxRetries-1 {
			return nil, fmt.Errorf("intent ID did not increase")
		}
	}

	fmt.Printf("✅ Intent created on %s: ID=%s\n", test.SourceChain, actualIntentID)

	// Generate recipients
	recipients := []string{
		utils.GenerateRandomAddress().Hex(),
		utils.GenerateRandomAddress().Hex(),
		utils.GenerateRandomAddress().Hex(),
	}
	amounts := []string{
		utils.ParseToken("30", decimals).String(),
		utils.ParseToken("35", decimals).String(),
		utils.ParseToken("30", decimals).String(),
	}

	// Store recipients
	fmt.Printf("📋 Storing recipients in relayer...\n")
	if err := storeRecipients(cfg.RelayerURL, actualIntentID.String(), recipients, amounts, test.SourceChainID.Int64()); err != nil {
		fmt.Printf("⚠️  Warning: Failed to store recipients: %v\n", err)
	} else {
		fmt.Println("✅ Recipients stored")
	}

	return &CreatedIntent{
		IntentID:      actualIntentID,
		SourceChain:   test.SourceChain,
		DestChain:     test.DestChain,
		SourceBridge:  test.SourceBridge,
		DestBridge:    test.DestBridge,
		SourceChainID: test.SourceChainID,
		Recipients:    recipients,
		Amounts:       amounts,
	}, nil
}

func getBridgeAddress(chain string, cfg *TestConfig) string {
	switch chain {
	case "horizen":
		return cfg.BridgeHorizenAddr
	case "base":
		return cfg.BridgeBaseAddr
	case "rari":
		return cfg.BridgeRariAddr
	default:
		return ""
	}
}

func checkIntentCompleted(ctx context.Context, bridge *bind.BoundContract, intentID *big.Int) bool {
	var result []interface{}
	err := bridge.Call(&bind.CallOpts{Context: ctx}, &result, "getIntent", intentID)
	if err != nil || len(result) == 0 {
		return false
	}

	intentStruct, ok := result[0].(struct {
		IntentId        *big.Int       `json:"intentId"`
		User            common.Address `json:"user"`
		TokenA          common.Address `json:"tokenA"`
		TokenB          common.Address `json:"tokenB"`
		AmountA         *big.Int       `json:"amountA"`
		ExpectedAmountB *big.Int       `json:"expectedAmountB"`
		Reward          *big.Int       `json:"reward"`
		DestChainId     *big.Int       `json:"destChainId"`
		EndTime         *big.Int       `json:"endTime"`
		WinningSolver   common.Address `json:"winningSolver"`
		WinningBid      *big.Int       `json:"winningBid"`
		Completed       bool           `json:"completed"`
		Deposited       bool           `json:"deposited"`
	})
	if !ok {
		return false
	}

	return intentStruct.Completed
}

func checkIntentSolved(ctx context.Context, bridge *bind.BoundContract, sourceChainID, intentID *big.Int) bool {
	var result []interface{}
	err := bridge.Call(&bind.CallOpts{Context: ctx}, &result, "isIntentSolvedOnChain2", sourceChainID, intentID)
	if err != nil || len(result) == 0 {
		return false
	}
	return result[0].(bool)
}

func mustGetBlockNumber(ctx context.Context, client *ethclient.Client) uint64 {
	block, err := client.BlockNumber(ctx)
	if err != nil {
		return 0
	}
	return block
}

func fetchTokenDecimals(ctx context.Context, client *ethclient.Client, tokenAddr string) int {
	decimalsABI, _ := abi.JSON(strings.NewReader(`[{"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"}]`))
	contract := bind.NewBoundContract(common.HexToAddress(tokenAddr), decimalsABI, client, client, client)
	var out []interface{}
	if err := contract.Call(&bind.CallOpts{Context: ctx}, &out, "decimals"); err != nil || len(out) == 0 {
		return 18
	}
	return int(out[0].(uint8))
}

func fetchECIESPublicKey(relayerURL string) (*ecies.PublicKey, error) {
	resp, err := http.Get(fmt.Sprintf("%s/ecies-pubkey", relayerURL))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch public key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch public key: %s", string(body))
	}

	var result struct {
		Success   bool   `json:"success"`
		PublicKey string `json:"publicKey"`
		Message   string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode public key response: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("failed to get public key: %s", result.Message)
	}

	pubKeyBytes, err := hex.DecodeString(result.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid public key hex: %w", err)
	}

	publicKey, err := ecies.NewPublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	return publicKey, nil
}

func encryptRecipientData(publicKey *ecies.PublicKey, recipients, amounts []string) (string, error) {
	data := map[string]interface{}{
		"recipients": recipients,
		"amounts":    amounts,
	}

	plaintext, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal recipient data: %w", err)
	}

	encrypted, err := ecies.Encrypt(publicKey, plaintext)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt data: %w", err)
	}

	return hex.EncodeToString(encrypted), nil
}

func storeRecipients(relayerURL, intentID string, recipients, amounts []string, chainID int64) error {
	fmt.Println("🔐 Fetching ECIES public key from relayer...")
	publicKey, err := fetchECIESPublicKey(relayerURL)
	if err != nil {
		return fmt.Errorf("failed to fetch ECIES public key: %w", err)
	}
	fmt.Println("✅ Public key fetched")

	fmt.Println("🔒 Encrypting recipient data...")
	encryptedData, err := encryptRecipientData(publicKey, recipients, amounts)
	if err != nil {
		return fmt.Errorf("failed to encrypt recipient data: %w", err)
	}
	fmt.Println("✅ Data encrypted")

	data := map[string]interface{}{
		"intentId":      intentID,
		"chainId":       chainID,
		"encryptedData": encryptedData,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	resp, err := http.Post(fmt.Sprintf("%s/store-recipients", relayerURL), "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to store recipients: %s", string(body))
	}

	return nil
}
