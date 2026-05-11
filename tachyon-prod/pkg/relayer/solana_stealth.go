// Solana-side stealth salt storage. Mirrors the EVM stealth flow in
// stealth.go but uses ed25519 signatures (Solana wallet) for the
// retrieval auth, instead of EIP-712 / ECDSA.
//
// Flow:
//   1. Sender (or test) generates a salt and calls register_stealth(salt)
//      on Solana — this requires the recipient's wallet sig.
//   2. Sender POSTs the salt + intentID + chainID to /solana/recipient/store-salt
//      keyed by the recipient's base58 pubkey. No sig required to store
//      (leaking the salt allows correlation, not theft — sweep_stealth
//      still requires the recipient's signature).
//   3. Later, recipient POSTs an ed25519-signed retrieval request to
//      /solana/recipient/get-salts. Relayer verifies the signature
//      against the requested recipient pubkey, returns the list of
//      stored {salt, intentID, chainID, stealthPDA} entries.
//   4. Recipient calls sweep_stealth(salt) on Solana to claim funds.

package relayer

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

const stealthSeed = "stealth"

// deriveSolanaSalt computes the salt for (intentID, chainID, nonce) under
// the recipient's viewSecret. HMAC-SHA256 with a 32-byte key gives a
// keyed PRF whose output is computationally indistinguishable from
// random — even an attacker who knows (intentID, chainID, nonce) cannot
// produce the salt without viewSecret.
func deriveSolanaSalt(viewSecret []byte, intentID, chainID string, nonce uint64) [32]byte {
	mac := hmac.New(sha256.New, viewSecret)
	mac.Write([]byte(intentID))
	mac.Write([]byte{0}) // separator so {intent="11", chain="2"} ≠ {intent="1", chain="12"}
	mac.Write([]byte(chainID))
	mac.Write([]byte{0})
	nonceLE := make([]byte, 8)
	binary.LittleEndian.PutUint64(nonceLE, nonce)
	mac.Write(nonceLE)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// deriveSolanaStealthPDA mirrors what the Anchor program computes via
// findProgramAddress(["stealth", user_pubkey, salt], program_id).
func deriveSolanaStealthPDA(programID, userPubkey solana.PublicKey, salt [32]byte) (solana.PublicKey, error) {
	pda, _, err := solana.FindProgramAddress(
		[][]byte{[]byte(stealthSeed), userPubkey.Bytes(), salt[:]},
		programID,
	)
	return pda, err
}

// RegisterSolanaRecipient is the one-time setup for a Solana recipient.
// Verifies an ed25519 signature over a canonical message, then generates
// a 32-byte `viewSecret` and stores it. Subsequent /generate-salt calls
// derive per-intent salts from this secret.
//
// Idempotent: re-register returns the existing account untouched.
//
// Canonical message: "REGISTER_SOLANA_STEALTH:<pubkey>:<timestamp>"
func (r *Relayer) RegisterSolanaRecipient(req *types.RegisterSolanaRecipientRequest) (*types.RegisterSolanaRecipientResponse, error) {
	// Window check (±5min replay protection).
	now := time.Now().Unix()
	if req.Timestamp < now-300 || req.Timestamp > now+300 {
		return nil, fmt.Errorf("timestamp out of window")
	}

	pk, err := solana.PublicKeyFromBase58(req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("invalid recipientPubkey: %w", err)
	}
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(req.Signature, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signature must be %d bytes", ed25519.SignatureSize)
	}
	msg := []byte(fmt.Sprintf("REGISTER_SOLANA_STEALTH:%s:%d", req.RecipientPubkey, req.Timestamp))
	if !ed25519.Verify(ed25519.PublicKey(pk.Bytes()), msg, sigBytes) {
		return nil, fmt.Errorf("invalid ed25519 signature")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	account, err := r.store.GetSolanaRecipientAccount(req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	if account != nil && account.ViewSecret != "" {
		// Already registered.
		return &types.RegisterSolanaRecipientResponse{
			Success:         true,
			RecipientPubkey: req.RecipientPubkey,
			AlreadyExists:   true,
		}, nil
	}

	// Generate fresh viewSecret (32 bytes). In a TEE deployment this would
	// be sealed; today it's plaintext in pebble, matching the EVM-side
	// trust level (ViewPrivKey is also plaintext on disk).
	var viewSecret [32]byte
	if _, err := rand.Read(viewSecret[:]); err != nil {
		return nil, fmt.Errorf("generate viewSecret: %w", err)
	}

	nowUnix := time.Now().Unix()
	if account == nil {
		account = &types.SolanaRecipientAccount{
			RecipientPubkey: req.RecipientPubkey,
			Records:         []types.SolanaStealthRecord{},
			CreatedAt:       nowUnix,
		}
	}
	account.ViewSecret = hex.EncodeToString(viewSecret[:])
	account.UpdatedAt = nowUnix

	if err := r.store.SaveSolanaRecipientAccount(req.RecipientPubkey, account); err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}

	log.WithFields(log.Fields{
		"recipient": req.RecipientPubkey,
	}).Info("Registered Solana stealth recipient (viewSecret generated)")
	return &types.RegisterSolanaRecipientResponse{
		Success:         true,
		RecipientPubkey: req.RecipientPubkey,
		AlreadyExists:   false,
	}, nil
}

// GenerateSolanaSalt returns the next deterministic salt for
// (recipient, intentID, chainID) under the recipient's viewSecret.
// Unauthenticated — anyone (typically the sender) can request a fresh
// salt for any registered recipient. The salt alone is not theft-
// enabling; sweep_stealth still requires the recipient's signature.
//
// Persists only the metadata `{intentID, chainID, nonce, stealthPDA}`
// — never the salt itself. The salt is recomputed deterministically
// at GetSolanaStealthSalts time.
func (r *Relayer) GenerateSolanaSalt(req *types.GenerateSolanaSaltRequest) (*types.GenerateSolanaSaltResponse, error) {
	pk, err := solana.PublicKeyFromBase58(req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("invalid recipientPubkey: %w", err)
	}
	programID, err := solana.PublicKeyFromBase58(req.ProgramID)
	if err != nil {
		return nil, fmt.Errorf("invalid programId: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	account, err := r.store.GetSolanaRecipientAccount(req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	if account == nil || account.ViewSecret == "" {
		return nil, fmt.Errorf("recipient %s not registered — call /solana/recipient/register first", req.RecipientPubkey)
	}
	viewSecret, err := hex.DecodeString(account.ViewSecret)
	if err != nil || len(viewSecret) != 32 {
		return nil, fmt.Errorf("corrupt viewSecret in storage")
	}

	nonce := account.Nonce
	salt := deriveSolanaSalt(viewSecret, req.IntentID, req.ChainID, nonce)
	stealthPDA, err := deriveSolanaStealthPDA(programID, pk, salt)
	if err != nil {
		return nil, fmt.Errorf("derive PDA: %w", err)
	}

	account.Nonce = nonce + 1
	account.Records = append(account.Records, types.SolanaStealthRecord{
		IntentID:   req.IntentID,
		ChainID:    req.ChainID,
		Nonce:      nonce,
		StealthPDA: stealthPDA.String(),
		CreatedAt:  time.Now().Unix(),
	})
	account.UpdatedAt = time.Now().Unix()
	if err := r.store.SaveSolanaRecipientAccount(req.RecipientPubkey, account); err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}

	log.WithFields(log.Fields{
		"recipient":  req.RecipientPubkey,
		"intentID":   req.IntentID,
		"chainID":    req.ChainID,
		"nonce":      nonce,
		"stealthPDA": stealthPDA.String(),
	}).Info("Generated Solana stealth salt")

	return &types.GenerateSolanaSaltResponse{
		Success:    true,
		Salt:       hex.EncodeToString(salt[:]),
		Nonce:      nonce,
		StealthPDA: stealthPDA.String(),
	}, nil
}

// StoreSolanaStealthSalt appends a (salt, intentID, chainID) record to
// the recipient's stealth account. Idempotent on (recipientPubkey,
// intentID) — re-storing the same intentID replaces the prior record.
func (r *Relayer) StoreSolanaStealthSalt(req *types.StoreSolanaStealthRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate the recipient pubkey is a real Solana pubkey.
	if _, err := solana.PublicKeyFromBase58(req.RecipientPubkey); err != nil {
		return fmt.Errorf("invalid recipientPubkey: %w", err)
	}
	// Validate salt is 32 bytes hex.
	saltHex := strings.TrimPrefix(req.Salt, "0x")
	saltBytes, err := hex.DecodeString(saltHex)
	if err != nil || len(saltBytes) != 32 {
		return fmt.Errorf("salt must be 32 bytes hex (got %d bytes)", len(saltBytes))
	}

	account, err := r.store.GetSolanaRecipientAccount(req.RecipientPubkey)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	now := time.Now().Unix()
	if account == nil {
		account = &types.SolanaRecipientAccount{
			RecipientPubkey: req.RecipientPubkey,
			Records:         []types.SolanaStealthRecord{},
			CreatedAt:       now,
			UpdatedAt:       now,
		}
	}

	// Idempotency key is (intentID, stealthPDA) — NOT intentID alone, since
	// multi-recipient stealth produces N PDAs per intent. Dedupe collapses
	// duplicate posts for the same (intent, PDA) pair without dropping
	// peers in the same intent.
	newRecord := types.SolanaStealthRecord{
		IntentID:   req.IntentID,
		ChainID:    req.ChainID,
		Salt:       saltHex,
		StealthPDA: req.StealthPDA,
		CreatedAt:  now,
	}
	replaced := false
	for i, rec := range account.Records {
		if rec.IntentID == req.IntentID && rec.StealthPDA == req.StealthPDA {
			account.Records[i] = newRecord
			replaced = true
			break
		}
	}
	if !replaced {
		account.Records = append(account.Records, newRecord)
	}
	account.UpdatedAt = now

	if err := r.store.SaveSolanaRecipientAccount(req.RecipientPubkey, account); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	log.WithFields(log.Fields{
		"recipient":  req.RecipientPubkey,
		"intentID":   req.IntentID,
		"chainID":    req.ChainID,
		"stealthPDA": req.StealthPDA,
		"replaced":   replaced,
	}).Info("Stored Solana stealth salt")
	return nil
}

// GetSolanaStealthSalts verifies the ed25519 sig over the canonical
// retrieval message and returns the recipient's stored records.
//
// Canonical message: "GET_SOLANA_STEALTH:<recipientPubkey>:<timestamp>"
// Timestamp must be within +/- 5 minutes of now (replay protection).
func (r *Relayer) GetSolanaStealthSalts(req *types.GetSolanaStealthSaltsRequest) (*types.GetSolanaStealthSaltsResponse, error) {
	// Validate timestamp window.
	now := time.Now().Unix()
	if req.Timestamp < now-300 || req.Timestamp > now+300 {
		return nil, fmt.Errorf("timestamp out of window (must be within 5 minutes of now)")
	}

	// Decode pubkey + signature.
	pk, err := solana.PublicKeyFromBase58(req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("invalid recipientPubkey: %w", err)
	}
	sigHex := strings.TrimPrefix(req.Signature, "0x")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signature must be %d bytes (got %d)", ed25519.SignatureSize, len(sigBytes))
	}

	// Verify ed25519(message, signature, pubkey).
	message := []byte(fmt.Sprintf("GET_SOLANA_STEALTH:%s:%d", req.RecipientPubkey, req.Timestamp))
	if !ed25519.Verify(ed25519.PublicKey(pk.Bytes()), message, sigBytes) {
		return nil, fmt.Errorf("invalid ed25519 signature")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	account, err := r.store.GetSolanaRecipientAccount(req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	if account == nil {
		return &types.GetSolanaStealthSaltsResponse{Success: true, Records: nil}, nil
	}

	// Recompute salts for records that came in via the derive-on-demand
	// path (Salt empty, Nonce set). Legacy /store-salt records keep their
	// pre-stored Salt verbatim. The response shape is identical either
	// way — caller always sees `Salt` populated.
	out := make([]types.SolanaStealthRecord, 0, len(account.Records))
	var viewSecret []byte
	if account.ViewSecret != "" {
		viewSecret, _ = hex.DecodeString(account.ViewSecret)
	}
	for _, rec := range account.Records {
		copyRec := rec
		if copyRec.Salt == "" && len(viewSecret) == 32 {
			salt := deriveSolanaSalt(viewSecret, copyRec.IntentID, copyRec.ChainID, copyRec.Nonce)
			copyRec.Salt = hex.EncodeToString(salt[:])
		}
		out = append(out, copyRec)
	}
	return &types.GetSolanaStealthSaltsResponse{
		Success: true,
		Records: out,
	}, nil
}

// BuildGetSolanaStealthMessage returns the canonical message bytes the
// recipient must sign for /solana/recipient/get-salts. Exposed for the
// FE / test code so both sides share the exact same format.
func BuildGetSolanaStealthMessage(recipientPubkey string, timestamp int64) []byte {
	return []byte(fmt.Sprintf("GET_SOLANA_STEALTH:%s:%d", recipientPubkey, timestamp))
}
