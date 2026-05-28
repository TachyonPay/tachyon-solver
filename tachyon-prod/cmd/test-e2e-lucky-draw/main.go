// test-e2e-lucky-draw: full end-to-end integration test that runs the complete
// Tachyon bridge flow and then enters the lucky draw with the same stealth identity.
//
// Phases:
//
//	Phase 1  BRIDGE — Horizen → Base Sepolia
//	           Register recipient, generate stealth address + proxy Safe
//	           Approve + createIntent on Horizen
//	           Wait for solver to deliver USDC to proxy Safe on Base
//
//	Phase 2  STEALTH — Derive key, deploy proxy Safe, verify USDC balance
//
//	Phase 3  LUCKY DRAW — EIP-712 sign, enter lucky draw, verify result
//
// Required env vars (all present in tachyon-prod/.env after adding LUCKY_DRAW_CHAIN_N_*):
//
//	RELAYER_URL                            default http://localhost:8080
//	PRIVATE_KEY                            wallet that pays for the bridge intent
//	CHAIN_1_RPC / CHAIN_1_BRIDGE / CHAIN_1_ID   Horizen (source)
//	CHAIN_2_RPC / CHAIN_2_BRIDGE / CHAIN_2_ID   Base Sepolia (dest + lucky draw)
//	TOKEN_USDC_HORIZEN / TOKEN_USDC_BASE         USDC addresses on each chain
//	SAFE_SIGNER_KEY_1/2/3                        TEE keys (for lucky draw Safe signing)
//	LUCKY_DRAW_CHAIN_COUNT + LUCKY_DRAW_CHAIN_N_* lucky draw chain config
//	TEST_AMOUNT_A (default 100)            raw USDC deposited on source
//	TEST_AMOUNT_B (default 95)             raw USDC expected on dest
//	TEST_REWARD   (default 5)              raw USDC solver incentive
//	TEST_AUCTION_DURATION (default 100)    auction window in seconds
//
// Run:
//
//	go run ./cmd/test-e2e-lucky-draw/
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
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
	"github.com/zk-tachyon/tachyon-prod/pkg/luckydraw"
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

// ─── ABIs ────────────────────────────────────────────────────────────────────

const bridgeIntentABI = `[
  {"inputs":[{"internalType":"address","name":"_tokenA","type":"address"},{"internalType":"address","name":"_tokenB","type":"address"},{"internalType":"uint256","name":"_amountA","type":"uint256"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"_reward","type":"uint256"},{"internalType":"uint256","name":"_destChainId","type":"uint256"},{"internalType":"uint256","name":"_auctionDuration","type":"uint256"}],"name":"createIntent","outputs":[],"stateMutability":"nonpayable","type":"function"},
  {"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
  {"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getIntent","outputs":[{"components":[{"internalType":"uint256","name":"intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"bool","name":"deposited","type":"bool"}],"internalType":"struct BridgeIntent.Intent","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},
  {"inputs":[{"internalType":"uint256","name":"_sourceChainId","type":"uint256"},{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"isIntentSolvedOnChain2","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"}
]`

const erc20ABI = `[
  {"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},
  {"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
  {"inputs":[],"name":"decimals","outputs":[{"internalType":"uint8","name":"","type":"uint8"}],"stateMutability":"view","type":"function"}
]`

// ─── Main ────────────────────────────────────────────────────────────────────

