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
	"os"
	"strconv"
	"strings"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"github.com/zk-tachyon-solver/tachyon-prod/test/e2e/utils"
)

const erc20ABI = `[{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"owner","type":"address"},{"internalType":"address","name":"spender","type":"address"}],"name":"allowance","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"recipient","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

const bridgeABI = `[{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getIntent","outputs":[{"components":[{"internalType":"uint256","name":"intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"bool","name":"deposited","type":"bool"}],"internalType":"struct BridgeIntent.Intent","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint256","name":"_sourceChainId","type":"uint256"},{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"isIntentSolvedOnChain2","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"}]`

func main() {
	fmt.Println("=== TWAP E2E Test ===")
	fmt.Println("Testing: Buy AMZN on Robinhood with USDC on Base via private TWAP (1000:1)")
	fmt.Println("  - 3 tranches, 5s apart, 100 USDC each → 0.1 AMZN each")
	fmt.Println("  - Fresh random wallet funded from deployer")
	fmt.Println("  - TWAP server on localhost:3002")
	fmt.Println()

	// Try multiple paths for .env depending on where binary is run from
	if err := godotenv.Load("../../.env"); err != nil {
		if err2 := godotenv.Load("../../../.env"); err2 != nil {
			fmt.Printf("Warning: could not load .env: %v\n", err2)
		}
	}

	ctx := context.Background()

	twapURL := getEnv("TWAP_URL", "http://localhost:3002")
	relayerURL := getEnv("RELAYER_URL", "http://localhost:3000")
	deployerKeyHex := os.Getenv("PRIVATE_KEY")

	baseRPC := os.Getenv("CHAIN_1_RPC")
	robinhoodRPC := os.Getenv("CHAIN_2_RPC")
	bridgeBaseAddr := os.Getenv("CHAIN_1_BRIDGE")
	bridgeRobinhoodAddr := os.Getenv("CHAIN_2_BRIDGE")

	tokenUSDC_Base := os.Getenv("TOKEN_USDC_BASE")
	tokenAMZN_Robinhood := os.Getenv("TOKEN_AMZN_ROBINHOOD")

	// Use chain IDs from env (RPC ChainID() can be unreliable on testnets)
	baseChainIDInt, _ := strconv.ParseInt(os.Getenv("CHAIN_1_ID"), 10, 64)
	robinhoodChainIDInt, _ := strconv.ParseInt(os.Getenv("CHAIN_2_ID"), 10, 64)
	baseChainID := big.NewInt(baseChainIDInt)
	robinhoodChainID := big.NewInt(robinhoodChainIDInt)

	// Connect to chains
	fmt.Println("Connecting to networks...")
	baseClient, err := ethclient.Dial(baseRPC)
	fatal(err, "connect to Base")
	robinhoodClient, err := ethclient.Dial(robinhoodRPC)
	fatal(err, "connect to Robinhood")

	fmt.Printf("  Base: chain %s, block %d\n", baseChainID, mustBlock(ctx, baseClient))
	fmt.Printf("  Robinhood: chain %s, block %d\n\n", robinhoodChainID, mustBlock(ctx, robinhoodClient))

	// Create deployer wallet (has funds)
	deployerWallet, err := utils.CreateTestWallet(deployerKeyHex)
	fatal(err, "create deployer wallet")
	fmt.Printf("Deployer: %s\n", deployerWallet.Address.Hex())

	// ============================================================
	// Step 0: Generate fresh random wallet and fund it
	// ============================================================
	fmt.Println("\nStep 0: Generating fresh random wallet...")
	freshKey, err := crypto.GenerateKey()
	fatal(err, "generate random key")
	freshAddr := crypto.PubkeyToAddress(freshKey.PublicKey)
	fmt.Printf("  Fresh wallet: %s\n", freshAddr.Hex())

	// Fund fresh wallet with ETH from deployer (for gas)
	fmt.Println("  Funding fresh wallet with ETH for gas...")
	ethAmount := utils.ParseEther("0.01") // 0.01 ETH for gas
	err = sendETH(ctx, baseClient, deployerWallet, freshAddr, ethAmount, baseChainID)
	fatal(err, "fund fresh wallet with ETH")
	fmt.Printf("  Sent 0.01 ETH to fresh wallet\n")

	// Fund fresh wallet with USDC from deployer
	totalUSDC := utils.ParseToken("300", 6) // 300 USDC total for 3 tranches
	fmt.Printf("  Transferring %s USDC to fresh wallet...\n", totalUSDC)

	parsedERC20, _ := abi.JSON(strings.NewReader(erc20ABI))
	usdcBase := bind.NewBoundContract(common.HexToAddress(tokenUSDC_Base), parsedERC20, baseClient, baseClient, baseClient)

	transferTx, err := retryContractTx(ctx, baseClient, deployerWallet, baseChainID, usdcBase, "transfer", freshAddr, totalUSDC)
	fatal(err, "transfer USDC to fresh wallet")
	err = utils.WaitForTransaction(ctx, baseClient, transferTx.Hash(), 2*time.Minute)
	fatal(err, "wait for USDC transfer")
	fmt.Printf("  Funded! USDC transfer tx: %s\n\n", transferTx.Hash().Hex())

	// Build a TestWallet-like struct for the fresh key
	freshWallet := &utils.TestWallet{
		PrivateKey: freshKey,
		PublicKey:  &freshKey.PublicKey,
		Address:    freshAddr,
	}

	// ============================================================
	// Step 1: Get TEE address from TWAP server
	// ============================================================
	fmt.Println("Step 1: Getting TEE address from TWAP server...")
	teeAddress, err := getTEEAddress(twapURL)
	fatal(err, "get TEE address")
	fmt.Printf("  TEE Address: %s\n", teeAddress)
	fmt.Println("  (User must approve this address for transferFrom)\n")

	// ============================================================
	// Step 2: Approve TEE address to spend USDC on Base (from fresh wallet)
	// ============================================================
	amountPerTranche := utils.ParseToken("100", 6) // 100 USDC (6 decimals)
	totalAmount := new(big.Int).Mul(amountPerTranche, big.NewInt(3))

	fmt.Printf("Step 2: Approving TEE to spend %s USDC on Base (from fresh wallet)...\n", totalAmount)

	freshAuth, err := utils.GetTransactOpts(ctx, baseClient, freshWallet, baseChainID)
	fatal(err, "get fresh wallet tx opts")

	approveTx, err := usdcBase.Transact(freshAuth, "approve", common.HexToAddress(teeAddress), totalAmount)
	fatal(err, "approve tx")

	err = utils.WaitForTransaction(ctx, baseClient, approveTx.Hash(), 2*time.Minute)
	fatal(err, "wait for approve")
	fmt.Printf("  Approved! tx: %s\n\n", approveTx.Hash().Hex())

	// ============================================================
	// Step 3: Get ECIES pubkey and encrypt TWAP order
	// ============================================================
	fmt.Println("Step 3: Encrypting TWAP order...")
	pubKey, err := fetchECIESPubKey(twapURL)
	fatal(err, "fetch ECIES pubkey")

	// Generate random recipients for the dest chain (Robinhood)
	recipients := []string{
		utils.GenerateRandomAddress().Hex(),
		utils.GenerateRandomAddress().Hex(),
		utils.GenerateRandomAddress().Hex(),
	}
	// 0.1 AMZN per tranche (18 decimals), split across 3 recipients
	// 1000:1 ratio: 100 USDC -> 0.1 AMZN
	recipientAmounts := []string{
		utils.ParseToken("0.03", 18).String(),
		utils.ParseToken("0.04", 18).String(),
		utils.ParseToken("0.03", 18).String(),
	}

	now := time.Now().Unix()
	orderParams := map[string]interface{}{
		"user":            freshWallet.Address.Hex(),
		"assetIn":         tokenUSDC_Base,
		"assetOut":        tokenAMZN_Robinhood,
		"chainIn":         "base",
		"chainOut":        "robinhood",
		"sourceChainId":   baseChainID.Int64(),
		"destChainId":     robinhoodChainID.Int64(),
		"totalAmountIn":   totalAmount.String(),
		"amountPerPeriod": amountPerTranche.String(),
		"period":          5,
		"startTime":       now + 5,
		"endTime":         now + 600,
		"permitSignature": "",
		"recipients":      recipients,
		"amounts":         recipientAmounts,
	}

	plaintext, _ := json.Marshal(orderParams)
	fmt.Printf("  DEBUG raw JSON: %s\n", string(plaintext))
	encrypted, err := ecies.Encrypt(pubKey, plaintext)
	fatal(err, "encrypt order")
	encryptedHex := hex.EncodeToString(encrypted)

	fmt.Printf("  Order encrypted (%d bytes)\n", len(encrypted))
	fmt.Printf("  Params: 3 tranches x 100 USDC, 5s apart (1000:1 -> 0.1 AMZN each)\n")
	fmt.Printf("  Route: Base (USDC) -> Robinhood (AMZN) @ 1000:1\n\n")

	// ============================================================
	// Step 3b: Sign message to prove ownership of the wallet
	// ============================================================
	fmt.Println("Step 3b: Signing authentication message...")
	signMessage := fmt.Sprintf("tachyon-twap:%s:%d", freshWallet.Address.Hex(), now)
	signHash := signEthMessage(signMessage)
	sig, err := crypto.Sign(signHash, freshWallet.PrivateKey)
	fatal(err, "sign message")
	// Convert v from 0/1 to 27/28 (Ethereum convention)
	sig[64] += 27
	sigHex := hex.EncodeToString(sig)
	fmt.Printf("  Signed message: %s\n", signMessage)
	fmt.Printf("  Signature: 0x%s...%s\n\n", sigHex[:8], sigHex[len(sigHex)-8:])

	// ============================================================
	// Step 4: Submit encrypted TWAP order
	// ============================================================
	fmt.Println("Step 4: Submitting TWAP order to TEE server...")

	reqBody, _ := json.Marshal(map[string]string{
		"encryptedData": encryptedHex,
		"signature":     sigHex,
		"message":       signMessage,
	})

	resp, err := http.Post(twapURL+"/twap/create", "application/json", bytes.NewReader(reqBody))
	fatal(err, "POST /twap/create")
	defer resp.Body.Close()

	var createResp struct {
		Success bool   `json:"success"`
		OrderID string `json:"orderId"`
		Status  string `json:"status"`
		Error   string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&createResp)

	if !createResp.Success {
		fmt.Printf("  FAILED: %s\n", createResp.Error)
		os.Exit(1)
	}

	orderID := createResp.OrderID
	fmt.Printf("  Order created: %s\n", orderID)
	fmt.Printf("  Status: %s\n\n", createResp.Status)

	// ============================================================
	// Step 5: Record initial intent IDs on source chain
	// ============================================================
	parsedBridge, _ := abi.JSON(strings.NewReader(bridgeABI))
	bridgeBase := bind.NewBoundContract(common.HexToAddress(bridgeBaseAddr), parsedBridge, baseClient, baseClient, baseClient)
	bridgeRobinhood := bind.NewBoundContract(common.HexToAddress(bridgeRobinhoodAddr), parsedBridge, robinhoodClient, robinhoodClient, robinhoodClient)

	var initialIntentIDResult []interface{}
	bridgeBase.Call(&bind.CallOpts{Context: ctx}, &initialIntentIDResult, "getLatestIntentId")
	initialIntentID := initialIntentIDResult[0].(*big.Int)
	fmt.Printf("Step 5: Initial latest intent ID on Base: %s\n\n", initialIntentID)

	// ============================================================
	// Step 6: Wait and poll for tranches
	// ============================================================
	fmt.Println("Step 6: Monitoring TWAP execution...")
	fmt.Println("  Waiting for TEE to create intents per period...")
	fmt.Println()

	maxWait := 5 * time.Minute
	pollInterval := 30 * time.Second
	deadline := time.Now().Add(maxWait)
	lastTranchesSeen := 0

	for time.Now().Before(deadline) {
		statusResp, err := http.Get(fmt.Sprintf("%s/twap/status/%s", twapURL, orderID))
		if err != nil {
			fmt.Printf("  [%s] Error checking status: %v\n", time.Now().Format("15:04:05"), err)
			time.Sleep(pollInterval)
			continue
		}

		var status struct {
			Success        bool   `json:"success"`
			Status         string `json:"status"`
			TranchesFilled int    `json:"tranchesFilled"`
			TotalTranches  int    `json:"totalTranches"`
			AmountSpent    string `json:"amountSpent"`
		}
		json.NewDecoder(statusResp.Body).Decode(&status)
		statusResp.Body.Close()

		if status.TranchesFilled > lastTranchesSeen {
			fmt.Printf("  [%s] Tranche %d/%d executed! Spent: %s\n",
				time.Now().Format("15:04:05"), status.TranchesFilled, status.TotalTranches, status.AmountSpent)
			lastTranchesSeen = status.TranchesFilled
		}

		if status.Status == "completed" || status.TranchesFilled >= status.TotalTranches {
			fmt.Printf("\n  All %d tranches completed!\n\n", status.TotalTranches)
			break
		}

		if status.Status == "cancelled" {
			fmt.Println("\n  Order was cancelled!")
			break
		}

		time.Sleep(pollInterval)
	}

	// ============================================================
	// Step 7: Wait for solver to process all intents
	// ============================================================
	fmt.Println("Step 7: Waiting 60s for solver to process all created intents...")
	time.Sleep(60 * time.Second)

	// ============================================================
	// Step 8: Verify intents on-chain
	// ============================================================
	fmt.Println("\nStep 8: Verification Phase")
	fmt.Println(strings.Repeat("=", 60))

	var finalIntentIDResult []interface{}
	bridgeBase.Call(&bind.CallOpts{Context: ctx}, &finalIntentIDResult, "getLatestIntentId")
	finalIntentID := finalIntentIDResult[0].(*big.Int)

	newIntents := new(big.Int).Sub(finalIntentID, initialIntentID).Int64()
	fmt.Printf("\n  New intents created on Base: %d (IDs %s to %s)\n", newIntents, new(big.Int).Add(initialIntentID, big.NewInt(1)), finalIntentID)

	successCount := 0
	for i := int64(1); i <= newIntents; i++ {
		intentID := new(big.Int).Add(initialIntentID, big.NewInt(i))

		sourceCompleted := checkCompleted(ctx, bridgeBase, intentID)
		destSolved := checkSolved(ctx, bridgeRobinhood, baseChainID, intentID)

		status := "PENDING"
		if sourceCompleted && destSolved {
			status = "FULLY COMPLETED"
			successCount++
		} else if sourceCompleted {
			status = "Source completed, dest pending"
		} else if destSolved {
			status = "Dest solved, source pending"
		}

		fmt.Printf("  Intent %s: %s\n", intentID, status)
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("TWAP Results: %d/%d intents fully completed\n", successCount, newIntents)
	fmt.Printf("Order: %s\n", orderID)

	_ = relayerURL
	_ = bridgeRobinhoodAddr
	fmt.Println()
	finalStatus, _ := http.Get(fmt.Sprintf("%s/twap/status/%s", twapURL, orderID))
	if finalStatus != nil {
		var s struct {
			TranchesFilled int    `json:"tranchesFilled"`
			TotalTranches  int    `json:"totalTranches"`
			AmountSpent    string `json:"amountSpent"`
			Status         string `json:"status"`
		}
		json.NewDecoder(finalStatus.Body).Decode(&s)
		finalStatus.Body.Close()
		fmt.Printf("TWAP Final Status: %s (%d/%d tranches, spent %s)\n", s.Status, s.TranchesFilled, s.TotalTranches, s.AmountSpent)
	}
}

// --- Helpers ---

func retryContractTx(ctx context.Context, client *ethclient.Client, wallet *utils.TestWallet, chainID *big.Int, contract *bind.BoundContract, method string, args ...interface{}) (*types.Transaction, error) {
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		auth, err := utils.GetTransactOpts(ctx, client, wallet, chainID)
		if err != nil {
			return nil, err
		}
		tx, err := contract.Transact(auth, method, args...)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "nonce") || strings.Contains(errStr, "replacement transaction underpriced") {
				fmt.Printf("  Nonce conflict on %s (attempt %d), retrying...\n", method, attempt+1)
				continue
			}
			return nil, err
		}
		return tx, nil
	}
	return nil, fmt.Errorf("%s failed after 10 retries", method)
}

func sendETH(ctx context.Context, client *ethclient.Client, from *utils.TestWallet, to common.Address, amount *big.Int, chainID *big.Int) error {
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		nonce, err := client.PendingNonceAt(ctx, from.Address)
		if err != nil {
			return err
		}
		gasPrice, err := client.SuggestGasPrice(ctx)
		if err != nil {
			return err
		}

		tx := types.NewTransaction(nonce, to, amount, uint64(21000), gasPrice, nil)
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), from.PrivateKey)
		if err != nil {
			return err
		}
		err = client.SendTransaction(ctx, signedTx)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "nonce") || strings.Contains(errStr, "replacement transaction underpriced") {
				fmt.Printf("  Nonce conflict (attempt %d), retrying...\n", attempt+1)
				continue
			}
			return err
		}
		return utils.WaitForTransaction(ctx, client, signedTx.Hash(), 2*time.Minute)
	}
	return fmt.Errorf("sendETH failed after 10 retries")
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(err error, msg string) {
	if err != nil {
		fmt.Printf("FATAL [%s]: %v\n", msg, err)
		os.Exit(1)
	}
}

func mustBlock(ctx context.Context, c *ethclient.Client) uint64 {
	b, _ := c.BlockNumber(ctx)
	return b
}

func getTEEAddress(twapURL string) (string, error) {
	resp, err := http.Get(twapURL + "/twap/address")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool   `json:"success"`
		Address string `json:"address"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.Success {
		return "", fmt.Errorf("failed to get TEE address")
	}
	return result.Address, nil
}

