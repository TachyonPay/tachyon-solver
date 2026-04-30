//go:build e2e
// +build e2e

package rebalancer

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

// TestRebalancerE2E runs the rebalancer against live Base + Horizen mainnet
// RPCs using the provided private key. Defaults are scaled for a low-funds
// test wallet (sub-dollar amounts).
//
// Run modes:
//
//   1. Dry-run only (no on-chain writes — safe to run any time):
//        REBALANCER_E2E=1 PRIVATE_KEY=0x... \
//          go test -tags=e2e -run TestRebalancerE2E -v -timeout 3m ./pkg/rebalancer
//
//   2. Execute live (will submit real transactions from PRIVATE_KEY):
//        REBALANCER_E2E=1 REBALANCER_E2E_EXECUTE=1 PRIVATE_KEY=0x... \
//          go test -tags=e2e -run TestRebalancerE2E -v -timeout 15m ./pkg/rebalancer
//
// All targets / RPCs / pcts have sensible defaults; override only what you need.
func TestRebalancerE2E(t *testing.T) {
	if os.Getenv("REBALANCER_E2E") != "1" {
		t.Skip("set REBALANCER_E2E=1 to run this test")
	}

	log.SetLevel(log.InfoLevel)
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	pk := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if pk == "" {
		t.Fatal("PRIVATE_KEY env var is required (with or without 0x)")
	}

	baseRPC := envOr("BASE_MAINNET_RPC", "https://base-mainnet.g.alchemy.com/v2/ZnxNrK14O1olpqoj-KVkP")
	hzRPC := envOr("HORIZEN_MAINNET_RPC", "https://horizen.calderachain.xyz/http")

	// Defaults scaled for a low-funds test wallet (~$0.10–$0.30 per leg).
	// Override any of these via env if your wallet has different reserves.
	setIfUnset("REBALANCER_ENABLED", "true")
	setIfUnset("REBALANCER_THRESHOLD_PCT", "0.30")
	setIfUnset("REBALANCER_BUFFER_PCT", "0.10")
	setIfUnset("REBALANCER_SLIPPAGE_PCT", "0.02") // a touch wider for tiny trades
	setIfUnset("TARGET_USDC_BASE", "100000")               // 0.1 USDC
	setIfUnset("TARGET_USDC_HORIZEN", "100000")            // 0.1 USDC.e
	setIfUnset("TARGET_ZEN_BASE", "10000000000000000")     // 0.01 ZEN
	setIfUnset("TARGET_ZEN_HORIZEN", "10000000000000000")  // 0.01 ZEN
	setIfUnset("TARGET_WETH_BASE", "10000000000000")       // 0.00001 WETH (~$0.04)
	// Keep LZ fee buffers small but non-zero for cheap chains.
	setIfUnset("REBALANCER_STARGATE_GAS_BUFFER_WEI", "1000000000000") // 1e12 wei
	setIfUnset("REBALANCER_OFT_GAS_BUFFER_WEI", "1000000000000")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Build per-chain clients. The contract addresses passed here are the
	// solver's bridge contracts; rebalancer doesn't actually call them, but
	// blockchain.NewClient requires a non-empty contract addr.
	baseClient, err := blockchain.NewClient(
		baseRPC,
		"0x52F77B624a9f20d41a60eF550e5792a00747262c", // Base mainnet bridge
		pk,
		new(big.Int).SetUint64(8453),
		"base_mainnet",
		"", // no Safe — use raw EOA
		nil,
	)
	if err != nil {
		t.Fatalf("base client: %v", err)
	}
	defer baseClient.Close()

	hzClient, err := blockchain.NewClient(
		hzRPC,
		"0x1809703DE921177cca0f1c80FB58D788E09AdDB4", // Horizen mainnet bridge
		pk,
		new(big.Int).SetUint64(26514),
		"horizen_mainnet",
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("horizen client: %v", err)
	}
	defer hzClient.Close()

	clients := map[string]*blockchain.Client{
		ChainIDBase:    baseClient,
		ChainIDHorizen: hzClient,
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load rebalancer config: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("rebalancer config disabled — should not happen given defaults above")
	}

	t.Logf("EOA wallet: %s", baseClient.GetAddress().Hex())
	t.Logf("Targets: %d (chain, token) pairs", len(cfg.Targets))
	t.Logf("Threshold: %.0f%%  Buffer: %.0f%%  Slippage: %.2f%%",
		cfg.ThresholdPct*100, cfg.BufferPct*100, cfg.SlippagePct*100)

	// Show native gas balances so user can confirm there's enough ETH/ZEN to pay fees.
	if bal, err := baseClient.GetBalance(ctx, common.Address{}); err == nil {
		t.Logf("Base ETH balance:   %s wei", bal.String())
	}
	if bal, err := hzClient.GetBalance(ctx, common.Address{}); err == nil {
		t.Logf("Horizen ZEN gas:    %s wei", bal.String())
	}

	rb := New(cfg, clients)

	// Wire the EOA signer as a Quoter even for dry-run, so bridge legs
	// that fund a swap show live-priced amounts in the plan.
	if quoterPK := strings.TrimPrefix(pk, "0x"); quoterPK != "" {
		os.Setenv("REBALANCER_TEST_PRIVATE_KEY", quoterPK)
		if signerForQuote, err := NewEOASignerFromEnv(clients); err == nil && signerForQuote != nil {
			rb.SetQuoter(signerForQuote)
			t.Log("Quoter wired (live Aerodrome quotes for bridge sizing)")
		}
	}

	// Always run dry-run first to print the plan.
	snap, plan, err := rb.DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	t.Log("=== Snapshot ===")
	for _, b := range snap.Buckets(cfg) {
		t.Logf("  %s  bal=%s  target=%s  thresh=%s  floor=%s  def=%s  sur=%s",
			b.Key, b.Balance, b.Target, b.Threshold, b.Floor, b.Deficit, b.Surplus)
	}

	t.Logf("=== Plan (%d actions, %d notes) ===", len(plan.Actions), len(plan.Notes))
	for i, a := range plan.Actions {
		t.Logf("  [%d] %-22s %s→%s  %s→%s  amount=%s\n         %s",
			i+1, a.Kind, a.SrcChainID, a.DstChainID, a.SrcToken, a.DstToken,
			a.Amount.String(), a.Reason)
	}
	for _, n := range plan.Notes {
		t.Logf("  note: %s", n)
	}

	if os.Getenv("REBALANCER_E2E_EXECUTE") != "1" {
		t.Log("REBALANCER_E2E_EXECUTE not set — dry-run only, exiting before any tx submission")
		return
	}

	if len(plan.Actions) == 0 {
		t.Log("Plan is empty — nothing to execute")
		return
	}

	// Wire the EOA executor (uses the same private key, since user requested
	// reusing the AWS key for this test).
	os.Setenv("REBALANCER_TEST_PRIVATE_KEY", pk)
	signer, err := NewEOASignerFromEnv(clients)
	if err != nil {
		t.Fatalf("eoa signer: %v", err)
	}
	if signer == nil {
		t.Fatal("eoa signer is nil despite REBALANCER_TEST_PRIVATE_KEY being set")
	}
	rb.SetExecutor(NewEOAExecutor(signer, cfg))

	t.Logf("=== Executing %d action(s) with %s ===", len(plan.Actions), signer.Address().Hex())

	snap2, plan2, txs, err := rb.Execute(ctx)
	for i, h := range txs {
		t.Logf("  tx[%d]: %s", i+1, h)
	}
	if err != nil {
		t.Fatalf("Execute halted after %d tx(s): %v", len(txs), err)
	}
	_ = snap2
	_ = plan2

	// Final snapshot for visual confirmation.
	finalSnap, _, err := rb.DryRun(ctx)
	if err == nil {
		t.Log("=== Post-execute snapshot ===")
		for _, b := range finalSnap.Buckets(cfg) {
			t.Logf("  %s  bal=%s  def=%s  sur=%s",
				b.Key, b.Balance, b.Deficit, b.Surplus)
		}
	}
}

// envOr returns the env var if set, else def.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// setIfUnset sets an env var only if it isn't already set, so callers can
// override defaults from the outside.
func setIfUnset(key, val string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, val)
	}
}

