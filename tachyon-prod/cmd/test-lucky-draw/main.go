// test-lucky-draw: complete end-to-end flow test for the lucky draw service.
//
// Tests every stage in sequence and prints a pass/fail summary:
//
//	Phase 1  EIP-712 sign ↔ recover roundtrip (no RPC)
//	Phase 2  Wrong-chain and wrong-proxy sig rejection (no RPC)
//	Phase 3  Full service flow against a real EVM testnet
//	  3a     Entry accepted, USDC dripped, txHash returned
//	  3b     Second entry with same stealth key rejected
//	  3c     Status reflects the win
//	  3d     Different proxy, same stealth key → still rejected (stealth dedup)
//
// # Required env for Phase 3
//
//	TEST_LD_STEALTH_KEY   hex private key of the stealth key that owns the proxy Safe
//	TEST_LD_PROXY_ADDRESS 0x address of the proxy Safe (must hold > 0 USDC)
//	TEST_LD_CHAIN_ID      decimal chain ID to test on (must be in LUCKY_DRAW_CHAIN_N_*)
//
// Plus the full set of LUCKY_DRAW_* env vars used by luckydraw.LoadConfig().
//
// Run:
//
//	go run ./cmd/test-lucky-draw/
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
	"github.com/zk-tachyon/tachyon-prod/pkg/luckydraw"
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

