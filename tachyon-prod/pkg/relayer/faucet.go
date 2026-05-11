package relayer

import (
	"context"
	"encoding/binary" // retained for checkCooldown (re-enable when cooldown is turned back on)
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	atok "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

// FaucetService mints a fixed amount of an SPL token to any Solana wallet
// that requests it, subject to a per-wallet cooldown enforced by PebbleDB.
type FaucetService struct {
	mintAuth  solana.PrivateKey
	mintPub   solana.PublicKey
	rpcClient *rpc.Client
	amountRaw uint64
	cooldown  time.Duration
	store     *storage.Store
	mu        sync.Mutex
}

func newFaucetService(cfg *config.FaucetConfig, store *storage.Store) (*FaucetService, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	if cfg.RPCURL == "" {
		return nil, fmt.Errorf("faucet: no Solana RPC URL (set FAUCET_SOLANA_RPC_URL or configure a Solana chain)")
	}

	key, err := parseFaucetPrivateKey(cfg.MintAuthKey)
	if err != nil {
		return nil, fmt.Errorf("faucet: parse FAUCET_SOLANA_PRIVATE_KEY: %w", err)
	}

	mintPub, err := solana.PublicKeyFromBase58(cfg.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("faucet: parse token mint %q: %w", cfg.TokenMint, err)
	}

	cooldown := time.Duration(cfg.CooldownHours) * time.Hour

	f := &FaucetService{
		mintAuth:  key,
		mintPub:   mintPub,
		rpcClient: rpc.New(cfg.RPCURL),
		amountRaw: cfg.AmountRaw,
		cooldown:  cooldown,
		store:     store,
	}

	// Query actual decimals from the mint account and recompute amountRaw so
	// we always send exactly 100 tokens regardless of the configured default.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if supply, err := f.rpcClient.GetTokenSupply(ctx, mintPub, rpc.CommitmentConfirmed); err == nil && supply != nil && supply.Value != nil {
		decimals := supply.Value.Decimals
		amount := uint64(100)
		for i := uint8(0); i < decimals; i++ {
			amount *= 10
		}
		f.amountRaw = amount
		log.WithFields(log.Fields{
			"mint":     mintPub.String(),
			"decimals": decimals,
			"amount":   f.amountRaw,
		}).Info("Faucet: resolved token decimals from chain")
	} else {
		log.WithError(err).Warn("Faucet: could not query token decimals, using configured default")
	}

	log.WithFields(log.Fields{
		"mint":      mintPub.String(),
		"authority": key.PublicKey().String(),
		"cooldown":  cooldown,
		"amountRaw": f.amountRaw,
	}).Info("Faucet service initialized")

	return f, nil
}

// parseFaucetPrivateKey accepts either the Solana CLI JSON byte-array format
// (e.g. the contents of ~/.config/solana/id.json) or a base58-encoded secret.
func parseFaucetPrivateKey(keyStr string) (solana.PrivateKey, error) {
	keyStr = strings.TrimSpace(keyStr)
	if strings.HasPrefix(keyStr, "[") {
		var ints []int
		if err := json.Unmarshal([]byte(keyStr), &ints); err != nil {
			return nil, fmt.Errorf("JSON byte array: %w", err)
		}
		b := make([]byte, len(ints))
		for i, v := range ints {
			b[i] = byte(v)
		}
		return solana.PrivateKey(b), nil
	}
	return solana.PrivateKeyFromBase58(keyStr)
}

// FaucetClaimResult is returned by Claim.
type FaucetClaimResult struct {
	Signature string `json:"signature"`
	Wallet    string `json:"wallet"`
	AmountRaw uint64 `json:"amountRaw"`
	TokenMint string `json:"tokenMint"`
}

// FaucetStatusResult is returned by Status.
type FaucetStatusResult struct {
	CanClaim        bool   `json:"canClaim"`
	NextClaimAt     *int64 `json:"nextClaimAt,omitempty"` // unix timestamp
	CooldownSeconds int    `json:"cooldownSeconds"`
	TokenMint       string `json:"tokenMint"`
	AmountRaw       uint64 `json:"amountRaw"`
}

// FaucetStatus reports whether a wallet can currently claim.
func (f *FaucetService) FaucetStatus(walletAddr string) (*FaucetStatusResult, error) {
	if _, err := solana.PublicKeyFromBase58(walletAddr); err != nil {
		return nil, fmt.Errorf("invalid wallet address: %w", err)
	}
	// Cooldown disabled — always claimable.
	// canClaim, nextAt := f.checkCooldown(walletAddr)
	res := &FaucetStatusResult{
		CanClaim:        true,
		CooldownSeconds: 0,
		TokenMint:       f.mintPub.String(),
		AmountRaw:       f.amountRaw,
	}
	return res, nil
}

