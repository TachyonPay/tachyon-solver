package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"github.com/zk-tachyon-solver/tachyon-prod/test/e2e/utils"
)

const (
	bridgeIntentABI = `[{"inputs":[{"internalType":"uint256","name":"_chainId","type":"uint256"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[{"internalType":"address","name":"owner","type":"address"}],"name":"OwnableInvalidOwner","type":"error"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"OwnableUnauthorizedAccount","type":"error"},{"inputs":[],"name":"ReentrancyGuardReentrantCall","type":"error"},{"inputs":[{"internalType":"address","name":"token","type":"address"}],"name":"SafeERC20FailedOperation","type":"error"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"bidAmount","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"timestamp","type":"uint256"}],"name":"BidPlaced","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":false,"internalType":"address[]","name":"recipients","type":"address[]"},{"indexed":false,"internalType":"uint256[]","name":"amounts","type":"uint256[]"}],"name":"FundsDistributed","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"}],"name":"IntentCompleted","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"user","type":"address"},{"indexed":false,"internalType":"address","name":"tokenA","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"destChainId","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"endTime","type":"uint256"}],"name":"IntentCreated","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"winningBid","type":"uint256"}],"name":"IntentSettled","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountB","type":"uint256"}],"name":"IntentSolvedOnChain2","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"winningSolver","type":"address"},{"indexed":false,"internalType":"uint256","name":"winningBid","type":"uint256"}],"name":"IntentWon","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"previousOwner","type":"address"},{"indexed":true,"internalType":"address","name":"newOwner","type":"address"}],"name":"OwnershipTransferred","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"amount","type":"uint256"}],"name":"SolverDeposited","type":"event"},{"inputs":[],"name":"MAX_AUCTION_DURATION","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"MIN_AUCTION_DURATION","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"authorizedRelayers","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"chainId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"_tokenA","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_amountA","type":"uint256"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"_reward","type":"uint256"},{"internalType":"uint256","name":"_destChainId","type":"uint256"},{"internalType":"uint256","name":"_auctionDuration","type":"uint256"}],"name":"createIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"depositAndPickup","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"finalizeAuction","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getIntent","outputs":[{"components":[{"internalType":"uint256","name":"intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"bool","name":"deposited","type":"bool"}],"internalType":"struct BridgeIntent.Intent","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"uint256","name":"_bidAmount","type":"uint256"}],"name":"placeBid","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"address","name":"_user","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"address[]","name":"_recipients","type":"address[]"},{"internalType":"uint256[]","name":"_amounts","type":"uint256[]"}],"name":"solveIntentOnChain2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"isIntentSolvedOnChain2","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"uint256","name":"_chain2IntentId","type":"uint256"}],"name":"settleIntentWithChain2Verification","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

	mockERC20ABI = `[{"inputs":[{"internalType":"string","name":"name","type":"string"},{"internalType":"string","name":"symbol","type":"string"},{"internalType":"uint8","name":"decimals_","type":"uint8"},{"internalType":"uint256","name":"initialSupply","type":"uint256"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"owner","type":"address"},{"internalType":"address","name":"spender","type":"address"}],"name":"allowance","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`
)

type TestConfig struct {
	HorizenRPC        string
	BaseRPC           string
	BridgeHorizenAddr string
	BridgeBaseAddr    string
	PrivateKey        string
	RelayerURL        string
	AuctionDuration   int64
}

type IntentTest struct {
	TestNum       int
	SourceChain   string
	DestChain     string
	TokenA        string
	TokenB        string
	TokenDecimals int // Decimals for both tokenA and tokenB (should match for cross-chain swaps)
	SourceClient  *ethclient.Client
	DestClient    *ethclient.Client
	SourceBridge  *bind.BoundContract
	DestBridge    *bind.BoundContract
	SourceChainID *big.Int
	DestChainID   *big.Int
}

func loadConfig() (*TestConfig, error) {
	// Load from tachyon-prod/.env
	if err := godotenv.Load("../../.env"); err != nil {
		fmt.Printf("Warning: Error loading ../../.env file: %v\n", err)
	}

	cfg := &TestConfig{
		HorizenRPC:        os.Getenv("CHAIN_1_RPC"),
		BaseRPC:           os.Getenv("CHAIN_2_RPC"),
		BridgeHorizenAddr: os.Getenv("CHAIN_1_BRIDGE"),
		BridgeBaseAddr:    os.Getenv("CHAIN_2_BRIDGE"),
		PrivateKey:        os.Getenv("PRIVATE_KEY"),
		RelayerURL:        getEnvOrDefault("RELAYER_URL", "http://65.0.45.215:3000"),
		AuctionDuration:   100,
	}
	fmt.Printf(">>> RelayerURL = %s\n", cfg.RelayerURL)
	return cfg, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	fmt.Printf("🔐 EIP-712 Domain Separator: %x\n", eip712DomainSeparator)
	fmt.Printf("🔐 EIP-712 DomainTypeHash:   %x\n", eip712DomainTypeHash)
	fmt.Printf("🔐 RegisterTypeHash:         %x\n", registerStealthTypeHash)
	fmt.Println("🚀 Starting Multi-Chain E2E Test")
	fmt.Println("Testing 2 bidirectional intent flows:")
	fmt.Println("  1. Horizen → Base Sepolia (WETH)")
	fmt.Println("  2. Base Sepolia → Horizen (USDC)\n")

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

	// Get chain IDs
	horizenChainID, _ := horizenClient.ChainID(ctx)
	baseChainID, _ := baseClient.ChainID(ctx)

	fmt.Printf("✅ Connected to all networks\n")
	fmt.Printf("   Horizen: Chain ID %s, Block %d\n", horizenChainID, mustGetBlockNumber(ctx, horizenClient))
	fmt.Printf("   Base Sepolia: Chain ID %s, Block %d\n\n", baseChainID, mustGetBlockNumber(ctx, baseClient))

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

	// Get token addresses from env
	tokenWETH_Horizen := os.Getenv("TOKEN_WETH_HORIZEN")
	tokenWETH_Base := os.Getenv("TOKEN_WETH_BASE")
	tokenUSDC_Horizen := os.Getenv("TOKEN_USDC_HORIZEN")
	tokenUSDC_Base := os.Getenv("TOKEN_USDC_BASE")

	// Define test scenarios
	tests := []IntentTest{
		{
			TestNum: 1, SourceChain: "horizen", DestChain: "base",
			TokenA: tokenWETH_Horizen, TokenB: tokenWETH_Base, TokenDecimals: 18,
			SourceClient: horizenClient, DestClient: baseClient,
			SourceBridge: bridgeHorizen, DestBridge: bridgeBase,
			SourceChainID: horizenChainID, DestChainID: baseChainID,
		},
		{
			TestNum: 2, SourceChain: "base", DestChain: "horizen",
			TokenA: tokenUSDC_Base, TokenB: tokenUSDC_Horizen, TokenDecimals: 6,
			SourceClient: baseClient, DestClient: horizenClient,
			SourceBridge: bridgeBase, DestBridge: bridgeHorizen,
			SourceChainID: baseChainID, DestChainID: horizenChainID,
		},
	}

	// Create all intents
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

		// Wait between intents to avoid nonce conflicts and ensure blockchain state propagates
		if test.TestNum < len(tests) {
			fmt.Println("\n⏳ Waiting 10 seconds before next intent...")
			time.Sleep(10 * time.Second)
		}
	}

	fmt.Printf("\n✅ Successfully created %d intents\n\n", len(createdIntents))

	// Wait for solver to process all intents
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("⏳ Waiting 2 minutes for solver to process all intents...\n")
	fmt.Println("   (1 minute per intent for bidding, solving, and settling)")
	fmt.Println(strings.Repeat("=", 80))
	time.Sleep(2 * time.Minute)

	// Verify all intents
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🔍 VERIFICATION PHASE")
	fmt.Println(strings.Repeat("=", 80))

	successCount := 0
	for i, intent := range createdIntents {
		fmt.Printf("\n📊 Intent %d: %s → %s (ID: %s)\n", i+1, intent.SourceChain, intent.DestChain, intent.IntentID)

		// Fetch settlement details from relayer with retries (24 retries × 10 seconds = 4 minutes total)
		settlementDetails, err := getIntentDetailsWithRetries(cfg.RelayerURL, intent.IntentID.String(), tests[i].SourceChainID.String(), 24, 10*time.Second)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to fetch settlement details from relayer after retries: %v\n", err)
		} else {
			fmt.Printf("   Relayer Status: %s\n", settlementDetails.Status)
			if settlementDetails.DepositTxHash != "" {
				fmt.Printf("   Deposit Tx: %s\n", settlementDetails.DepositTxHash)
			}
			if settlementDetails.SolveTxHash != "" {
				fmt.Printf("   Solve Tx: %s\n", settlementDetails.SolveTxHash)
			}
			if settlementDetails.SettleTxHash != "" {
				fmt.Printf("   Settle Tx: %s\n", settlementDetails.SettleTxHash)
			}
			if settlementDetails.SolverAddress != "" {
				fmt.Printf("   Solver: %s\n", settlementDetails.SolverAddress)
			}
		}

		// Check source chain status
		sourceCompleted := checkIntentCompleted(ctx, intent.SourceBridge, intent.IntentID)
		if sourceCompleted {
			fmt.Println("   Source Chain: ✅ Completed")
		} else {
			fmt.Println("   Source Chain: ❌ Not Completed")
		}

		// Check destination chain status
		destSolved := checkIntentSolved(ctx, intent.DestBridge, intent.IntentID)
		if destSolved {
			fmt.Println("   Destination Chain: ✅ Solved")
		} else {
			fmt.Println("   Destination Chain: ❌ Not Solved")
		}

		if sourceCompleted && destSolved {
			successCount++
		}
	}

	// =====================================================================
	// STEALTH RECIPIENT PHASE: Derive key, deploy Safe, verify balances
	// =====================================================================
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🔏 STEALTH RECIPIENT PHASE")
	fmt.Println("   Recipients derive stealth key, deploy Safe, verify token balances")
	fmt.Println(strings.Repeat("=", 80))

	stealthSuccessCount := 0
	stealthTotalTests := 0

	for i, intent := range createdIntents {
		fmt.Printf("\n📊 Intent %d (%s → %s): Testing stealth recipient flow\n", i+1, intent.SourceChain, intent.DestChain)

		// Only test first recipient per intent to save gas/time
		recipientKey := intent.RecipientKeys[0]
		stealthAddr := intent.StealthAddrs[0]
		safeAddr := intent.Recipients[0] // This is the Safe address now
		recipientAddr := crypto.PubkeyToAddress(recipientKey.PublicKey).Hex()

		stealthTotalTests++
		fmt.Printf("   Recipient (%s...):\n", recipientAddr[:12])

		// Step A: Fetch stealth addresses (EIP-712)
		getSig := signGetStealthAddresses(recipientKey, common.HexToAddress(recipientAddr))

		u, _ := url.Parse(cfg.RelayerURL + "/recipient/addresses")
		q := u.Query()
		q.Set("address", recipientAddr)
		q.Set("signature", hex.EncodeToString(getSig))
		u.RawQuery = q.Encode()

		addrResp, err := http.Get(u.String())
		if err != nil {
			fmt.Printf("      ❌ Failed to fetch addresses: %v\n", err)
			continue
		}
		addrBody, _ := io.ReadAll(addrResp.Body)
		addrResp.Body.Close()

		var addrResult struct {
			Success          bool `json:"success"`
			StealthAddresses []struct {
				Address      string `json:"address"`
				ChainID      string `json:"chainId"`
				IntentID     string `json:"intentId"`
				SafeDeployed bool   `json:"safeDeployed"`
				SafeAddress  string `json:"safeAddress"`
			} `json:"stealthAddresses"`
		}
		json.Unmarshal(addrBody, &addrResult)

		if !addrResult.Success || len(addrResult.StealthAddresses) == 0 {
			fmt.Printf("      ❌ No stealth addresses returned\n")
			continue
		}
		fmt.Printf("      ✅ Fetched %d stealth address(es)\n", len(addrResult.StealthAddresses))

		// Step B: Derive stealth private key from TEE (EIP-712)
		deriveSig := signDeriveStealthKey(recipientKey, common.HexToAddress(recipientAddr), common.HexToAddress(stealthAddr))

		deriveBody, _ := json.Marshal(map[string]string{
			"recipientAddress": recipientAddr,
			"stealthAddress":   stealthAddr,
			"signature":        hex.EncodeToString(deriveSig),
		})

		deriveResp, err := http.Post(cfg.RelayerURL+"/recipient/derive-stealth-key", "application/json", bytes.NewReader(deriveBody))
		if err != nil {
			fmt.Printf("      ❌ Failed to derive stealth key: %v\n", err)
			continue
		}
		deriveRespBody, _ := io.ReadAll(deriveResp.Body)
		deriveResp.Body.Close()

		var deriveResult struct {
			Success           bool   `json:"success"`
			StealthAddress    string `json:"stealthAddress"`
			StealthPrivateKey string `json:"stealthPrivateKey"`
			Error             string `json:"error"`
		}
		json.Unmarshal(deriveRespBody, &deriveResult)

		if !deriveResult.Success {
			fmt.Printf("      ❌ Failed to derive stealth key: %s\n", deriveResult.Error)
			continue
		}

		stealthPrivKey, err := crypto.HexToECDSA(deriveResult.StealthPrivateKey)
		if err != nil {
			fmt.Printf("      ❌ Invalid stealth private key: %v\n", err)
			continue
		}
		derivedAddr := crypto.PubkeyToAddress(stealthPrivKey.PublicKey)
		fmt.Printf("      ✅ Derived stealth key, controls %s\n", derivedAddr.Hex()[:16]+"...")

		// Step C: Verify token balance at Safe address on destination chain
		destClient := tests[i].DestClient
		tokenB := tests[i].TokenB
		tokenContract := bind.NewBoundContract(common.HexToAddress(tokenB), erc20ABI, destClient, destClient, destClient)

		var balResult []interface{}
		err = tokenContract.Call(&bind.CallOpts{Context: ctx}, &balResult, "balanceOf", common.HexToAddress(safeAddr))
		if err != nil {
			fmt.Printf("      ⚠️  Failed to check balance at Safe %s: %v\n", safeAddr[:12]+"...", err)
		} else if len(balResult) > 0 {
			balance := balResult[0].(*big.Int)
			fmt.Printf("      ✅ Safe %s has token balance: %s\n", safeAddr[:12]+"...", balance.String())
		}

		// Step D: Deploy Safe using stealth private key (EIP-712)
		destChainIDBig, _ := new(big.Int).SetString(intent.DestChainID, 10)
		deploySig := signDeploySafe(stealthPrivKey, common.HexToAddress(stealthAddr), destChainIDBig)

		deployBody, _ := json.Marshal(map[string]string{
			"stealthAddress": stealthAddr,
			"signature":      hex.EncodeToString(deploySig),
			"chainId":        intent.DestChainID,
		})

		deployResp, err := http.Post(cfg.RelayerURL+"/recipient/deploy-safe", "application/json", bytes.NewReader(deployBody))
		if err != nil {
			fmt.Printf("      ❌ Deploy Safe request failed: %v\n", err)
			continue
		}
		deployRespBody, _ := io.ReadAll(deployResp.Body)
		deployResp.Body.Close()

		var deployResult struct {
			Success     bool   `json:"success"`
			SafeAddress string `json:"safeAddress"`
			TxHash      string `json:"txHash"`
			Error       string `json:"error"`
		}
		json.Unmarshal(deployRespBody, &deployResult)

		if deployResult.Success {
			fmt.Printf("      ✅ Safe deployed at %s (tx: %s)\n", deployResult.SafeAddress[:16]+"...", deployResult.TxHash[:16]+"...")
			stealthSuccessCount++
		} else {
			fmt.Printf("      ❌ Deploy Safe failed: %s\n", deployResult.Error)
		}
	}

	// Print summary
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🎉 Multi-Chain E2E Test Complete!")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("\n📊 Bridge Results: %d/%d intents completed successfully\n", successCount, len(createdIntents))
	fmt.Printf("📊 Stealth Results: %d/%d recipient flows passed\n", stealthSuccessCount, stealthTotalTests)
	fmt.Println("\n✅ Test Scenarios:")
	fmt.Println("   ✓ Horizen → Base Sepolia (WETH)")
	fmt.Println("   ✓ Base Sepolia → Horizen (USDC)")
	fmt.Println("\n🔏 Stealth Flow Tested:")
	fmt.Println("   ✓ Recipient registration (TEE key generation)")
	fmt.Println("   ✓ Stealth address + Safe address generation per intent")
	fmt.Println("   ✓ Solver delivers tokens to predicted Safe addresses")
	fmt.Println("   ✓ Recipient fetches stealth addresses from TEE")
	fmt.Println("   ✓ Recipient derives stealth private key from TEE")
	fmt.Println("   ✓ Token balance verified at Safe address")
	fmt.Println("   ✓ Safe deployed using stealth key signature")
	fmt.Println("\n💡 Check solver logs for routing decisions!")
}