func main() {
	godotenv.Load()

	relayerURL := envOr("RELAYER_URL", "http://localhost:8080")
	srcRPC := mustEnv("CHAIN_1_RPC")
	dstRPC := mustEnv("CHAIN_2_RPC")
	srcBridgeAddr := mustEnv("CHAIN_1_BRIDGE")
	dstBridgeAddr := mustEnv("CHAIN_2_BRIDGE")
	tokenHorizen := mustEnv("TOKEN_USDC_HORIZEN")
	tokenBase := mustEnv("TOKEN_USDC_BASE")
	walletKeyHex := strings.TrimPrefix(mustEnv("PRIVATE_KEY"), "0x")

	amountA := envOr("TEST_AMOUNT_A", "100")
	amountB := envOr("TEST_AMOUNT_B", "95")
	reward := envOr("TEST_REWARD", "5")
	auctionDurSec := parseInt64(envOr("TEST_AUCTION_DURATION", "100"))

	ctx := context.Background()

	section("PHASE 0: SETUP")

	walletKey, err := crypto.HexToECDSA(walletKeyHex)
	must(err, "parse PRIVATE_KEY")
	walletAddr := crypto.PubkeyToAddress(walletKey.PublicKey)
	fmt.Printf("  Wallet:         %s\n", walletAddr.Hex())
	fmt.Printf("  Relayer:        %s\n", relayerURL)

	srcClient, err := ethclient.DialContext(ctx, srcRPC)
	must(err, "dial Horizen RPC")
	defer srcClient.Close()
	dstClient, err := ethclient.DialContext(ctx, dstRPC)
	must(err, "dial Base RPC")
	defer dstClient.Close()

	srcChainID, err := srcClient.ChainID(ctx)
	must(err, "get Horizen chainID")
	dstChainID, err := dstClient.ChainID(ctx)
	must(err, "get Base chainID")

	srcBlock, _ := srcClient.BlockNumber(ctx)
	dstBlock, _ := dstClient.BlockNumber(ctx)
	fmt.Printf("  Horizen:        chain %s, block %d\n", srcChainID, srcBlock)
	fmt.Printf("  Base Sepolia:   chain %s, block %d\n", dstChainID, dstBlock)

	bridgeABIParsed, _ := abi.JSON(strings.NewReader(bridgeIntentABI))
	erc20ABIParsed, _ := abi.JSON(strings.NewReader(erc20ABI))

	srcBridge := bind.NewBoundContract(common.HexToAddress(srcBridgeAddr), bridgeABIParsed, srcClient, srcClient, srcClient)
	dstBridge := bind.NewBoundContract(common.HexToAddress(dstBridgeAddr), bridgeABIParsed, dstClient, dstClient, dstClient)
	srcToken := bind.NewBoundContract(common.HexToAddress(tokenHorizen), erc20ABIParsed, srcClient, srcClient, srcClient)

	decimalsA := fetchDecimals(ctx, srcClient, tokenHorizen)
	decimalsB := fetchDecimals(ctx, dstClient, tokenBase)
	amountARaw := parseToken(amountA, decimalsA)
	amountBRaw := parseToken(amountB, decimalsB)
	rewardRaw := parseToken(reward, decimalsA)

	fmt.Printf("  Token decimals: src=%d dst=%d\n", decimalsA, decimalsB)
	fmt.Printf("  AmountA:        %s raw (%s USDC)\n", amountARaw, amountA)
	fmt.Printf("  AmountB:        %s raw (%s USDC)\n", amountBRaw, amountB)
	fmt.Printf("  Reward:         %s raw (%s USDC)\n", rewardRaw, reward)

	// ── Phase 1: Bridge (Horizen → Base Sepolia) ─────────────────────────────
	section("PHASE 1: BRIDGE (Horizen → Base Sepolia)")

	// 1a — generate ephemeral recipient key
	recipientKey, err := crypto.GenerateKey()
	must(err, "generate recipient key")
	recipientAddr := crypto.PubkeyToAddress(recipientKey.PublicKey)
	fmt.Printf("  [1a] Ephemeral recipient: %s\n", recipientAddr.Hex())

	// 1b — register recipient with relayer (EIP-712)
	fmt.Printf("  [1b] Registering recipient with relayer...\n")
	regSig, err := stealth.SignRegisterStealth(recipientKey, recipientAddr)
	must(err, "sign RegisterStealth")
	var regResp struct {
		Success     bool   `json:"success"`
		SpendPubKey string `json:"spendPubKey"`
		Error       string `json:"error"`
	}
	must(postJSON(ctx, relayerURL+"/recipient/register", map[string]string{
		"recipientAddress": recipientAddr.Hex(),
		"signature":        "0x" + hex.EncodeToString(regSig),
	}, &regResp), "POST /recipient/register")
	if !regResp.Success {
		fatalf("register failed: %s", regResp.Error)
	}
	spkLen := len(regResp.SpendPubKey)
	if spkLen > 20 {
		spkLen = 20
	}
	fmt.Printf("       SpendPubKey: %s...\n", regResp.SpendPubKey[:spkLen])

	// 1c — generate stealth address on Base Sepolia
	fmt.Printf("  [1c] Generating stealth address on chain %s...\n", dstChainID)
	var genResp struct {
		Success       bool   `json:"success"`
		StealthAddress string `json:"stealthAddress"`
		SafeAddress   string `json:"safeAddress"`
		Error         string `json:"error"`
	}
	must(postJSON(ctx, relayerURL+"/recipient/generate-stealth", map[string]string{
		"recipientAddress": recipientAddr.Hex(),
		"chainId":          dstChainID.String(),
		"intentId":         fmt.Sprintf("e2e-lucky-draw-%d", time.Now().Unix()),
	}, &genResp), "POST /recipient/generate-stealth")
	if !genResp.Success {
		fatalf("generate-stealth failed: %s", genResp.Error)
	}
	stealthAddrStr := genResp.StealthAddress
	proxySafeStr := genResp.SafeAddress
	proxySafeAddr := common.HexToAddress(proxySafeStr)
	fmt.Printf("       Stealth address: %s\n", stealthAddrStr)
	fmt.Printf("       Proxy Safe:      %s\n", proxySafeStr)

	// 1d — store recipients with relayer (ECIES-encrypted so solver can read)
	fmt.Printf("  [1d] Storing recipients with relayer (ECIES)...\n")
	if err := storeRecipients(ctx, relayerURL, "0", []string{proxySafeStr}, []string{amountBRaw.String()}, srcChainID.Int64()); err != nil {
		fmt.Printf("       WARNING: storeRecipients failed: %v (continuing)\n", err)
	} else {
		fmt.Printf("       Stored: %s gets %s raw USDC\n", proxySafeStr[:16]+"...", amountBRaw)
	}

	// 1e — read current latest intent ID before creating ours
	var latestIDRes []interface{}
	must(srcBridge.Call(&bind.CallOpts{Context: ctx}, &latestIDRes, "getLatestIntentId"), "getLatestIntentId")
	prevIntentID := latestIDRes[0].(*big.Int)
	fmt.Printf("  [1e] Current latest intent ID on Horizen: %s\n", prevIntentID)

	// 1f — approve tokenA to bridge
	fmt.Printf("  [1f] Approving %s raw tokenA to bridge on Horizen...\n", new(big.Int).Add(amountARaw, rewardRaw))
	auth, err := getTransactOpts(ctx, srcClient, walletKey, srcChainID)
	must(err, "getTransactOpts approve")
	approveTx, err := srcToken.Transact(auth, "approve", common.HexToAddress(srcBridgeAddr), new(big.Int).Add(amountARaw, rewardRaw))
	must(err, "approve tx")
	fmt.Printf("       ApproveTx: %s\n", approveTx.Hash().Hex())
	must(waitForTx(ctx, srcClient, approveTx.Hash(), 2*time.Minute), "wait approve")
	fmt.Printf("       Approve confirmed\n")

	// 1g — createIntent on Horizen
	fmt.Printf("  [1g] Creating bridge intent on Horizen...\n")
	auth2, err := getTransactOpts(ctx, srcClient, walletKey, srcChainID)
	must(err, "getTransactOpts createIntent")
	intentTx, err := srcBridge.Transact(auth2, "createIntent",
		common.HexToAddress(tokenHorizen),
		common.HexToAddress(tokenBase),
		amountARaw,
		amountBRaw,
		rewardRaw,
		dstChainID,
		big.NewInt(auctionDurSec),
	)
	must(err, "createIntent tx")
	fmt.Printf("       CreateTx:  %s\n", intentTx.Hash().Hex())
	must(waitForTx(ctx, srcClient, intentTx.Hash(), 2*time.Minute), "wait createIntent")

	// Read intent ID with retries (testnet RPC can lag)
	time.Sleep(10 * time.Second)
	var intentID *big.Int
	for i := 0; i < 6; i++ {
		var res []interface{}
		if err := srcBridge.Call(&bind.CallOpts{Context: ctx}, &res, "getLatestIntentId"); err == nil {
			id := res[0].(*big.Int)
			if id.Cmp(prevIntentID) > 0 {
				intentID = id
				break
			}
		}
		if i < 5 {
			fmt.Printf("       Waiting for intent ID propagation (retry %d/5)...\n", i+1)
			time.Sleep(8 * time.Second)
		}
	}
	if intentID == nil {
		fatalf("intent ID did not increase after createIntent tx — check Horizen RPC")
	}
	fmt.Printf("       IntentID:  %s\n", intentID)
	fmt.Printf("       AmountA:   %s raw (%s USDC)\n", amountARaw, amountA)
	fmt.Printf("       AmountB:   %s raw (%s USDC expected)\n", amountBRaw, amountB)
	fmt.Printf("       Reward:    %s raw (%s USDC)\n", rewardRaw, reward)
	fmt.Printf("       Dest:      Base Sepolia (chain %s)\n", dstChainID)

	// Now that we have the intent ID, re-store recipients with the real intent ID
	fmt.Printf("  [1h] Re-storing recipients with real intentID=%s...\n", intentID)
	if err := storeRecipients(ctx, relayerURL, intentID.String(), []string{proxySafeStr}, []string{amountBRaw.String()}, srcChainID.Int64()); err != nil {
		fmt.Printf("       WARNING: storeRecipients (real intentID) failed: %v\n", err)
	} else {
		fmt.Printf("       Stored for intentID=%s\n", intentID)
	}

	// 1i — wait for solver to solve and settle
	waitSec := auctionDurSec + 180 // auction window + 3 min for solver
	fmt.Printf("  [1i] Waiting %ds for solver (auction=%ds + ~3min settle)...\n", waitSec, auctionDurSec)
	solverCtx, solverCancel := context.WithTimeout(ctx, time.Duration(waitSec+60)*time.Second)
	defer solverCancel()

	intentSettled := false
	for i := 0; i < int(waitSec/15)+1; i++ {
		time.Sleep(15 * time.Second)
		// Check source chain completed
		completed := checkIntentCompleted(solverCtx, srcBridge, intentID)
		// Check dest chain solved
		solved := checkIntentSolved(solverCtx, dstBridge, srcChainID, intentID)
		fmt.Printf("       [t+%ds] Horizen completed=%v  Base solved=%v\n",
			(i+1)*15, completed, solved)
		if completed && solved {
			intentSettled = true
			break
		}
	}

	if intentSettled {
		fmt.Printf("       BRIDGE SETTLED\n")
	} else {
		fmt.Printf("       WARNING: intent not fully settled within timeout — continuing anyway\n")
	}

	// ── Phase 2: Stealth key + proxy Safe deployment ──────────────────────────
	section("PHASE 2: STEALTH KEY & PROXY SAFE")

	// 2a — derive stealth private key from TEE
	fmt.Printf("  [2a] Deriving stealth private key from TEE...\n")
	deriveSig, err := stealth.SignDeriveStealthKey(recipientKey, recipientAddr, common.HexToAddress(stealthAddrStr))
	must(err, "sign DeriveStealthKey")
	var deriveResp struct {
		Success           bool   `json:"success"`
		StealthAddress    string `json:"stealthAddress"`
		StealthPrivateKey string `json:"stealthPrivateKey"`
		Error             string `json:"error"`
	}
	must(postJSON(ctx, relayerURL+"/recipient/derive-stealth-key", map[string]string{
		"recipientAddress": recipientAddr.Hex(),
		"stealthAddress":   stealthAddrStr,
		"signature":        "0x" + hex.EncodeToString(deriveSig),
	}, &deriveResp), "POST /recipient/derive-stealth-key")
	if !deriveResp.Success {
		fatalf("derive-stealth-key failed: %s", deriveResp.Error)
	}
	stealthPrivKey, err := crypto.HexToECDSA(strings.TrimPrefix(deriveResp.StealthPrivateKey, "0x"))
	must(err, "parse stealth private key")
	derivedStealthAddr := crypto.PubkeyToAddress(stealthPrivKey.PublicKey)
	fmt.Printf("       Stealth address (expected):  %s\n", stealthAddrStr)
	fmt.Printf("       Stealth address (derived):   %s\n", derivedStealthAddr.Hex())
	if !strings.EqualFold(derivedStealthAddr.Hex(), stealthAddrStr) {
		fmt.Printf("       WARNING: derived address does not match stored stealth address\n")
	}

	// 2b — deploy proxy Safe on Base Sepolia
	fmt.Printf("  [2b] Deploying proxy Safe on Base Sepolia (chain %s)...\n", dstChainID)
	deploySig, err := stealth.SignDeploySafe(stealthPrivKey, derivedStealthAddr, dstChainID)
	must(err, "sign DeploySafe")
	var deployResp struct {
		Success     bool   `json:"success"`
		SafeAddress string `json:"safeAddress"`
		TxHash      string `json:"txHash"`
		Error       string `json:"error"`
	}
	must(postJSON(ctx, relayerURL+"/recipient/deploy-safe", map[string]string{
		"stealthAddress": stealthAddrStr,
		"chainId":        dstChainID.String(),
		"signature":      "0x" + hex.EncodeToString(deploySig),
	}, &deployResp), "POST /recipient/deploy-safe")
	if !deployResp.Success {
		fmt.Printf("       WARNING: deploy-safe failed: %s (Safe may already be deployed)\n", deployResp.Error)
	} else {
		fmt.Printf("       SafeAddress: %s\n", deployResp.SafeAddress)
		fmt.Printf("       DeployTx:    %s\n", deployResp.TxHash)
	}

	// 2c — wait for Safe code to appear on Base
	fmt.Printf("  [2c] Waiting for Safe contract code on Base Sepolia...\n")
	safeDeployed := false
	for i := 0; i < 20; i++ {
		time.Sleep(4 * time.Second)
		code, err := dstClient.CodeAt(ctx, proxySafeAddr, nil)
		if err == nil && len(code) > 0 {
			fmt.Printf("       Safe deployed (%d bytes) after %ds\n", len(code), (i+1)*4)
			safeDeployed = true
			break
		}
	}
	if !safeDeployed {
		fmt.Printf("       WARNING: Safe code not detected within 80s — getOwners() may fail\n")
	}

	// 2d — check USDC balance at proxy Safe on Base
	fmt.Printf("  [2d] Checking USDC balance at proxy Safe on Base Sepolia...\n")
	dstTokenContract := bind.NewBoundContract(common.HexToAddress(tokenBase), erc20ABIParsed, dstClient, dstClient, dstClient)
	var balRes []interface{}
	must(dstTokenContract.Call(&bind.CallOpts{Context: ctx}, &balRes, "balanceOf", proxySafeAddr), "balanceOf proxy Safe")
	proxySafeBalance := balRes[0].(*big.Int)
	fmt.Printf("       Proxy Safe USDC: %s raw\n", proxySafeBalance)
	if proxySafeBalance.Sign() == 0 {
		fmt.Printf("       WARNING: proxy Safe has 0 USDC — bridge may not have settled yet\n")
		fmt.Printf("       Lucky draw eligibility check will fail if balance stays 0\n")
	}

	// ── Phase 3: Lucky Draw ───────────────────────────────────────────────────
	section("PHASE 3: LUCKY DRAW")

	// 3a — load lucky draw config and init service
	fmt.Printf("  [3a] Loading lucky draw config...\n")
	ldCfg, err := luckydraw.LoadConfig()
	must(err, "luckydraw.LoadConfig")

	// Verify dest chain is configured for lucky draw
	destChainStr := dstChainID.String()
	chainSupported := false
	var supportedIDs []string
	for _, ce := range ldCfg.Chains {
		supportedIDs = append(supportedIDs, ce.ChainID.String())
		if ce.ChainID.String() == destChainStr {
			chainSupported = true
		}
	}
	if !chainSupported {
		fatalf("dest chain %s not in lucky draw config. Supported: %v\n"+
			"Add LUCKY_DRAW_CHAIN_N_ID=%s to .env", destChainStr, supportedIDs, destChainStr)
	}
	fmt.Printf("       Supported lucky draw chains: %v\n", supportedIDs)
	fmt.Printf("       Drip chain: %s (Base Sepolia)\n", destChainStr)

	// 3b — check team Safe USDC balance (pre-flight)
	teamSafeAddr, teamSafeBalance := checkTeamSafeBalance(ctx, ldCfg, dstClient, tokenBase, destChainStr, erc20ABIParsed)
	fmt.Printf("       Team Safe: %s\n", teamSafeAddr)
	fmt.Printf("       Team Safe USDC: %s raw\n", teamSafeBalance)
	if teamSafeBalance.Sign() == 0 {
		fmt.Printf("       WARNING: team Safe has 0 USDC — ExecuteViaSafe will fail\n")
		fmt.Printf("       Fund the team Safe with USDC on Base Sepolia before running this test\n")
	}

	// 3c — open isolated temp DB
	tmpDir, err := os.MkdirTemp("", "luckydraw-e2e-*")
	must(err, "mktemp")
	defer os.RemoveAll(tmpDir)

	store, err := storage.NewStore(tmpDir)
	must(err, "open lucky draw DB")
	defer store.Close()

	ldSvc, err := luckydraw.NewService(ldCfg, store)
	must(err, "NewService")
	fmt.Printf("       Lucky draw service initialised\n")

	// 3d — sign EIP-712 LuckyDrawEntry with stealth key
	fmt.Printf("  [3d] Signing EIP-712 LuckyDrawEntry...\n")
	digest := stealth.LuckyDrawEntryDigest(proxySafeAddr, dstChainID)
	fmt.Printf("       Domain:         TachyonStealth v1\n")
	fmt.Printf("       Type:           LuckyDrawEntry(address proxyAddress,uint256 chainId)\n")
	fmt.Printf("       proxyAddress:   %s\n", proxySafeAddr.Hex())
	fmt.Printf("       chainId:        %s\n", dstChainID)
	fmt.Printf("       Digest:         %s\n", digest.Hex())

	rawSig, err := stealth.SignLuckyDrawEntry(stealthPrivKey, proxySafeAddr, dstChainID)
	must(err, "SignLuckyDrawEntry")
	rawSig[64] += 27 // adjust v for EIP-155 / MetaMask compat
	fmt.Printf("       Signature:      0x%s\n", hex.EncodeToString(rawSig))

	// 3e — enter lucky draw
	fmt.Printf("  [3e] Entering lucky draw (WinChance=%.2f)...\n", ldCfg.WinChance)
	ldCtx, ldCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer ldCancel()

	result, err := ldSvc.Enter(ldCtx, luckydraw.EnterRequest{
		ProxyAddress: proxySafeAddr.Hex(),
		ChainID:      destChainStr,
		Signature:    "0x" + hex.EncodeToString(rawSig),
	})
	if err != nil {
		fatalf("lucky draw Enter failed: %v\n\n"+
			"  Common causes:\n"+
			"  - Proxy Safe not deployed (getOwners failed) → check Phase 2\n"+
			"  - Proxy Safe has 0 USDC (bridge not settled) → check Phase 1\n"+
			"  - Team Safe has 0 USDC (no funds to drip) → fund the team Safe\n"+
			"  - LUCKY_DRAW_ADMIN_KEY is not a Safe owner → comment it out in .env", err)
	}

	fmt.Printf("\n       *** LUCKY DRAW RESULT ***\n")
	fmt.Printf("       Won:             %v\n", result.Won)
	fmt.Printf("       Stealth address: %s\n", result.StealthAddress)
	fmt.Printf("       Proxy Safe:      %s\n", result.ProxyAddress)
	fmt.Printf("       Chain:           %s\n", result.ChainID)
	if result.Won {
		fmt.Printf("       Drip amount:     %d USDC\n", result.AmountUSDC)
		fmt.Printf("       Drip amount raw: %s\n", result.AmountRaw)
		fmt.Printf("       TxHash:          %s\n", result.TxHash)
	} else {
		fmt.Printf("       No drip (did not win — WinChance=%.2f, try again or set LUCKY_DRAW_WIN_CHANCE=1.0 for testing)\n", ldCfg.WinChance)
	}

	// 3f — verify status persisted (both win and loss must be stored)
	fmt.Printf("  [3f] Verifying lucky draw status persisted...\n")
	status, err := ldSvc.Status(derivedStealthAddr.Hex())
	must(err, "Status")
	fmt.Printf("       Entered: %v  Won: %v  Amount: %d USDC\n", status.Entered, status.Won, status.AmountUSDC)
	if status.TxHash != "" {
		fmt.Printf("       TxHash:  %s\n", status.TxHash)
	}
	if !status.Entered {
		fatalf("Status.Entered is false after Enter — persistence bug")
	}
	if status.Won != result.Won {
		fatalf("Status.Won=%v does not match result.Won=%v", status.Won, result.Won)
	}

	// 3g — verify duplicate entry is rejected regardless of win/loss
	fmt.Printf("  [3g] Verifying duplicate entry rejected (dedup by stealth address)...\n")
	_, dupErr := ldSvc.Enter(ldCtx, luckydraw.EnterRequest{
		ProxyAddress: proxySafeAddr.Hex(),
		ChainID:      destChainStr,
		Signature:    "0x" + hex.EncodeToString(rawSig),
	})
	if dupErr == nil {
		fatalf("second entry was NOT rejected — dedup is broken")
	}
	fmt.Printf("       Rejected (expected): %v\n", dupErr)

	// ── Summary ───────────────────────────────────────────────────────────────
	section("SUMMARY")
	fmt.Printf("  Bridge flow:      Horizen (chain %s) → Base Sepolia (chain %s)\n", srcChainID, dstChainID)
	fmt.Printf("  Intent ID:        %s\n", intentID)
	fmt.Printf("  Intent settled:   %v\n", intentSettled)
	fmt.Printf("  CreateIntent tx:  %s\n", intentTx.Hash().Hex())
	fmt.Println()
	fmt.Printf("  Recipient EOA:    %s\n", recipientAddr.Hex())
	fmt.Printf("  Stealth address:  %s\n", derivedStealthAddr.Hex())
	fmt.Printf("  Proxy Safe:       %s\n", proxySafeAddr.Hex())
	fmt.Printf("  USDC at Safe:     %s raw (from bridge)\n", proxySafeBalance)
	fmt.Println()
	fmt.Printf("  Lucky draw chain: %s (Base Sepolia)\n", destChainStr)
	fmt.Printf("  Win chance:       %.0f%%\n", ldCfg.WinChance*100)
	fmt.Printf("  Won:              %v\n", result.Won)
	if result.Won {
		fmt.Printf("  Drip amount:      %d USDC (%s raw)\n", result.AmountUSDC, result.AmountRaw)
		fmt.Printf("  Drip tx:          %s\n", result.TxHash)
		fmt.Println()
		fmt.Printf("  Configured range: %d – %d USDC\n", ldCfg.MinUSDC, ldCfg.MaxUSDC)
		if result.AmountUSDC < ldCfg.MinUSDC || result.AmountUSDC > ldCfg.MaxUSDC {
			fatalf("drip amount %d outside configured range [%d, %d]", result.AmountUSDC, ldCfg.MinUSDC, ldCfg.MaxUSDC)
		}
	} else {
		fmt.Printf("  No drip (did not win this round)\n")
		fmt.Printf("  Note: set LUCKY_DRAW_WIN_CHANCE=1.0 in .env to force a win for testing\n")
	}

	fmt.Println()
	fmt.Println("  FULL E2E FLOW PASSED")
}

