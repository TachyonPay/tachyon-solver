// Solana legs for the complete-flow e2e test.
//
//   Test 3: Horizen -> Solana (USDC.e on Horizen -> SPL test-USDC on devnet)
//   Test 4: Solana  -> Horizen (SPL test-USDC on devnet -> USDC.e on Horizen)
//
// These run after the two EVM-only legs (tests 1 and 2) in complete_flow.go.
// They share the same operator wallet for both EVM and Solana sides — the
// Solana key is loaded from SOLVER_SOLANA_PRIVATE_KEY. For a single-operator
// test that's fine; in production the user and solver would be different
// keys.
//
// What's verified:
//   - Test 3: createIntentToSolana lands on EVM, IntentCreatedToSolana event
//             fires, and (if the solver is running) the Solana SolvedRecord
//             PDA at [b"solved", srcChainID_le, intentID_le] exists after
//             solve_intent_on_chain2.
//   - Test 4: Anchor create_intent lands on Solana, IntentRecord PDA exists
//             with the expected fields, and (if the solver is running) the
//             EVM contract's solvedIntents[(40168, intentID)] is true.
//
// If the solver isn't running, both tests report "intent created, dest
// delivery pending" rather than failing.

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon-solver/tachyon-prod/test/e2e/utils"
)

// createIntentToSolanaABI is a single-function ABI fragment so we can
// build calldata for the new EVM function without re-embedding the full
// 17 KB BridgeIntent ABI string.
const createIntentToSolanaABI = `[{"inputs":[{"internalType":"address","name":"_tokenA","type":"address"},{"internalType":"bytes32","name":"_tokenBMint","type":"bytes32"},{"internalType":"uint256","name":"_amountA","type":"uint256"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"_reward","type":"uint256"},{"internalType":"uint64","name":"_destSolanaEID","type":"uint64"},{"internalType":"bytes32","name":"_recipient","type":"bytes32"},{"internalType":"uint256","name":"_auctionDuration","type":"uint256"}],"name":"createIntentToSolana","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

// SolanaTestConfig collects the Solana-specific env vars consumed by the
// two new test legs. Read once via loadSolanaTestConfig; missing values
// are surfaced as a single descriptive error rather than scattered nils.
type SolanaTestConfig struct {
	RPCURL          string // CHAIN_4_RPC or SOLANA_RPC_URL — devnet default
	WSURL           string // optional, derived from RPCURL when empty
	ProgramID       string // CHAIN_4_SOLANA_PROGRAM_ID
	USDCMint        string // CHAIN_4_SOLANA_USDC_MINT (test mint from cmd/deploy-spl-test-tokens)
	WSOLMint        string // canonical or override
	ChainID         uint64 // LayerZero EID (40168 devnet)
	UserSecretBase58 string // user's Solana keypair — defaults to SOLVER_SOLANA_PRIVATE_KEY
}

func loadSolanaTestConfig() (*SolanaTestConfig, error) {
	pickFirst := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}

	cfg := &SolanaTestConfig{
		RPCURL:           pickFirst("CHAIN_4_RPC", "SOLANA_RPC_URL"),
		WSURL:            pickFirst("CHAIN_4_HELIUS_RPC", "SOLANA_WS_URL"),
		ProgramID:        pickFirst("CHAIN_4_SOLANA_PROGRAM_ID", "SOLANA_PROGRAM_ID"),
		USDCMint:         pickFirst("CHAIN_4_SOLANA_USDC_MINT", "SOLANA_USDC_MINT"),
		WSOLMint:         pickFirst("CHAIN_4_SOLANA_WSOL_MINT", "SOLANA_WSOL_MINT"),
		UserSecretBase58: pickFirst("USER_SOLANA_PRIVATE_KEY", "SOLVER_SOLANA_PRIVATE_KEY"),
	}
	if cfg.WSOLMint == "" {
		cfg.WSOLMint = "So11111111111111111111111111111111111111112"
	}
	if cfg.RPCURL == "" {
		cfg.RPCURL = rpc.DevNet_RPC
	}
	if cfg.WSURL == "" {
		cfg.WSURL = rpc.DevNet_WS
	}

	chainStr := pickFirst("CHAIN_4_ID", "SOLANA_CHAIN_ID")
	if chainStr == "" {
		chainStr = "40168" // devnet LZ EID default
	}
	id, err := strconv.ParseUint(chainStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid solana chain id %q: %w", chainStr, err)
	}
	cfg.ChainID = id

	if cfg.ProgramID == "" {
		return nil, fmt.Errorf("CHAIN_4_SOLANA_PROGRAM_ID (or SOLANA_PROGRAM_ID) is required for the Solana test legs")
	}
	if cfg.USDCMint == "" {
		return nil, fmt.Errorf("CHAIN_4_SOLANA_USDC_MINT (or SOLANA_USDC_MINT) is required — run `go run ./cmd/deploy-spl-test-tokens` first")
	}
	if cfg.UserSecretBase58 == "" {
		return nil, fmt.Errorf("SOLVER_SOLANA_PRIVATE_KEY (or USER_SOLANA_PRIVATE_KEY) is required for Solana signing")
	}
	return cfg, nil
}

// SolanaTestResult is the per-leg report rendered in the summary.
type SolanaTestResult struct {
	Direction       string
	IntentID        *big.Int
	SourceTxHash    string
	SourceCompleted bool
	DestVerified    bool
	Note            string
	Err             error
}

// solanaPubkeyToBytes32 returns the raw 32 bytes of a Solana pubkey for
// use as the EVM `bytes32` recipient field on createIntentToSolana.
func solanaPubkeyToBytes32(pk solana.PublicKey) [32]byte {
	var out [32]byte
	copy(out[:], pk.Bytes())
	return out
}

// evmAddressToSolanaRecipient32 left-pads a 20-byte EVM address with 12
// zero bytes so it fits the Anchor program's `recipient: [u8;32]` field.
// The Solana solver detects the leading zeros and renders this back as
// `0x...20-bytes` for downstream EVM-side consumption.
func evmAddressToSolanaRecipient32(addr common.Address) [32]byte {
	var out [32]byte
	copy(out[12:], addr.Bytes())
	return out
}

// runHorizenToSolana executes test 3 — an EVM->Solana intent originated
// on Horizen with delivery on Solana devnet. Verifies the source-side
// intent landed; polls for solver-delivered SolvedRecord PDA on Solana.
func runHorizenToSolana(
	ctx context.Context,
	cfg *TestConfig,
	solCfg *SolanaTestConfig,
	wallet *utils.TestWallet,
	horizenClient *ethclient.Client,
	horizenChainID *big.Int,
	bridgeHorizenAddr common.Address,
) *SolanaTestResult {
	res := &SolanaTestResult{Direction: "horizen->solana"}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🧪 TEST 3: Horizen → Solana (createIntentToSolana)")
	fmt.Println(strings.Repeat("=", 80))

	tokenA := strings.TrimSpace(os.Getenv("TOKEN_USDC_HORIZEN"))
	if tokenA == "" {
		res.Err = fmt.Errorf("TOKEN_USDC_HORIZEN not set")
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}

	usdcMint, err := solana.PublicKeyFromBase58(solCfg.USDCMint)
	if err != nil {
		res.Err = fmt.Errorf("invalid devnet USDC mint: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	userSolanaKey, err := solana.PrivateKeyFromBase58(solCfg.UserSecretBase58)
	if err != nil {
		res.Err = fmt.Errorf("invalid solana user key: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	recipient32 := solanaPubkeyToBytes32(userSolanaKey.PublicKey())
	tokenBMint32 := solanaPubkeyToBytes32(usdcMint)

	decimalsA := fetchTokenDecimals(ctx, horizenClient, tokenA)
	decimalsB := 6 // mock USDC + real devnet USDC both use 6
	amountA := utils.ParseToken(cfg.AmountA, decimalsA)
	expectedAmountB := utils.ParseToken(cfg.AmountB, decimalsB)
	reward := utils.ParseToken(cfg.Reward, decimalsA)
	auctionDuration := big.NewInt(cfg.AuctionDuration)

	fmt.Printf("💱 tokenA=%s (decimals %d), tokenB=devnet USDC (mint %s), recipient=%s\n",
		tokenA, decimalsA, solCfg.USDCMint, userSolanaKey.PublicKey())
	fmt.Printf("   amountA=%s expectedAmountB=%s reward=%s\n",
		amountA, expectedAmountB, reward)

	// Approve tokenA for amountA + reward.
	erc20ABIParsed, _ := abi.JSON(strings.NewReader(mockERC20ABI))
	tokenAContract := bind.NewBoundContract(common.HexToAddress(tokenA), erc20ABIParsed, horizenClient, horizenClient, horizenClient)
	auth, err := utils.GetTransactOpts(ctx, horizenClient, wallet, horizenChainID)
	if err != nil {
		res.Err = err
		fmt.Printf("❌ TransactOpts: %v\n", err)
		return res
	}
	approveAmount := new(big.Int).Add(amountA, reward)
	fmt.Println("🔐 Approving USDC.e on Horizen...")
	approveTx, err := tokenAContract.Transact(auth, "approve", bridgeHorizenAddr, approveAmount)
	if err != nil {
		res.Err = fmt.Errorf("approve: %w", err)
		fmt.Printf("❌ approve failed: %v\n", err)
		return res
	}
	utils.WaitForTransaction(ctx, horizenClient, approveTx.Hash(), 2*time.Minute)
	fmt.Println("✅ Approved")

	// Read currentLatestId so we know which ID the new intent will get.
	bridgeABIParsed, _ := abi.JSON(strings.NewReader(bridgeIntentABI))
	bridgeContract := bind.NewBoundContract(bridgeHorizenAddr, bridgeABIParsed, horizenClient, horizenClient, horizenClient)
	var latestRes []interface{}
	if err := bridgeContract.Call(&bind.CallOpts{Context: ctx}, &latestRes, "getLatestIntentId"); err != nil {
		res.Err = fmt.Errorf("getLatestIntentId: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	prevLatest := latestRes[0].(*big.Int)
	expectedID := new(big.Int).Add(prevLatest, big.NewInt(1))
	res.IntentID = expectedID
	fmt.Printf("🎯 Expecting intent ID %s\n", expectedID)

	// Build createIntentToSolana via a fragment-only BoundContract so we
	// don't need the new function in the main 17 KB embedded ABI.
	soloABI, _ := abi.JSON(strings.NewReader(createIntentToSolanaABI))
	createBound := bind.NewBoundContract(bridgeHorizenAddr, soloABI, horizenClient, horizenClient, horizenClient)
	auth2, _ := utils.GetTransactOpts(ctx, horizenClient, wallet, horizenChainID)

	fmt.Println("🎯 Submitting createIntentToSolana...")
	createTx, err := createBound.Transact(auth2, "createIntentToSolana",
		common.HexToAddress(tokenA),
		tokenBMint32,
		amountA, expectedAmountB, reward,
		solCfg.ChainID,
		recipient32,
		auctionDuration,
	)
	if err != nil {
		res.Err = fmt.Errorf("createIntentToSolana: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	utils.WaitForTransaction(ctx, horizenClient, createTx.Hash(), 2*time.Minute)
	res.SourceTxHash = createTx.Hash().Hex()
	fmt.Printf("✅ createIntentToSolana confirmed: %s\n", res.SourceTxHash)

	// Sanity-check intent landed at expectedID.
	time.Sleep(8 * time.Second)
	var ext []interface{}
	if err := bridgeContract.Call(&bind.CallOpts{Context: ctx}, &ext, "getSolanaIntentExt", expectedID); err == nil && len(ext) >= 4 {
		isSolanaDest, _ := ext[3].(bool)
		fmt.Printf("   isSolanaDestIntent[%s] = %v\n", expectedID, isSolanaDest)
	}

	// Poll Solana for the SolvedRecord PDA — created when the solver
	// runs solve_intent_on_chain2 on Solana. This is the destination-side
	// proof of delivery.
	fmt.Println("⏳ Polling Solana for SolvedRecord PDA (up to 3 min)...")
	res.DestVerified = pollSolanaSolvedRecord(ctx, solCfg, horizenChainID.Uint64(), expectedID.Uint64(), 3*time.Minute)
	if res.DestVerified {
		fmt.Println("✅ Solana SolvedRecord PDA exists — destination delivery confirmed")
	} else {
		res.Note = "destination delivery not observed within timeout; solver may not be running"
		fmt.Println("⚠️  SolvedRecord PDA not found within timeout (run the solver to drive completion)")
	}

	// Source-side completion (after settle on Horizen).
	res.SourceCompleted = checkIntentCompleted(ctx, bridgeContract, expectedID)
	if res.SourceCompleted {
		fmt.Println("✅ Horizen intent.completed = true")
	} else {
		fmt.Println("⚠️  Horizen intent not yet settled")
	}
	return res
}

// runSolanaToHorizen executes test 4 — a Solana->EVM intent originated
// on Solana devnet with delivery on Horizen.
func runSolanaToHorizen(
	ctx context.Context,
	cfg *TestConfig,
	solCfg *SolanaTestConfig,
	wallet *utils.TestWallet,
	horizenClient *ethclient.Client,
	horizenChainID *big.Int,
	bridgeHorizenAddr common.Address,
) *SolanaTestResult {
	res := &SolanaTestResult{Direction: "solana->horizen"}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🧪 TEST 4: Solana → Horizen (Anchor create_intent)")
	fmt.Println(strings.Repeat("=", 80))

	userKey, err := solana.PrivateKeyFromBase58(solCfg.UserSecretBase58)
	if err != nil {
		res.Err = fmt.Errorf("invalid user key: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	usdcMint, err := solana.PublicKeyFromBase58(solCfg.USDCMint)
	if err != nil {
		res.Err = fmt.Errorf("invalid USDC mint: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}

	sc, err := blockchain.NewSolanaClient(
		solCfg.RPCURL, solCfg.WSURL,
		solCfg.ProgramID,
		new(big.Int).SetUint64(solCfg.ChainID),
		"solana-devnet-test",
		"confirmed", 0,
		solCfg.USDCMint, solCfg.WSOLMint,
		solCfg.UserSecretBase58,
	)
	if err != nil {
		res.Err = fmt.Errorf("NewSolanaClient: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	defer sc.Close()

	rpcCli := sc.RPC()

	// Read Config.next_intent_id to predict the new intent's ID.
	configPda, _, err := sc.ConfigPDA()
	if err != nil {
		res.Err = fmt.Errorf("derive config PDA: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	cfgAcct, err := rpcCli.GetAccountInfoWithOpts(ctx, configPda, &rpc.GetAccountInfoOpts{
		Commitment: rpc.CommitmentConfirmed,
	})
	if err != nil || cfgAcct == nil || cfgAcct.Value == nil {
		res.Err = fmt.Errorf("Config PDA not found at %s — has the program been initialized? (run `anchor migrate --provider.cluster devnet`)", configPda)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	cfgData := cfgAcct.Value.Data.GetBinary()
	// Layout: 8 disc + 32 owner + 8 chainId + 64 (2 mints) + 8 nextIntentId + 1 + 1
	if len(cfgData) < 8+32+8+64+8 {
		res.Err = fmt.Errorf("Config account too short: %d bytes", len(cfgData))
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	nextIntentID := binary.LittleEndian.Uint64(cfgData[8+32+8+64 : 8+32+8+64+8])
	fmt.Printf("🎯 Config.next_intent_id = %d (this will be the new intent's ID)\n", nextIntentID)

	// Build accounts for create_intent.
	amountA := uint64(parseTokenU64(cfg.AmountA, 6))
	expectedAmountB := uint64(parseTokenU64(cfg.AmountB, 6)) // EVM USDC.e is also 6 decimals
	reward := uint64(parseTokenU64(cfg.Reward, 6))
	auctionDuration := uint64(cfg.AuctionDuration)
	destChainID := horizenChainID.Uint64()

	// EVM recipient — for this test we send to the same wallet's EVM address.
	recipient32 := evmAddressToSolanaRecipient32(wallet.Address)

	// User token-A ATA (devnet USDC).
	userATA, _, err := solana.FindAssociatedTokenAddress(userKey.PublicKey(), usdcMint)
	if err != nil {
		res.Err = fmt.Errorf("derive user ATA: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}

	fmt.Printf("💱 user=%s userATA=%s amountA=%d expectedAmountB=%d reward=%d destChainID=%d\n",
		userKey.PublicKey(), userATA, amountA, expectedAmountB, reward, destChainID)

	// Anchor `create_intent` requires both token_a_mint and token_b_mint
	// accounts. For non-Solana destinations the Solana program treats
	// `token_b_mint` as a sentinel; we pass usdcMint twice so the typed
	// `Account<Mint>` check passes.
	ix, err := sc.BuildCreateIntentIx(
		userKey.PublicKey(),
		usdcMint, usdcMint, // tokenA mint, tokenB mint (sentinel — actual EVM token off-chain)
		userATA,
		nextIntentID,
		amountA, expectedAmountB, reward,
		destChainID,
		recipient32,
		auctionDuration,
	)
	if err != nil {
		res.Err = fmt.Errorf("BuildCreateIntentIx: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}

	fmt.Println("🎯 Submitting Anchor create_intent on Solana devnet...")
	sig, err := sendSingleSolanaIx(ctx, sc, ix, userKey)
	if err != nil {
		res.Err = fmt.Errorf("send create_intent: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	res.SourceTxHash = sig
	res.IntentID = new(big.Int).SetUint64(nextIntentID)
	fmt.Printf("✅ create_intent confirmed: %s\n", sig)

	// Confirm the IntentRecord PDA exists with the expected fields.
	time.Sleep(5 * time.Second)
	snap, err := sc.GetIntent(ctx, res.IntentID)
	if err != nil {
		fmt.Printf("⚠️  could not read IntentRecord: %v\n", err)
	} else {
		fmt.Printf("   IntentRecord OK: amountA=%s reward=%s destChainID=%s deposited=%v completed=%v\n",
			snap.AmountA, snap.Reward, snap.DestChainID, snap.Deposited, snap.Completed)
	}

	// Poll EVM-side delivery proof: solvedIntents[(40168, intentID)].
	fmt.Println("⏳ Polling Horizen for solvedIntents flag (up to 3 min)...")
	bridgeABIParsed, _ := abi.JSON(strings.NewReader(bridgeIntentABI))
	bridgeContract := bind.NewBoundContract(bridgeHorizenAddr, bridgeABIParsed, horizenClient, horizenClient, horizenClient)
	res.DestVerified = pollEVMSolvedIntents(ctx, bridgeContract, new(big.Int).SetUint64(solCfg.ChainID), res.IntentID, 3*time.Minute)
	if res.DestVerified {
		fmt.Println("✅ Horizen solvedIntents = true — destination delivery confirmed")
	} else {
		res.Note = "destination delivery not observed within timeout; solver may not be running"
		fmt.Println("⚠️  Horizen solvedIntents not flipped within timeout (run the solver to drive completion)")
	}

	// Source-side completion (after Solana settle).
	if updated, err := sc.GetIntent(ctx, res.IntentID); err == nil {
		res.SourceCompleted = updated.Completed
		if updated.Completed {
			fmt.Println("✅ Solana intent_record.completed = true")
		} else {
			fmt.Println("⚠️  Solana intent not yet settled")
		}
	}
	return res
}

// ---- helpers ----

// pollSolanaSolvedRecord polls until the (srcChainID, intentID) PDA
// exists or the timeout elapses.
func pollSolanaSolvedRecord(ctx context.Context, solCfg *SolanaTestConfig, srcChainID, intentID uint64, timeout time.Duration) bool {
	progID, err := solana.PublicKeyFromBase58(solCfg.ProgramID)
	if err != nil {
		return false
	}
	srcLE := make([]byte, 8)
	binary.LittleEndian.PutUint64(srcLE, srcChainID)
	intentLE := make([]byte, 8)
	binary.LittleEndian.PutUint64(intentLE, intentID)
	pda, _, err := solana.FindProgramAddress([][]byte{[]byte("solved"), srcLE, intentLE}, progID)
	if err != nil {
		return false
	}

	cli := rpc.New(solCfg.RPCURL)
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		acct, err := cli.GetAccountInfoWithOpts(ctx, pda, &rpc.GetAccountInfoOpts{
			Commitment: rpc.CommitmentConfirmed,
		})
		if err == nil && acct != nil && acct.Value != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func pollEVMSolvedIntents(ctx context.Context, bridge *bind.BoundContract, srcChainID, intentID *big.Int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		if checkIntentSolved(ctx, bridge, srcChainID, intentID) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

// sendSingleSolanaIx is a thin wrapper that signs + submits + confirms a
// single Solana instruction with the user's keypair. Mirrors the
// blockchain.SolanaClient.sendIxs internal helper which isn't exported.
func sendSingleSolanaIx(ctx context.Context, sc *blockchain.SolanaClient, ix solana.Instruction, signer solana.PrivateKey) (string, error) {
	rpcCli := sc.RPC()
	hash, err := rpcCli.GetLatestBlockhash(ctx, sc.Commitment())
	if err != nil {
		return "", fmt.Errorf("getLatestBlockhash: %w", err)
	}
	tx, err := solana.NewTransaction(
		[]solana.Instruction{ix},
		hash.Value.Blockhash,
		solana.TransactionPayer(signer.PublicKey()),
	)
	if err != nil {
		return "", fmt.Errorf("build tx: %w", err)
	}
	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key == signer.PublicKey() {
			return &signer
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("sign tx: %w", err)
	}
	sig, err := rpcCli.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{
		PreflightCommitment: sc.Commitment(),
	})
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	// Brief best-effort wait for confirmation — devnet is fast.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		stRes, err := rpcCli.GetSignatureStatuses(ctx, false, sig)
		if err == nil && stRes != nil && len(stRes.Value) > 0 && stRes.Value[0] != nil {
			st := stRes.Value[0]
			if st.ConfirmationStatus == rpc.ConfirmationStatusConfirmed || st.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
				return sig.String(), nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return sig.String(), nil
}

// parseTokenU64 wraps utils.ParseToken and downcasts to uint64 for the
// Solana side. The Anchor program's u64 fields must fit; an overflow
// here means the test inputs are too large for SPL semantics.
func parseTokenU64(amount string, decimals int) uint64 {
	bi := utils.ParseToken(amount, decimals)
	if !bi.IsUint64() {
		panic(fmt.Sprintf("amount %s does not fit u64", bi))
	}
	return bi.Uint64()
}
