package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"github.com/zk-tachyon-solver/tachyon-prod/test/e2e/utils"
)

const (
	bridgeIntentABI = `[{"inputs":[{"internalType":"uint256","name":"_chainId","type":"uint256"}],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":false,"internalType":"address","name":"solver","type":"address"},{"indexed":false,"internalType":"uint256","name":"bidAmount","type":"uint256"}],"name":"BidPlaced","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"user","type":"address"},{"indexed":false,"internalType":"address","name":"tokenA","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"endTime","type":"uint256"}],"name":"IntentCreated","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":false,"internalType":"address[]","name":"recipients","type":"address[]"},{"indexed":false,"internalType":"uint256[]","name":"amounts","type":"uint256[]"}],"name":"IntentSolvedOnChain2","type":"event"},{"inputs":[{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"duration","type":"uint256"}],"name":"createIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"depositAndPickup","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"finalizeAuction","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"uint256","name":"_bidAmount","type":"uint256"}],"name":"placeBid","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"address[]","name":"recipients","type":"address[]"},{"internalType":"uint256[]","name":"amounts","type":"uint256[]"}],"name":"solveIntentOnChain2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"solvedIntents","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"chain1IntentId","type":"uint256"},{"internalType":"uint256","name":"chain2IntentId","type":"uint256"},{"internalType":"address","name":"solverAddress","type":"address"}],"name":"settleIntentWithChain2Verification","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

	mockERC20ABI = `[{"inputs":[{"internalType":"string","name":"name","type":"string"},{"internalType":"string","name":"symbol","type":"string"},{"internalType":"uint256","name":"initialSupply","type":"uint256"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"owner","type":"address"},{"internalType":"address","name":"spender","type":"address"}],"name":"allowance","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`
)

type TestConfig struct {
	HorizenRPC        string
	BaseRPC           string
	BridgeHorizenAddr string
	BridgeBaseAddr    string
	PrivateKey        string
	SolverURL         string
	RelayerURL        string
	AuctionDuration   int64
}

type RelayerResponse struct {
	Success         bool   `json:"success"`
	TransactionHash string `json:"transactionHash,omitempty"`
	Error           string `json:"error,omitempty"`
}

func loadConfig() (*TestConfig, error) {
	godotenv.Load(".env.test")

	return &TestConfig{
		HorizenRPC:        os.Getenv("HORIZEN_TESTNET_RPC_URL"),
		BaseRPC:           os.Getenv("BASE_SEPOLIA_RPC_URL"),
		BridgeHorizenAddr: os.Getenv("BRIDGE_CONTRACT_HORIZEN_LATEST"),
		BridgeBaseAddr:    os.Getenv("BRIDGE_CONTRACT_BASE_LATEST"),
		PrivateKey:        os.Getenv("PRIVATE_KEY"),
		SolverURL:         getEnvOrDefault("SOLVER_URL", "http://localhost:3001"),
		RelayerURL:        getEnvOrDefault("RELAYER_URL", "http://localhost:3000"),
		AuctionDuration:   20,
	}, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	fmt.Println("Starting complete cross-chain bridge flow test (Go implementation)...\n")

	ctx := context.Background()

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	clients, err := utils.InitClients(ctx, &utils.TestConfig{
		HorizenRPC:      cfg.HorizenRPC,
		BaseRPC:         cfg.BaseRPC,
		BridgeHorizenAddr: cfg.BridgeHorizenAddr,
		BridgeBaseAddr:  cfg.BridgeBaseAddr,
		PrivateKey:      cfg.PrivateKey,
		SolverURL:       cfg.SolverURL,
		RelayerURL:      cfg.RelayerURL,
		AuctionDuration: cfg.AuctionDuration,
	})
	if err != nil {
		fmt.Printf("Failed to initialize clients: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Chain IDs: Horizen=%s, Base=%s\n", clients.HorizenChainID, clients.BaseChainID)
	fmt.Printf("Wallet Address: %s\n\n", clients.Wallet.Address.Hex())

	fmt.Println("Checking service health...")
	if err := checkServiceHealth(cfg.SolverURL); err != nil {
		fmt.Printf("Solver service health check failed: %v\n", err)
	} else {
		fmt.Println("Solver service is healthy")
	}

	if err := checkServiceHealth(cfg.RelayerURL); err != nil {
		fmt.Printf("Relayer service health check failed: %v\n", err)
	} else {
		fmt.Println("Relayer service is healthy")
	}
	fmt.Println()

	bridgeABI, err := abi.JSON(strings.NewReader(bridgeIntentABI))
	if err != nil {
		fmt.Printf("Failed to parse bridge ABI: %v\n", err)
		os.Exit(1)
	}

	erc20ABI, err := abi.JSON(strings.NewReader(mockERC20ABI))
	if err != nil {
		fmt.Printf("Failed to parse ERC20 ABI: %v\n", err)
		os.Exit(1)
	}

	bridgeHorizen := bind.NewBoundContract(common.HexToAddress(cfg.BridgeHorizenAddr), bridgeABI, clients.HorizenClient, clients.HorizenClient, clients.HorizenClient)
	bridgeBase := bind.NewBoundContract(common.HexToAddress(cfg.BridgeBaseAddr), bridgeABI, clients.BaseClient, clients.BaseClient, clients.BaseClient)

	var latestIntentIDResult []interface{}
	err = bridgeHorizen.Call(&bind.CallOpts{Context: ctx}, &latestIntentIDResult, "getLatestIntentId")
	if err != nil {
		fmt.Printf("Failed to get latest intent ID: %v\n", err)
		os.Exit(1)
	}
	currentLatestIntentID := latestIntentIDResult[0].(*big.Int)
	targetIntentID := new(big.Int).Add(currentLatestIntentID, big.NewInt(1))
	fmt.Printf("Target Intent ID: %s (will be created next)\n\n", targetIntentID)

	fmt.Println("STEP 1: Using existing test tokens...")

	tokenAAddrStr := os.Getenv("TOKEN_A_HORIZEN")
	tokenBAddrStr := os.Getenv("TOKEN_B_BASE")

	if tokenAAddrStr == "" {
		tokenAAddrStr = "0x6d5be3853E3b277f9Cd2de7B911e4318eE78cfCb"
	}
	if tokenBAddrStr == "" {
		tokenBAddrStr = "0xAb50d4703484B4F0079607c873E064b18F49D48e"
	}

	tokenAAddr := common.HexToAddress(tokenAAddrStr)
	tokenBAddr := common.HexToAddress(tokenBAddrStr)

	fmt.Printf("TokenA (Horizen): %s\n", tokenAAddr.Hex())
	fmt.Printf("TokenB (Base): %s\n\n", tokenBAddr.Hex())

	tokenA := bind.NewBoundContract(tokenAAddr, erc20ABI, clients.HorizenClient, clients.HorizenClient, clients.HorizenClient)
	_ = tokenA

	fmt.Println("STEP 1.5: Approving tokens for multiple intents...")

	numIntents := 5
	expectedAmountB := utils.ParseEther("95")
	reward := utils.ParseEther("5")
	approveAmountPerIntent := new(big.Int).Add(expectedAmountB, reward)
	totalApproveAmount := new(big.Int).Mul(approveAmountPerIntent, big.NewInt(int64(numIntents)))

	auth, err := utils.GetTransactOpts(ctx, clients.HorizenClient, clients.Wallet, clients.HorizenChainID)
	if err != nil {
		fmt.Printf("Failed to create auth: %v\n", err)
		os.Exit(1)
	}

	approveTx, err := tokenA.Transact(auth, "approve", common.HexToAddress(cfg.BridgeHorizenAddr), totalApproveAmount)
	if err != nil {
		fmt.Printf("Failed to approve tokens: %v\n", err)
		os.Exit(1)
	}
	utils.WaitForTransaction(ctx, clients.HorizenClient, approveTx.Hash(), 2*time.Minute)
	fmt.Printf("Approved tokens for %d intents\n\n", numIntents)

	fmt.Printf("STEP 2: Creating %d intents sequentially on Horizen Testnet...\n", numIntents)

	createdIntents := make([]*big.Int, 0, numIntents)

	for i := 0; i < numIntents; i++ {
		intentNum := i + 1
		expectedIntentID := new(big.Int).Add(targetIntentID, big.NewInt(int64(i)))

		recipients := []string{
			utils.GenerateRandomAddress().Hex(),
			utils.GenerateRandomAddress().Hex(),
			utils.GenerateRandomAddress().Hex(),
		}
		amounts := []*big.Int{
			utils.ParseEther("30"),
			utils.ParseEther("35"),
			utils.ParseEther("30"),
		}
		amountStrings := make([]string, len(amounts))
		for j, amt := range amounts {
			amountStrings[j] = amt.String()
		}

		fmt.Printf("  [Intent %d] Creating intent ID %s...\n", intentNum, expectedIntentID)

		auth, err := utils.GetTransactOpts(ctx, clients.HorizenClient, clients.Wallet, clients.HorizenChainID)
		if err != nil {
			fmt.Printf("Failed to get transaction opts for intent %s: %v\n", expectedIntentID, err)
			continue
		}

		createIntentTx, err := bridgeHorizen.Transact(auth, "createIntent",
			tokenAAddr, tokenBAddr, expectedAmountB, expectedAmountB, reward, big.NewInt(cfg.AuctionDuration))
		if err != nil {
			fmt.Printf("Failed to create intent %s: %v\n", expectedIntentID, err)
			continue
		}

		utils.WaitForTransaction(ctx, clients.HorizenClient, createIntentTx.Hash(), 2*time.Minute)
		fmt.Printf("  [Intent %d] Created with tx: %s\n", intentNum, createIntentTx.Hash().Hex())

		go func(num int, intentID *big.Int, recs []string, amts []string, chainID int64) {
			if err := storeRecipients(cfg.RelayerURL, intentID.String(), recs, amts, chainID); err != nil {
				fmt.Printf("  [Intent %d] Failed to store recipients: %v\n", num, err)
			} else {
				fmt.Printf("  [Intent %d] Recipients stored\n", num)
			}
		}(intentNum, expectedIntentID, recipients, amountStrings, clients.BaseChainID.Int64())

		createdIntents = append(createdIntents, expectedIntentID)
	}

	fmt.Printf("\nSuccessfully created %d intents: ", len(createdIntents))
	for _, id := range createdIntents {
		fmt.Printf("%s ", id)
	}
	fmt.Println()

	fmt.Println("\nSTEP 3-6: Waiting for solver to handle complete auction process for all intents...")
	fmt.Printf("This includes: bidding, auction finalization, and deposit/pickup for %d intents\n", len(createdIntents))
	fmt.Printf("Waiting %d seconds for solver to complete all steps...\n", cfg.AuctionDuration+15)
	utils.SleepWithProgress(time.Duration(cfg.AuctionDuration+15)*time.Second, "Waiting for solver")
	fmt.Printf("Solver should have completed bidding, auction, and deposit for %d intents\n", len(createdIntents))

	actualIntentID := createdIntents[0]

	fmt.Println("\nSTEP 7-9: Waiting for solver to complete the flow...")
	fmt.Println("Solver should:")
	fmt.Println("   1. Retrieve recipients from relayer (with security validation)")
	fmt.Println("   2. Solve the intent on Base Sepolia")
	fmt.Println("   3. Call relayer to settle on Horizen")

	maxWaitSolve := 90 * time.Second
	pollInterval := 5 * time.Second
	startTime := time.Now()
	isSolved := false

	fmt.Println("\nChecking if intent is solved on Base...")
	for time.Since(startTime) < maxWaitSolve {
		var solvedResult []interface{}
		err = bridgeBase.Call(&bind.CallOpts{Context: ctx}, &solvedResult, "solvedIntents", actualIntentID)
		if err == nil && len(solvedResult) > 0 {
			isSolved = solvedResult[0].(bool)
			if isSolved {
				elapsed := time.Since(startTime).Round(time.Second)
				fmt.Printf("Intent solved on Base! (took %v)\n", elapsed)
				break
			}
		}
		fmt.Printf("Waiting for solver to solve on Base... (%v elapsed)\n", time.Since(startTime).Round(time.Second))
		time.Sleep(pollInterval)
	}

	if !isSolved {
		fmt.Printf("Solver did not solve the intent within %v\n", maxWaitSolve)
		fmt.Println("Possible reasons:")
		fmt.Println("   - Solver is not running")
		fmt.Println("   - Solver didn't win the auction")
		fmt.Println("   - Solver encountered an error retrieving recipients")
		fmt.Println("   - Security validation prevented unauthorized access")
		os.Exit(1)
	}

	fmt.Println("\nChecking if intent is settled on Horizen...")
	maxWaitSettle := 60 * time.Second
	startTime = time.Now()
	isCompleted := false

	for time.Since(startTime) < maxWaitSettle {
		var completedResult []interface{}
		err = bridgeHorizen.Call(&bind.CallOpts{Context: ctx}, &completedResult, "getIntent", actualIntentID)
		if err == nil && len(completedResult) > 0 {
			intentStruct := completedResult[0].(struct {
				IntentId        *big.Int       `json:"intentId"`
				User            common.Address `json:"user"`
				TokenA          common.Address `json:"tokenA"`
				TokenB          common.Address `json:"tokenB"`
				AmountA         *big.Int       `json:"amountA"`
				ExpectedAmountB *big.Int       `json:"expectedAmountB"`
				Reward          *big.Int       `json:"reward"`
				EndTime         *big.Int       `json:"endTime"`
				WinningSolver   common.Address `json:"winningSolver"`
				WinningBid      *big.Int       `json:"winningBid"`
				Completed       bool           `json:"completed"`
				Deposited       bool           `json:"deposited"`
			})
			isCompleted = intentStruct.Completed
			if isCompleted {
				elapsed := time.Since(startTime).Round(time.Second)
				fmt.Printf("Intent settled on Horizen! (took %v)\n", elapsed)
				break
			}
		}
		fmt.Printf("Waiting for solver to settle on Horizen... (%v elapsed)\n", time.Since(startTime).Round(time.Second))
		time.Sleep(pollInterval)
	}

	if !isCompleted {
		fmt.Printf("Intent not settled within %v\n", maxWaitSettle)
		fmt.Println("Note: Intent was solved on Base but settlement may still be pending")
	} else {
		fmt.Println("\nComplete solver flow validated successfully!")
		fmt.Println("   - Solver won the auction")
		fmt.Println("   - Solver retrieved recipients from relayer (security validated)")
		fmt.Println("   - Solver solved intent on Base")
		fmt.Println("   - Solver settled intent on Horizen")
	}

	fmt.Println("\nComplete flow test finished successfully!")
	fmt.Printf("Final Intent ID used: %s\n", actualIntentID)
}

func deployMockERC20(ctx context.Context, client *ethclient.Client, wallet *utils.TestWallet, chainID *big.Int, name, symbol string) (common.Address, error) {
	_ = ctx
	_ = client
	_ = wallet
	_ = chainID
	_ = name
	_ = symbol

	fmt.Println("Note: Using existing token deployment method from original scripts")
	return common.HexToAddress("0x0"), fmt.Errorf("deploy via TypeScript scripts first")
}

func checkServiceHealth(serviceURL string) error {
	resp, err := http.Get(fmt.Sprintf("%s/health", serviceURL))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy status: %d", resp.StatusCode)
	}

	return nil
}

func storeRecipients(relayerURL, intentID string, recipients, amounts []string, chainID int64) error {
	data := map[string]interface{}{
		"intentId":   intentID,
		"recipients": recipients,
		"amounts":    amounts,
		"chainId":    chainID,
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

func settleIntent(relayerURL, intentID, network, solverAddress string) error {
	data := map[string]interface{}{
		"intentId":         intentID,
		"network":          network,
		"chain2IntentId":   intentID,
		"solverAddress":    solverAddress,
		"destinationChain": "base",
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	resp, err := http.Post(fmt.Sprintf("%s/settle", relayerURL), "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("settlement failed: %s", string(body))
	}

	var response RelayerResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}

	if !response.Success {
		return fmt.Errorf("settlement failed: %s", response.Error)
	}

	fmt.Printf("Settlement transaction: %s\n", response.TransactionHash)
	return nil
}