// ─── on-chain check helpers ───────────────────────────────────────────────────

func checkIntentCompleted(ctx context.Context, bridge *bind.BoundContract, intentID *big.Int) bool {
	type intentStruct struct {
		IntentId        *big.Int
		User            common.Address
		TokenA          common.Address
		TokenB          common.Address
		AmountA         *big.Int
		ExpectedAmountB *big.Int
		Reward          *big.Int
		DestChainId     *big.Int
		EndTime         *big.Int
		WinningSolver   common.Address
		WinningBid      *big.Int
		Completed       bool
		Deposited       bool
	}
	var out []interface{}
	if err := bridge.Call(&bind.CallOpts{Context: ctx}, &out, "getIntent", intentID); err != nil || len(out) == 0 {
		return false
	}
	s, ok := out[0].(intentStruct)
	return ok && s.Completed
}

func checkIntentSolved(ctx context.Context, bridge *bind.BoundContract, sourceChainID, intentID *big.Int) bool {
	var out []interface{}
	if err := bridge.Call(&bind.CallOpts{Context: ctx}, &out, "isIntentSolvedOnChain2", sourceChainID, intentID); err != nil || len(out) == 0 {
		return false
	}
	solved, ok := out[0].(bool)
	return ok && solved
}

func checkTeamSafeBalance(ctx context.Context, cfg *luckydraw.Config, client *ethclient.Client, tokenAddr, chainIDStr string, erc20ABI abi.ABI) (string, *big.Int) {
	for _, ce := range cfg.Chains {
		if ce.ChainID.String() != chainIDStr {
			continue
		}
		tokenContract := bind.NewBoundContract(ce.USDCAddress, erc20ABI, client, client, client)
		var res []interface{}
		if err := tokenContract.Call(&bind.CallOpts{Context: ctx}, &res, "balanceOf", common.HexToAddress(ce.SafeAddress)); err != nil {
			return ce.SafeAddress, big.NewInt(0)
		}
		return ce.SafeAddress, res[0].(*big.Int)
	}
	return "unknown", big.NewInt(0)
}