func fetchECIESPubKey(twapURL string) (*ecies.PublicKey, error) {
	resp, err := http.Get(twapURL + "/twap/ecies-pubkey")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed: %s", string(body))
	}

	var result struct {
		Success   bool   `json:"success"`
		PublicKey string `json:"publicKey"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	pubKeyBytes, err := hex.DecodeString(result.PublicKey)
	if err != nil {
		return nil, err
	}
	return ecies.NewPublicKeyFromBytes(pubKeyBytes)
}

func checkCompleted(ctx context.Context, bridge *bind.BoundContract, intentID *big.Int) bool {
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

func checkSolved(ctx context.Context, bridge *bind.BoundContract, sourceChainID, intentID *big.Int) bool {
	var result []interface{}
	err := bridge.Call(&bind.CallOpts{Context: ctx}, &result, "isIntentSolvedOnChain2", sourceChainID, intentID)
	if err != nil || len(result) == 0 {
		return false
	}
	return result[0].(bool)
}

// signEthMessage produces the Ethereum personal_sign hash: keccak256("\x19Ethereum Signed Message:\n" + len(msg) + msg)
func signEthMessage(message string) []byte {
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	return crypto.Keccak256([]byte(prefixed))
}

// Ensure unused imports don't cause issues
var _ = (*ecdsa.PrivateKey)(nil)