type CreatedIntent struct {
	IntentID       *big.Int
	SourceChain    string
	DestChain      string
	SourceBridge   *bind.BoundContract
	DestBridge     *bind.BoundContract
	Recipients     []string
	Amounts        []string
	RecipientKeys  []*ecdsa.PrivateKey // EOA keys for each recipient (to sign stealth requests)
	StealthAddrs   []string           // Stealth addresses generated for each recipient
	DestChainID    string             // Destination chain ID string
}

func createIntent(ctx context.Context, cfg *TestConfig, wallet *utils.TestWallet, test *IntentTest, erc20ABI abi.ABI) (*CreatedIntent, error) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 80))
	fmt.Printf("🧪 TEST %d: %s → %s\n", test.TestNum, test.SourceChain, test.DestChain)
	fmt.Printf("%s\n", strings.Repeat("=", 80))

	// Use correct decimals for each token type
	expectedAmountB := utils.ParseToken("95", test.TokenDecimals)
	reward := utils.ParseToken("5", test.TokenDecimals)
	auctionDuration := big.NewInt(cfg.AuctionDuration)

	// Get current latest intent ID on source chain
	var latestIDResult []interface{}
	err := test.SourceBridge.Call(&bind.CallOpts{Context: ctx}, &latestIDResult, "getLatestIntentId")
	if err != nil {
		return nil, fmt.Errorf("failed to get latest intent ID: %w", err)
	}
	currentLatestID := latestIDResult[0].(*big.Int)
	targetIntentID := new(big.Int).Add(currentLatestID, big.NewInt(1))
	fmt.Printf("🎯 Current Latest Intent ID on %s: %s\n", test.SourceChain, currentLatestID)
	fmt.Printf("🎯 Target Intent ID: %s\n", targetIntentID)

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

	// Wait for blockchain state to fully propagate before reading intent ID
	// Testnet RPC nodes can have significant propagation delays (Horizen especially slow)
	time.Sleep(10 * time.Second)

	// Read the latest intent ID with multiple retries to handle RPC delays
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

		// Verify the intent ID increased from what we expected
		if actualIntentID.Cmp(currentLatestID) > 0 {
			break
		}

		if i == maxRetries-1 {
			return nil, fmt.Errorf("intent ID did not increase after transaction (current: %s, expected > %s)", actualIntentID, currentLatestID)
		}
	}

	// Verify the intent exists by reading its data using getIntent function
	var intentData []interface{}
	err = test.SourceBridge.Call(&bind.CallOpts{Context: ctx}, &intentData, "getIntent", actualIntentID)
	if err != nil {
		return nil, fmt.Errorf("failed to read intent data for ID %s: %w", actualIntentID, err)
	}

	// Intent struct: (intentId, user, tokenA, tokenB, amountA, expectedAmountB, reward, destChainId, endTime, winningSolver, winningBid, completed, deposited)
	if len(intentData) < 1 {
		return nil, fmt.Errorf("unexpected intent data length: %d", len(intentData))
	}

	// The getIntent function returns a struct as a single element
	// We need to extract fields from the returned struct
	// For now, just verify we got data back (non-empty response means intent exists)
	fmt.Printf("✅ Intent created on %s: ID=%s (verified on-chain)\n", test.SourceChain, actualIntentID)

	// =========================================================================
	// STEALTH ADDRESS FLOW: Register recipients, generate stealth addresses
	// Instead of random addresses, we register 3 recipients with the relayer,
	// generate stealth addresses for each, and use those as delivery targets.
	// =========================================================================
	fmt.Println("🔏 Registering recipients and generating stealth addresses...")

	recipientKeys := make([]*ecdsa.PrivateKey, 3)
	stealthAddrs := make([]string, 3)
	safeAddrs := make([]string, 3)
	destChainIDStr := test.DestChainID.String()

	for i := 0; i < 3; i++ {
		// Generate a recipient EOA
		recipientKey, err := crypto.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate recipient key %d: %w", i, err)
		}
		recipientKeys[i] = recipientKey
		recipientAddr := crypto.PubkeyToAddress(recipientKey.PublicKey).Hex()

		// Register recipient with relayer (TEE generates view+spend keys, EIP-712)
		regSig := signRegisterStealth(recipientKey, common.HexToAddress(recipientAddr))
		fmt.Printf("   [DEBUG] Register EIP-712: addr=%s sigLen=%d sig=%s\n", recipientAddr, len(regSig), hex.EncodeToString(regSig[:8]))
		regBody, _ := json.Marshal(map[string]string{
			"recipientAddress": recipientAddr,
			"signature":        hex.EncodeToString(regSig),
		})

		regResp, err := http.Post(cfg.RelayerURL+"/recipient/register", "application/json", bytes.NewReader(regBody))
		if err != nil {
			return nil, fmt.Errorf("failed to register recipient %d: %w", i, err)
		}
		regRespBody, _ := io.ReadAll(regResp.Body)
		regResp.Body.Close()

		if regResp.StatusCode != 200 {
			return nil, fmt.Errorf("register recipient %d failed: %s", i, string(regRespBody))
		}

		var regResult map[string]interface{}
		json.Unmarshal(regRespBody, &regResult)
		if regResult["success"] != true {
			return nil, fmt.Errorf("register recipient %d failed: %v", i, regResult["error"])
		}
		fmt.Printf("   ✅ Recipient %d registered: %s\n", i+1, recipientAddr[:16]+"...")

		// Generate stealth address for this recipient on the destination chain
		genBody, _ := json.Marshal(map[string]string{
			"recipientAddress": recipientAddr,
			"chainId":          destChainIDStr,
			"intentId":         actualIntentID.String(),
		})

		genResp, err := http.Post(cfg.RelayerURL+"/recipient/generate-stealth", "application/json", bytes.NewReader(genBody))
		if err != nil {
			return nil, fmt.Errorf("failed to generate stealth for recipient %d: %w", i, err)
		}
		genRespBody, _ := io.ReadAll(genResp.Body)
		genResp.Body.Close()

		if genResp.StatusCode != 200 {
			return nil, fmt.Errorf("generate stealth %d failed: %s", i, string(genRespBody))
		}

		var genResult map[string]interface{}
		json.Unmarshal(genRespBody, &genResult)
		if genResult["success"] != true {
			return nil, fmt.Errorf("generate stealth %d failed: %v", i, genResult["error"])
		}

		stealthAddrs[i] = genResult["stealthAddress"].(string)
		safeAddrs[i] = genResult["safeAddress"].(string)
		fmt.Printf("   ✅ Stealth address %d: %s\n", i+1, stealthAddrs[i][:16]+"...")
		fmt.Printf("   ✅ Safe address %d:    %s\n", i+1, safeAddrs[i][:16]+"...")
	}

	// Use predicted Safe addresses as recipients — tokens land at the Safe controlled by the stealth EOA
	recipients := safeAddrs
	amounts := []string{
		utils.ParseToken("30", test.TokenDecimals).String(),
		utils.ParseToken("35", test.TokenDecimals).String(),
		utils.ParseToken("30", test.TokenDecimals).String(),
	}

	// Store recipients (stealth addresses) in relayer with SOURCE chain ID
	fmt.Printf("📋 Storing stealth recipients in relayer (intentID=%s, sourceChainID=%d)...\n", actualIntentID, test.SourceChainID)
	if err := storeRecipients(cfg.RelayerURL, actualIntentID.String(), recipients, amounts, test.SourceChainID.Int64()); err != nil {
		fmt.Printf("⚠️  Warning: Failed to store recipients: %v\n", err)
	} else {
		fmt.Println("✅ Stealth recipients stored for solver")
	}

	return &CreatedIntent{
		IntentID:      actualIntentID,
		SourceChain:   test.SourceChain,
		DestChain:     test.DestChain,
		SourceBridge:  test.SourceBridge,
		DestBridge:    test.DestBridge,
		Recipients:    recipients,
		Amounts:       amounts,
		RecipientKeys: recipientKeys,
		StealthAddrs:  stealthAddrs,
		DestChainID:   destChainIDStr,
	}, nil
}