func main() {
	godotenv.Load()

	pass := 0
	fail := 0

	check := func(label string, err error) {
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", label, err)
			fail++
		} else {
			fmt.Printf("  ✓ %s\n", label)
			pass++
		}
	}

	// ─── Phase 1: EIP-712 cryptography — no RPC required ─────────────────────
	fmt.Println("\n=== Phase 1: EIP-712 sign ↔ recover roundtrip ===")

	stealthKey, err := crypto.GenerateKey()
	if err != nil {
		fmt.Printf("FATAL: generate key: %v\n", err)
		os.Exit(1)
	}
	stealthAddr := crypto.PubkeyToAddress(stealthKey.PublicKey)
	proxyAddr := common.HexToAddress("0xDeaDbeefdEAdbeefdEadbEEFdeadbeEFdEaDbeeF")
	chainID := big.NewInt(8453)

	// Sign with v=0/1 (raw go-ethereum output), then adjust to v=27/28 for MetaMask compat.
	sig, err := stealth.SignLuckyDrawEntry(stealthKey, proxyAddr, chainID)
	check("SignLuckyDrawEntry produces 65-byte sig", func() error {
		if err != nil {
			return err
		}
		if len(sig) != 65 {
			return fmt.Errorf("expected 65 bytes, got %d", len(sig))
		}
		return nil
	}())

	// Adjust v before calling RecoverLuckyDrawEntry (service does the same internally).
	sigAdj := make([]byte, 65)
	copy(sigAdj, sig)
	sigAdj[64] += 27

	recovered, err := stealth.RecoverLuckyDrawEntry(sigAdj, proxyAddr, chainID)
	check("RecoverLuckyDrawEntry returns correct stealth address", func() error {
		if err != nil {
			return err
		}
		if recovered != stealthAddr {
			return fmt.Errorf("got %s, want %s", recovered.Hex(), stealthAddr.Hex())
		}
		return nil
	}())

	digest := stealth.LuckyDrawEntryDigest(proxyAddr, chainID)
	check("LuckyDrawEntryDigest is non-zero", func() error {
		if digest == (common.Hash{}) {
			return fmt.Errorf("digest is zero hash")
		}
		return nil
	}())

	// ─── Phase 2: sig binds to exact (proxy, chain) — no RPC ─────────────────
	fmt.Println("\n=== Phase 2: sig binds to exact (proxy, chainId) ===")

	wrongChainSig := make([]byte, 65)
	copy(wrongChainSig, sigAdj)
	recoveredWrongChain, _ := stealth.RecoverLuckyDrawEntry(wrongChainSig, proxyAddr, big.NewInt(26514)) // Horizen
	check("Sig for Base does NOT recover correctly for Horizen", func() error {
		if recoveredWrongChain == stealthAddr {
			return fmt.Errorf("should have failed recovery or returned wrong address")
		}
		return nil
	}())

	wrongProxy := common.HexToAddress("0x0000000000000000000000000000000000000001")
	recoveredWrongProxy, _ := stealth.RecoverLuckyDrawEntry(sigAdj, wrongProxy, chainID)
	check("Sig for proxy A does NOT recover correctly for proxy B", func() error {
		if recoveredWrongProxy == stealthAddr {
			return fmt.Errorf("should have failed recovery or returned wrong address")
		}
		return nil
	}())

	// ─── Phase 3: full service flow — requires testnet env vars ──────────────
	fmt.Println("\n=== Phase 3: full service flow (testnet) ===")

	stealthKeyHex := os.Getenv("TEST_LD_STEALTH_KEY")
	proxyAddrStr := os.Getenv("TEST_LD_PROXY_ADDRESS")
	testChainIDStr := os.Getenv("TEST_LD_CHAIN_ID")

	if stealthKeyHex == "" || proxyAddrStr == "" || testChainIDStr == "" {
		fmt.Println("  SKIPPED — set TEST_LD_STEALTH_KEY, TEST_LD_PROXY_ADDRESS, TEST_LD_CHAIN_ID to run")
		fmt.Printf("\nResult: %d passed, %d failed (Phase 3 skipped)\n", pass, fail)
		if fail > 0 {
			os.Exit(1)
		}
		return
	}

	testStealthKey, err := crypto.HexToECDSA(strings.TrimPrefix(stealthKeyHex, "0x"))
	if err != nil {
		fmt.Printf("  FATAL: invalid TEST_LD_STEALTH_KEY: %v\n", err)
		os.Exit(1)
	}
	testStealthAddr := crypto.PubkeyToAddress(testStealthKey.PublicKey)
	testProxyAddr := common.HexToAddress(proxyAddrStr)
	testChainID, ok := new(big.Int).SetString(testChainIDStr, 10)
	if !ok {
		fmt.Printf("  FATAL: invalid TEST_LD_CHAIN_ID: %s\n", testChainIDStr)
		os.Exit(1)
	}

	fmt.Printf("  stealth address : %s\n", testStealthAddr.Hex())
	fmt.Printf("  proxy Safe      : %s\n", testProxyAddr.Hex())
	fmt.Printf("  chain ID        : %s\n", testChainID.String())

	cfg, err := luckydraw.LoadConfig()
	if err != nil {
		fmt.Printf("  FATAL: LoadConfig: %v\n", err)
		os.Exit(1)
	}

	// Use an isolated temp DB so tests never pollute production state.
	tmpDir, err := os.MkdirTemp("", "luckydraw-test-*")
	if err != nil {
		fmt.Printf("  FATAL: mktemp: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	store, err := storage.NewStore(tmpDir)
	if err != nil {
		fmt.Printf("  FATAL: open DB: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	svc, err := luckydraw.NewService(cfg, store)
	if err != nil {
		fmt.Printf("  FATAL: NewService: %v\n", err)
		os.Exit(1)
	}

	// Sign EIP-712.
	rawSig, err := stealth.SignLuckyDrawEntry(testStealthKey, testProxyAddr, testChainID)
	if err != nil {
		fmt.Printf("  FATAL: signing: %v\n", err)
		os.Exit(1)
	}
	rawSig[64] += 27

	req := luckydraw.EnterRequest{
		ProxyAddress: testProxyAddr.Hex(),
		ChainID:      testChainIDStr,
		Signature:    "0x" + hex.EncodeToString(rawSig),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 3a: first entry accepted — may or may not win depending on WinChance.
	fmt.Println("\n  [3a] first entry →")
	result, err := svc.Enter(ctx, req)
	check("Enter accepted (no error)", err)
	if err == nil {
		fmt.Printf("       won: %v\n", result.Won)
		if result.Won {
			fmt.Printf("       drew %d USDC (raw=%s)\n", result.AmountUSDC, result.AmountRaw)
			fmt.Printf("       txHash: %s\n", result.TxHash)
			check("winning amount in configured range", func() error {
				if result.AmountUSDC < cfg.MinUSDC || result.AmountUSDC > cfg.MaxUSDC {
					return fmt.Errorf("got %d, want [%d, %d]", result.AmountUSDC, cfg.MinUSDC, cfg.MaxUSDC)
				}
				return nil
			}())
			check("txHash non-empty on win", func() error {
				if result.TxHash == "" || result.TxHash == "0x" {
					return fmt.Errorf("empty txHash")
				}
				return nil
			}())
		} else {
			fmt.Printf("       no drip (did not win — WinChance=%.2f)\n", cfg.WinChance)
			check("losing entry has zero amount", func() error {
				if result.AmountUSDC != 0 {
					return fmt.Errorf("expected 0, got %d", result.AmountUSDC)
				}
				return nil
			}())
			check("losing entry has empty txHash", func() error {
				if result.TxHash != "" {
					return fmt.Errorf("expected empty, got %s", result.TxHash)
				}
				return nil
			}())
		}
	}

	// 3b: identical request rejected.
	fmt.Println("\n  [3b] second entry with same stealth key →")
	_, err2 := svc.Enter(ctx, req)
	check("duplicate entry rejected", func() error {
		if err2 == nil {
			return fmt.Errorf("expected error, got nil")
		}
		return nil
	}())
	if err2 != nil {
		fmt.Printf("       rejection reason: %v\n", err2)
	}

	// 3c: status reflects the entry (win or loss).
	fmt.Println("\n  [3c] status check →")
	status, err := svc.Status(testStealthAddr.Hex())
	check("Status returned without error", err)
	if err == nil {
		check("status.Entered == true", func() error {
			if !status.Entered {
				return fmt.Errorf("Entered is false")
			}
			return nil
		}())
		if result != nil {
			check("status.Won matches entry", func() error {
				if status.Won != result.Won {
					return fmt.Errorf("got Won=%v, want Won=%v", status.Won, result.Won)
				}
				return nil
			}())
			check("status.AmountUSDC matches entry", func() error {
				if status.AmountUSDC != result.AmountUSDC {
					return fmt.Errorf("got %d, want %d", status.AmountUSDC, result.AmountUSDC)
				}
				return nil
			}())
			if result.Won {
				check("status.TxHash matches entry", func() error {
					if status.TxHash != result.TxHash {
						return fmt.Errorf("got %s, want %s", status.TxHash, result.TxHash)
					}
					return nil
				}())
			}
		}
		fmt.Printf("       entered=%v won=%v amount=%d USDC chain=%s\n",
			status.Entered, status.Won, status.AmountUSDC, status.ChainID)
	}

	// 3d: different proxy, same stealth key → dedup fires on stealth address.
	fmt.Println("\n  [3d] same stealth key, different proxy →")
	differentProxy := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	altSig, _ := stealth.SignLuckyDrawEntry(testStealthKey, differentProxy, testChainID)
	altSig[64] += 27
	altReq := luckydraw.EnterRequest{
		ProxyAddress: differentProxy.Hex(),
		ChainID:      testChainIDStr,
		Signature:    "0x" + hex.EncodeToString(altSig),
	}
	_, altErr := svc.Enter(ctx, altReq)
	check("entry with different proxy but same stealth key rejected", func() error {
		if altErr == nil {
			return fmt.Errorf("expected rejection, got nil")
		}
		return nil
	}())
	if altErr != nil {
		fmt.Printf("       rejection reason: %v\n", altErr)
	}

	// ─── Summary ──────────────────────────────────────────────────────────────
	fmt.Printf("\n═══ Result: %d passed, %d failed ═══\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}
