package relayer

// Integration test: mints real tokens on Solana devnet and verifies the
// on-chain ATA balance increased by exactly the faucet amount.
//
// Run with:
//
//	go test ./pkg/relayer/ -run TestFaucetClaim_Mint -v -timeout 90s
//
// Required env vars (loaded from .env at the repo root or set directly):
//
//	FAUCET_SOLANA_PRIVATE_KEY  — mint authority keypair (base58 or JSON array)
//	FAUCET_TOKEN_MINT          — SPL mint address (defaults to the project token)
//	FAUCET_SOLANA_RPC_URL      — Solana RPC (falls back to SOLANA_RPC_URL)
//
// The test is skipped automatically when FAUCET_SOLANA_PRIVATE_KEY is unset.

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/joho/godotenv"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
)

const (
	integrationTokenMint = "CLQdHANA2Z2tjka21NqBkzoytaH18JtA2Q56Bt4tHPj"
)

// loadIntegrationEnv loads .env from the tachyon-prod root (two directories
// up from this test file's package directory).
func loadIntegrationEnv() {
	_ = godotenv.Load("../../.env")
}

func integrationFaucet(t *testing.T) (*FaucetService, *rpc.Client) {
	t.Helper()
	loadIntegrationEnv()

	authKey := strings.TrimSpace(os.Getenv("FAUCET_SOLANA_PRIVATE_KEY"))
	if authKey == "" {
		t.Skip("FAUCET_SOLANA_PRIVATE_KEY not set — skipping integration test")
	}

	rpcURL := strings.TrimSpace(os.Getenv("FAUCET_SOLANA_RPC_URL"))
	if rpcURL == "" {
		rpcURL = strings.TrimSpace(os.Getenv("SOLANA_RPC_URL"))
	}
	if rpcURL == "" {
		t.Skip("no Solana RPC URL configured (set FAUCET_SOLANA_RPC_URL or SOLANA_RPC_URL)")
	}

	tokenMint := strings.TrimSpace(os.Getenv("FAUCET_TOKEN_MINT"))
	if tokenMint == "" {
		tokenMint = integrationTokenMint
	}

	store := tempStore(t)

	cfg := &config.FaucetConfig{
		Enabled:       true,
		TokenMint:     tokenMint,
		TokenDecimals: 6,
		AmountRaw:     100 * 1_000_000, // placeholder; newFaucetService will query chain
		CooldownHours: 24,
		MintAuthKey:   authKey,
		RPCURL:        rpcURL,
	}

	f, err := newFaucetService(cfg, store)
	if err != nil {
		t.Fatalf("newFaucetService: %v", err)
	}
	if f == nil {
		t.Fatal("faucet service is nil despite Enabled=true")
	}

	return f, rpc.New(rpcURL)
}

// ataBalance returns the token balance (raw units) of the ATA for (wallet, mint).
// Returns 0 and no error when the ATA does not yet exist.
func ataBalance(t *testing.T, rpcClient *rpc.Client, wallet, mint solana.PublicKey) *big.Int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ata, _, err := solana.FindAssociatedTokenAddress(wallet, mint)
	if err != nil {
		t.Fatalf("FindAssociatedTokenAddress: %v", err)
	}

	res, err := rpcClient.GetTokenAccountBalance(ctx, ata, rpc.CommitmentConfirmed)
	if err != nil {
		// ATA doesn't exist yet → balance is 0.
		return big.NewInt(0)
	}
	if res == nil || res.Value == nil {
		return big.NewInt(0)
	}
	bal, ok := new(big.Int).SetString(res.Value.Amount, 10)
	if !ok {
		t.Fatalf("could not parse token balance %q", res.Value.Amount)
	}
	return bal
}