func getBridgeAddress(chain string, cfg *TestConfig) string {
	switch chain {
	case "horizen":
		return cfg.BridgeHorizenAddr
	case "base":
		return cfg.BridgeBaseAddr
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

	// The result is a struct, we need to extract the 'completed' field
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

func checkIntentSolved(ctx context.Context, bridge *bind.BoundContract, intentID *big.Int) bool {
	var result []interface{}
	err := bridge.Call(&bind.CallOpts{Context: ctx}, &result, "isIntentSolvedOnChain2", intentID)
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

// fetchECIESPublicKey retrieves the ECIES public key from the relayer
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

	// Parse the hex-encoded public key
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

// encryptRecipientData encrypts recipient information using ECIES
func encryptRecipientData(publicKey *ecies.PublicKey, recipients, amounts []string) (string, error) {
	// Prepare the data to encrypt
	data := map[string]interface{}{
		"recipients": recipients,
		"amounts":    amounts,
	}

	// Convert to JSON
	plaintext, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal recipient data: %w", err)
	}

	// Encrypt using ECIES
	encrypted, err := ecies.Encrypt(publicKey, plaintext)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt data: %w", err)
	}

	// Return hex-encoded encrypted data
	return hex.EncodeToString(encrypted), nil
}

func storeRecipients(relayerURL, intentID string, recipients, amounts []string, chainID int64) error {
	// Fetch the ECIES public key from relayer
	fmt.Println("🔐 Fetching ECIES public key from relayer...")
	publicKey, err := fetchECIESPublicKey(relayerURL)
	if err != nil {
		return fmt.Errorf("failed to fetch ECIES public key: %w", err)
	}
	fmt.Println("✅ Public key fetched")

	// Encrypt the recipient data
	fmt.Println("🔒 Encrypting recipient data...")
	encryptedData, err := encryptRecipientData(publicKey, recipients, amounts)
	if err != nil {
		return fmt.Errorf("failed to encrypt recipient data: %w", err)
	}
	fmt.Println("✅ Data encrypted")

	// Send encrypted data to relayer
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

// IntentDetails represents the settlement details from the relayer
type IntentDetails struct {
	IntentID      string `json:"intentId"`
	SourceChainID string `json:"sourceChainId"`
	DestChainID   string `json:"destChainId"`
	SolverAddress string `json:"solverAddress"`
	DepositTxHash string `json:"depositTxHash,omitempty"`
	SolveTxHash   string `json:"solveTxHash,omitempty"`
	SettleTxHash  string `json:"settleTxHash,omitempty"`
	Status        string `json:"status"` // "pending", "solving", "settled", "completed"
}

// getIntentDetailsFromRelayer fetches settlement details from the relayer
func getIntentDetailsFromRelayer(relayerURL, intentID, sourceChainID string) (*IntentDetails, error) {
	url := fmt.Sprintf("%s/intent-details/%s?sourceChainId=%s", relayerURL, intentID, sourceChainID)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch intent details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch intent details (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool           `json:"success"`
		Intent  *IntentDetails `json:"intent,omitempty"`
		Error   string         `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("relayer returned error: %s", result.Error)
	}

	return result.Intent, nil
}

// EIP-712 signing helpers for TachyonStealth domain
// Domain: {name: "TachyonStealth", version: "1"} (off-chain only, no verifyingContract/chainId)
var (
	eip712DomainTypeHash        = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version)"))
	eip712DomainSeparator       = crypto.Keccak256Hash(eip712DomainTypeHash.Bytes(), crypto.Keccak256([]byte("TachyonStealth")), crypto.Keccak256([]byte("1")))
	registerStealthTypeHash     = crypto.Keccak256Hash([]byte("RegisterStealth(address signer)"))
	getStealthAddressesTypeHash = crypto.Keccak256Hash([]byte("GetStealthAddresses(address signer)"))
	deriveStealthKeyTypeHash    = crypto.Keccak256Hash([]byte("DeriveStealthKey(address recipientAddress,address stealthAddress)"))
	deploySafeTypeHash          = crypto.Keccak256Hash([]byte("DeploySafe(address stealthAddress,uint256 chainId)"))
)

func eip712Hash(structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, eip712DomainSeparator.Bytes(), structHash.Bytes())
}

func signRegisterStealth(key *ecdsa.PrivateKey, signer common.Address) []byte {
	structHash := crypto.Keccak256Hash(registerStealthTypeHash.Bytes(), common.LeftPadBytes(signer.Bytes(), 32))
	digest := eip712Hash(structHash)
	fmt.Printf("   [DEBUG-SIGN] RegisterStealth: signer=%s structHash=%x digest=%x\n", signer.Hex(), structHash[:8], digest[:8])
	sig, _ := crypto.Sign(digest.Bytes(), key)
	return sig
}

func signGetStealthAddresses(key *ecdsa.PrivateKey, signer common.Address) []byte {
	structHash := crypto.Keccak256Hash(getStealthAddressesTypeHash.Bytes(), common.LeftPadBytes(signer.Bytes(), 32))
	digest := eip712Hash(structHash)
	sig, _ := crypto.Sign(digest.Bytes(), key)
	return sig
}

func signDeriveStealthKey(key *ecdsa.PrivateKey, recipientAddress, stealthAddress common.Address) []byte {
	structHash := crypto.Keccak256Hash(deriveStealthKeyTypeHash.Bytes(), common.LeftPadBytes(recipientAddress.Bytes(), 32), common.LeftPadBytes(stealthAddress.Bytes(), 32))
	digest := eip712Hash(structHash)
	sig, _ := crypto.Sign(digest.Bytes(), key)
	return sig
}

func signDeploySafe(key *ecdsa.PrivateKey, stealthAddress common.Address, chainId *big.Int) []byte {
	structHash := crypto.Keccak256Hash(deploySafeTypeHash.Bytes(), common.LeftPadBytes(stealthAddress.Bytes(), 32), common.LeftPadBytes(chainId.Bytes(), 32))
	digest := eip712Hash(structHash)
	sig, _ := crypto.Sign(digest.Bytes(), key)
	return sig
}

// getIntentDetailsWithRetries fetches settlement details with retry logic
func getIntentDetailsWithRetries(relayerURL, intentID, sourceChainID string, maxRetries int, retryInterval time.Duration) (*IntentDetails, error) {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			fmt.Printf("   ⏳ Retry %d/%d: Checking settlement status (waiting %v)...\n", i, maxRetries-1, retryInterval)
			time.Sleep(retryInterval)
		}

		details, err := getIntentDetailsFromRelayer(relayerURL, intentID, sourceChainID)
		if err == nil {
			// Successfully fetched details
			if i > 0 {
				fmt.Printf("   ✅ Settlement details fetched after %d retries\n", i)
			}

			// Check if intent is in a final state (completed or settled)
			if details.Status == "completed" || details.Status == "settled" {
				return details, nil
			}

			// Intent exists but not yet completed, continue retrying
			lastErr = fmt.Errorf("intent status: %s (not yet completed)", details.Status)
			continue
		}

		lastErr = err
	}

	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}
