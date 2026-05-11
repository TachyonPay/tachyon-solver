package relayer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

// --- helpers -----------------------------------------------------------------

func tempStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.NewStore(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func makeFaucet(t *testing.T, cooldown time.Duration) *FaucetService {
	t.Helper()
	key := solana.NewWallet().PrivateKey
	mint := solana.NewWallet().PublicKey()
	return &FaucetService{
		mintAuth:  key,
		mintPub:   mint,
		rpcClient: nil, // not used in unit tests
		amountRaw: 100_000_000, // 100 tokens at 6 decimals
		cooldown:  cooldown,
		store:     tempStore(t),
	}
}

// writeClaimAt manually sets the stored claim timestamp for a wallet.
func writeClaimAt(t *testing.T, f *FaucetService, wallet string, at time.Time) {
	t.Helper()
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(at.Unix()))
	if err := f.store.Put(faucetStoreKey(wallet), ts); err != nil {
		t.Fatalf("writeClaimAt: %v", err)
	}
}

// --- parseFaucetPrivateKey ---------------------------------------------------

func TestParseFaucetPrivateKey_Base58(t *testing.T) {
	orig := solana.NewWallet().PrivateKey
	b58 := orig.String()

	got, err := parseFaucetPrivateKey(b58)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PublicKey() != orig.PublicKey() {
		t.Errorf("pubkey mismatch: got %s, want %s", got.PublicKey(), orig.PublicKey())
	}
}