// FaucetClaim mints amountRaw tokens to walletAddr if the cooldown has passed.
// Returns the transaction signature on success.
func (f *FaucetService) FaucetClaim(ctx context.Context, walletAddr string) (*FaucetClaimResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	recipient, err := solana.PublicKeyFromBase58(walletAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid wallet address: %w", err)
	}

	// Cooldown disabled — any wallet can claim any number of times.
	// canClaim, nextAt := f.checkCooldown(walletAddr)
	// if !canClaim {
	// 	return nil, fmt.Errorf("rate limited: next claim available at %s UTC", nextAt.UTC().Format(time.RFC3339))
	// }

	ata, _, err := solana.FindAssociatedTokenAddress(recipient, f.mintPub)
	if err != nil {
		return nil, fmt.Errorf("derive ATA: %w", err)
	}

	// create_idempotent_ata is a no-op if the ATA already exists, so it is
	// always safe to include it.
	createATA := atok.NewCreateIdempotentInstruction(
		f.mintAuth.PublicKey(),
		recipient,
		f.mintPub,
	).Build()

	mintTo := token.NewMintToInstruction(
		f.amountRaw,
		f.mintPub,
		ata,
		f.mintAuth.PublicKey(),
		nil,
	).Build()

	sig, err := f.sendFaucetTx(ctx, []solana.Instruction{createATA, mintTo})
	if err != nil {
		return nil, fmt.Errorf("send faucet tx: %w", err)
	}

	// Cooldown disabled — skip persisting claim timestamp.
	// ts := make([]byte, 8)
	// binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
	// if storeErr := f.store.Put(faucetStoreKey(walletAddr), ts); storeErr != nil {
	// 	log.WithError(storeErr).Warn("Faucet: failed to persist claim timestamp")
	// }

	log.WithFields(log.Fields{
		"wallet":    walletAddr,
		"signature": sig,
		"amount":    f.amountRaw,
		"mint":      f.mintPub.String(),
	}).Info("Faucet claim successful")

	return &FaucetClaimResult{
		Signature: sig,
		Wallet:    walletAddr,
		AmountRaw: f.amountRaw,
		TokenMint: f.mintPub.String(),
	}, nil
}

func faucetStoreKey(walletAddr string) string {
	return "faucet:claim:" + walletAddr
}

// checkCooldown returns (true, zero) if the wallet may claim, or (false,
// nextClaimAt) if the cooldown has not yet elapsed.
func (f *FaucetService) checkCooldown(walletAddr string) (bool, time.Time) {
	data, err := f.store.Get(faucetStoreKey(walletAddr))
	if err != nil || len(data) < 8 {
		return true, time.Time{}
	}
	lastUnix := int64(binary.BigEndian.Uint64(data))
	nextAt := time.Unix(lastUnix, 0).Add(f.cooldown)
	if time.Now().Before(nextAt) {
		return false, nextAt
	}
	return true, time.Time{}
}

// sendFaucetTx builds, signs, sends, and polls for confirmation of a Solana
// transaction. Mirrors the sendIxs logic from SolanaClient.
func (f *FaucetService) sendFaucetTx(ctx context.Context, instructions []solana.Instruction) (string, error) {
	hash, err := f.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentProcessed)
	if err != nil {
		return "", fmt.Errorf("getLatestBlockhash: %w", err)
	}

	tx, err := solana.NewTransaction(
		instructions,
		hash.Value.Blockhash,
		solana.TransactionPayer(f.mintAuth.PublicKey()),
	)
	if err != nil {
		return "", fmt.Errorf("build tx: %w", err)
	}

	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key == f.mintAuth.PublicKey() {
			pk := f.mintAuth
			return &pk
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("sign tx: %w", err)
	}

	sig, err := f.rpcClient.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{
		SkipPreflight:       true,
		PreflightCommitment: rpc.CommitmentConfirmed,
	})
	if err != nil {
		return "", fmt.Errorf("send tx: %w", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		stRes, err := f.rpcClient.GetSignatureStatuses(ctx, false, sig)
		if err == nil && stRes != nil && len(stRes.Value) > 0 && stRes.Value[0] != nil {
			st := stRes.Value[0]
			if st.Err != nil {
				return sig.String(), fmt.Errorf("tx reverted: %v", st.Err)
			}
			if st.ConfirmationStatus == rpc.ConfirmationStatusConfirmed ||
				st.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
				return sig.String(), nil
			}
		}
		select {
		case <-ctx.Done():
			return sig.String(), ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return sig.String(), fmt.Errorf("tx not confirmed within 60s: %s", sig)
}