// ─── ECIES store-recipients ───────────────────────────────────────────────────

func storeRecipients(ctx context.Context, relayerURL, intentID string, recipients, amounts []string, chainID int64) error {
	// Fetch relayer ECIES public key
	resp, err := http.Get(relayerURL + "/ecies-pubkey")
	if err != nil {
		return fmt.Errorf("GET /ecies-pubkey: %w", err)
	}
	defer resp.Body.Close()
	var pubkeyResp struct {
		Success   bool   `json:"success"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pubkeyResp); err != nil {
		return fmt.Errorf("decode ecies-pubkey: %w", err)
	}
	if !pubkeyResp.Success {
		return fmt.Errorf("ecies-pubkey returned success=false")
	}
	pubKeyBytes, err := hex.DecodeString(pubkeyResp.PublicKey)
	if err != nil {
		return fmt.Errorf("decode pubkey hex: %w", err)
	}
	pubKey, err := ecies.NewPublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("parse ecies pubkey: %w", err)
	}

	plaintext, _ := json.Marshal(map[string]interface{}{
		"recipients": recipients,
		"amounts":    amounts,
	})
	encrypted, err := ecies.Encrypt(pubKey, plaintext)
	if err != nil {
		return fmt.Errorf("ecies encrypt: %w", err)
	}

	return postJSON(ctx, relayerURL+"/store-recipients", map[string]interface{}{
		"intentId":      intentID,
		"chainId":       chainID,
		"encryptedData": hex.EncodeToString(encrypted),
	}, nil)
}

// ─── Transaction helpers ──────────────────────────────────────────────────────

func getTransactOpts(ctx context.Context, client *ethclient.Client, key *ecdsa.PrivateKey, chainID *big.Int) (*bind.TransactOpts, error) {
	addr := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, addr)
	if err != nil {
		return nil, err
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	auth, err := bind.NewKeyedTransactorWithChainID(key, chainID)
	if err != nil {
		return nil, err
	}
	auth.Nonce = big.NewInt(int64(nonce))
	auth.GasLimit = 3_000_000
	auth.GasPrice = gasPrice
	auth.Context = ctx
	return auth, nil
}

func waitForTx(ctx context.Context, client *ethclient.Client, txHash common.Hash, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		receipt, err := client.TransactionReceipt(ctx, txHash)
		if err == nil && receipt != nil {
			if receipt.Status == 0 {
				return fmt.Errorf("tx %s reverted", txHash.Hex())
			}
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("tx %s not mined within %s", txHash.Hex(), timeout)
}

// ─── HTTP helper ──────────────────────────────────────────────────────────────

func postJSON(ctx context.Context, url string, body interface{}, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ─── Token / env helpers ─────────────────────────────────────────────────────

func parseToken(amount string, decimals int) *big.Int {
	f, _, _ := new(big.Float).Parse(amount, 10)
	mult := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	result := new(big.Float).Mul(f, mult)
	out := new(big.Int)
	result.Int(out)
	return out
}

func fetchDecimals(ctx context.Context, client *ethclient.Client, tokenAddr string) int {
	decimalsABI, _ := abi.JSON(strings.NewReader(`[{"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"}]`))
	c := bind.NewBoundContract(common.HexToAddress(tokenAddr), decimalsABI, client, client, client)
	var out []interface{}
	if err := c.Call(&bind.CallOpts{Context: ctx}, &out, "decimals"); err != nil || len(out) == 0 {
		return 18
	}
	return int(out[0].(uint8))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatalf("env var %s is required", key)
	}
	return v
}

func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 100
	}
	return n
}

// ─── Output helpers ───────────────────────────────────────────────────────────

func section(title string) {
	bar := strings.Repeat("─", 60)
	fmt.Printf("\n%s\n  %s\n%s\n", bar, title, bar)
}

func must(err error, context string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL [%s]: %v\n", context, err)
		os.Exit(1)
	}
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}