func TestParseFaucetPrivateKey_JSONArray(t *testing.T) {
	orig := solana.NewWallet().PrivateKey

	// Build the same JSON byte-array that `cat ~/.config/solana/id.json` emits.
	ints := make([]int, len(orig))
	for i, b := range orig {
		ints[i] = int(b)
	}
	raw, _ := json.Marshal(ints)

	got, err := parseFaucetPrivateKey(string(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PublicKey() != orig.PublicKey() {
		t.Errorf("pubkey mismatch: got %s, want %s", got.PublicKey(), orig.PublicKey())
	}
}

func TestParseFaucetPrivateKey_JSONArrayFromFile(t *testing.T) {
	// Simulate writing a keypair to a temp file the same way solana-keygen does,
	// then reading it back via parseFaucetPrivateKey.
	orig := solana.NewWallet().PrivateKey

	ints := make([]int, len(orig))
	for i, b := range orig {
		ints[i] = int(b)
	}
	raw, _ := json.Marshal(ints)

	path := filepath.Join(t.TempDir(), "id.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	fileContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := parseFaucetPrivateKey(string(fileContent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PublicKey() != orig.PublicKey() {
		t.Errorf("pubkey mismatch via file round-trip")
	}
}

func TestParseFaucetPrivateKey_Invalid(t *testing.T) {
	cases := []struct{ name, input string }{
		{"empty", ""},
		{"garbage", "not-valid-base58-or-json!!!"},
		{"bad json", "[1,2,not-a-number]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFaucetPrivateKey(tc.input)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// --- checkCooldown -----------------------------------------------------------

func TestCheckCooldown_FirstClaim(t *testing.T) {
	f := makeFaucet(t, 24*time.Hour)
	wallet := solana.NewWallet().PublicKey().String()

	ok, next := f.checkCooldown(wallet)
	if !ok {
		t.Error("expected canClaim=true for a never-claimed wallet")
	}
	if !next.IsZero() {
		t.Errorf("expected zero nextAt, got %s", next)
	}
}

func TestCheckCooldown_InsideCooldown(t *testing.T) {
	f := makeFaucet(t, 24*time.Hour)
	wallet := solana.NewWallet().PublicKey().String()

	// Simulate a claim that happened 1 hour ago.
	writeClaimAt(t, f, wallet, time.Now().Add(-1*time.Hour))

	ok, nextAt := f.checkCooldown(wallet)
	if ok {
		t.Error("expected canClaim=false while inside cooldown")
	}
	if nextAt.IsZero() {
		t.Error("expected a non-zero nextAt")
	}
	// nextAt should be roughly now + 23 hours.
	remaining := time.Until(nextAt)
	if remaining < 22*time.Hour || remaining > 24*time.Hour {
		t.Errorf("nextAt looks wrong: remaining=%v", remaining)
	}
}

func TestCheckCooldown_CooldownExpired(t *testing.T) {
	f := makeFaucet(t, 24*time.Hour)
	wallet := solana.NewWallet().PublicKey().String()

	// Simulate a claim that happened 25 hours ago.
	writeClaimAt(t, f, wallet, time.Now().Add(-25*time.Hour))

	ok, next := f.checkCooldown(wallet)
	if !ok {
		t.Error("expected canClaim=true after cooldown expired")
	}
	if !next.IsZero() {
		t.Errorf("expected zero nextAt after cooldown expired, got %s", next)
	}
}

func TestCheckCooldown_ShortCooldown(t *testing.T) {
	// Use a 2-second cooldown so Unix-second truncation doesn't cause
	// a freshly-written timestamp to appear already expired.
	cooldown := 2 * time.Second
	f := makeFaucet(t, cooldown)
	wallet := solana.NewWallet().PublicKey().String()

	// Set claim 1 second in the future so it's clearly inside the 2s window.
	writeClaimAt(t, f, wallet, time.Now().Add(1*time.Second))

	ok, _ := f.checkCooldown(wallet)
	if ok {
		t.Error("expected canClaim=false when claim timestamp is in the future")
	}

	// Write a claim that expired 1 second ago (well outside the 2s window
	// because we back-date by 3s).
	writeClaimAt(t, f, wallet, time.Now().Add(-3*time.Second))

	ok, _ = f.checkCooldown(wallet)
	if !ok {
		t.Error("expected canClaim=true when cooldown has expired")
	}
}

// --- FaucetStatus ------------------------------------------------------------

func TestFaucetStatus_CanClaim(t *testing.T) {
	f := makeFaucet(t, 24*time.Hour)
	wallet := solana.NewWallet().PublicKey().String()

	res, err := f.FaucetStatus(wallet)
	if err != nil {
		t.Fatalf("FaucetStatus error: %v", err)
	}
	// Cooldown disabled — always claimable.
	if !res.CanClaim {
		t.Error("expected CanClaim=true (cooldown disabled)")
	}
	if res.NextClaimAt != nil {
		t.Error("expected NextClaimAt=nil (cooldown disabled)")
	}
	if res.CooldownSeconds != 0 {
		t.Errorf("CooldownSeconds = %d, want 0 (cooldown disabled)", res.CooldownSeconds)
	}
	if res.AmountRaw != f.amountRaw {
		t.Errorf("AmountRaw = %d, want %d", res.AmountRaw, f.amountRaw)
	}
	if res.TokenMint != f.mintPub.String() {
		t.Errorf("TokenMint = %s, want %s", res.TokenMint, f.mintPub.String())
	}
}

func TestFaucetStatus_OnCooldown(t *testing.T) {
	f := makeFaucet(t, 24*time.Hour)
	wallet := solana.NewWallet().PublicKey().String()

	writeClaimAt(t, f, wallet, time.Now().Add(-2*time.Hour))

	res, err := f.FaucetStatus(wallet)
	if err != nil {
		t.Fatalf("FaucetStatus error: %v", err)
	}
	// Cooldown disabled — still returns canClaim=true regardless of stored timestamp.
	if !res.CanClaim {
		t.Error("expected CanClaim=true (cooldown disabled, stored timestamp is ignored)")
	}
}

func TestFaucetStatus_InvalidWallet(t *testing.T) {
	f := makeFaucet(t, 24*time.Hour)

	_, err := f.FaucetStatus("not-a-valid-pubkey")
	if err == nil {
		t.Error("expected error for invalid wallet address")
	}
}

// --- FaucetClaim (rate-limiter path, no RPC) ---------------------------------

// TestFaucetClaim_RateLimited is intentionally removed — cooldown is disabled.

func TestFaucetClaim_InvalidWallet(t *testing.T) {
	f := makeFaucet(t, 24*time.Hour)

	_, err := f.FaucetClaim(context.Background(), "bad-address")
	if err == nil {
		t.Fatal("expected error for invalid wallet address")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