// TestFaucetClaim_Mint sends a real mint_to transaction on Solana devnet and
// asserts the recipient's ATA balance increased by exactly the faucet amount.
func TestFaucetClaim_Mint(t *testing.T) {
	f, rpcClient := integrationFaucet(t)

	mintPub, err := solana.PublicKeyFromBase58(integrationTokenMint)
	if err != nil {
		t.Fatalf("parse mint: %v", err)
	}

	// Mint to a freshly generated wallet so we start from a known baseline
	// and don't interfere with any real balances.
	recipient := solana.NewWallet()
	recipientPub := recipient.PublicKey()

	t.Logf("mint authority : %s", f.mintAuth.PublicKey())
	t.Logf("recipient      : %s", recipientPub)
	t.Logf("token mint     : %s", mintPub)
	t.Logf("amount (raw)   : %d", f.amountRaw)

	// Capture balance before the mint.
	before := ataBalance(t, rpcClient, recipientPub, mintPub)
	t.Logf("balance before : %s", before)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	result, err := f.FaucetClaim(ctx, recipientPub.String())
	if err != nil {
		t.Fatalf("FaucetClaim: %v", err)
	}

	t.Logf("tx signature   : %s", result.Signature)

	// Wait a moment for the confirmation to propagate to all RPC nodes before
	// we query balance (confirmed commitment already waited in sendFaucetTx,
	// but give the RPC another second for read-after-write consistency).
	time.Sleep(2 * time.Second)

	after := ataBalance(t, rpcClient, recipientPub, mintPub)
	t.Logf("balance after  : %s", after)

	diff := new(big.Int).Sub(after, before)
	expected := new(big.Int).SetUint64(f.amountRaw)

	if diff.Cmp(expected) != 0 {
		t.Errorf("balance delta = %s, want %s", diff, expected)
	}
}

// TestFaucetClaim_Mint_ToSelf mints to the faucet authority's own wallet to
// confirm the authority has an ATA and can receive its own token.
func TestFaucetClaim_Mint_ToSelf(t *testing.T) {
	f, rpcClient := integrationFaucet(t)

	mintPub, err := solana.PublicKeyFromBase58(integrationTokenMint)
	if err != nil {
		t.Fatalf("parse mint: %v", err)
	}

	selfPub := f.mintAuth.PublicKey()
	t.Logf("mint authority / recipient: %s", selfPub)

	before := ataBalance(t, rpcClient, selfPub, mintPub)
	t.Logf("balance before : %s", before)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	result, err := f.FaucetClaim(ctx, selfPub.String())
	if err != nil {
		t.Fatalf("FaucetClaim: %v", err)
	}
	t.Logf("tx signature   : %s", result.Signature)

	time.Sleep(2 * time.Second)

	after := ataBalance(t, rpcClient, selfPub, mintPub)
	t.Logf("balance after  : %s", after)

	diff := new(big.Int).Sub(after, before)
	expected := new(big.Int).SetUint64(f.amountRaw)

	if diff.Cmp(expected) != 0 {
		t.Errorf("balance delta = %s, want %s", diff, expected)
	}
}

// TestFaucetClaim_CooldownPersistedAcrossInstances verifies that the cooldown
// written by one FaucetService instance is enforced by a second instance
// sharing the same PebbleDB store — simulating a relayer restart.
func TestFaucetClaim_CooldownPersistedAcrossInstances(t *testing.T) {
	loadIntegrationEnv()

	authKey := strings.TrimSpace(os.Getenv("FAUCET_SOLANA_PRIVATE_KEY"))
	if authKey == "" {
		t.Skip("FAUCET_SOLANA_PRIVATE_KEY not set — skipping integration test")
	}

	rpcURL := strings.TrimSpace(os.Getenv("FAUCET_SOLANA_RPC_URL"))
	if rpcURL == "" {
		rpcURL = strings.TrimSpace(os.Getenv("SOLANA_RPC_URL"))
	}
	if rpcURL == "" {
		t.Skip("no Solana RPC URL — skipping integration test")
	}

	// Both instances share the same store dir.
	store := tempStore(t)

	buildSvc := func() *FaucetService {
		cfg := &config.FaucetConfig{
			Enabled:       true,
			TokenMint:     integrationTokenMint,
			TokenDecimals: 6,
			AmountRaw:     100 * 1_000_000,
			CooldownHours: 24,
			MintAuthKey:   authKey,
			RPCURL:        rpcURL,
		}
		f, err := newFaucetService(cfg, store)
		if err != nil {
			t.Fatalf("newFaucetService: %v", err)
		}
		return f
	}

	instance1 := buildSvc()

	wallet := solana.NewWallet().PublicKey().String()
	t.Logf("test wallet: %s", wallet)

	// Write a fake recent claim via instance 1.
	writeClaimAt(t, instance1, wallet, time.Now())

	// Instance 2 (simulating restart) must see the same cooldown.
	instance2 := buildSvc()

	ok, nextAt := instance2.checkCooldown(wallet)
	if ok {
		t.Error("instance 2 should see the cooldown written by instance 1")
	}
	if nextAt.IsZero() {
		t.Error("expected a non-zero nextAt from instance 2")
	}
	t.Logf("cooldown enforced by instance 2; next claim at %s", nextAt.UTC().Format(time.RFC3339))
}
