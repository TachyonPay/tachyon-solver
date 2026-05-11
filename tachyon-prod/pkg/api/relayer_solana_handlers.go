package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gin-gonic/gin"
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// Solana endpoint stubs for Phase 0.
//
// These mirror the EVM stealth-address registration flow for Solana
// recipients: the user provides their Solana wallet pubkey, the relayer
// generates a 32-byte ephemeral salt and computes the stealth PDA seeds
// = [b"stealth", user_pubkey, ephemeral_salt] against a configured
// `bridge_intent` program ID. The frontend later uses this PDA as the
// recipient address when creating an EVM→Solana intent.
//
// Phase 1 will replace the in-memory salt issuance below with TEE-backed
// generation + ECIES-encrypted blob delivery, mirroring the EVM register
// flow in pkg/api/recipient_handlers.go.

// RegisterSolanaRoutes registers the Solana-specific HTTP endpoints. Call
// from cmd/relayer once the relayer's RegisterRoutes returns, only when at
// least one configured chain has ChainType=solana.
func (api *RelayerAPI) RegisterSolanaRoutes(r *gin.Engine) {
	g := r.Group("/solana")
	{
		g.POST("/recipient/register-stealth", api.SolanaRegisterStealth)
		g.POST("/recipient/derive-stealth-pda", api.SolanaDeriveStealthPDA)
		g.GET("/intent/:chainId/:intentId/solved", api.SolanaIsIntentSolved)
		// Mirror of EVM /recipient/register + /recipient/generate-stealth.
		// Recipient registers once (ed25519 sig) → relayer generates a
		// viewSecret. Sender then asks for fresh salts which are derived
		// deterministically; only the viewSecret + nonce metadata persist.
		g.POST("/recipient/register", api.SolanaRegisterRecipient)
		g.POST("/recipient/generate-salt", api.SolanaGenerateSalt)
		// Legacy path: explicit salt store + ed25519-signed retrieval.
		// Still supported for backward compat with code that generates
		// salts client-side rather than asking the relayer for them.
		g.POST("/recipient/store-salt", api.SolanaStoreStealthSalt)
		g.POST("/recipient/get-salts", api.SolanaGetStealthSalts)
	}

	// Faucet routes are independent of chain config — they only require
	// FAUCET_SOLANA_PRIVATE_KEY to be set.
	api.RegisterFaucetRoutes(r)
}

// SolanaRegisterRecipient initializes a recipient's account at the
// relayer. Verifies an ed25519 signature, then generates a viewSecret.
// Idempotent — re-register returns AlreadyExists=true.
func (api *RelayerAPI) SolanaRegisterRecipient(c *gin.Context) {
	var req types.RegisterSolanaRecipientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	resp, err := api.relayer.RegisterSolanaRecipient(&req)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// SolanaGenerateSalt returns the next deterministic salt for an intent.
// Unauthenticated (matches EVM /recipient/generate-stealth). The
// returned salt is not persisted — relayer recomputes it on demand.
func (api *RelayerAPI) SolanaGenerateSalt(c *gin.Context) {
	var req types.GenerateSolanaSaltRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	resp, err := api.relayer.GenerateSolanaSalt(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// SolanaStoreStealthSalt persists a per-intent salt for a recipient.
// The salt MAY be plaintext (current impl) or sealed-to-recipient
// (future). Either way, sweep_stealth requires the recipient's
// signature, so the salt alone never allows theft.
func (api *RelayerAPI) SolanaStoreStealthSalt(c *gin.Context) {
	var req types.StoreSolanaStealthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	if err := api.relayer.StoreSolanaStealthSalt(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SolanaGetStealthSalts returns all stored salts for a recipient. Auth'd
// via ed25519 signature over "GET_SOLANA_STEALTH:<pubkey>:<timestamp>".
func (api *RelayerAPI) SolanaGetStealthSalts(c *gin.Context) {
	var req types.GetSolanaStealthSaltsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	resp, err := api.relayer.GetSolanaStealthSalts(&req)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

type solanaRegisterStealthRequest struct {
	UserPubkey string `json:"userPubkey" binding:"required"` // base58
	ProgramID  string `json:"programId" binding:"required"`  // base58 — bridge_intent on the target Solana cluster
	Signature  string `json:"signature,omitempty"`           // base64 ed25519 sig over canonical message; future-required
}

// solanaRegisterStealthResponse is the wire payload returned to the
// frontend on successful stealth registration. The salt itself is never
// returned in the clear; the FE decrypts `Sealed` using the user's wallet
// key (ed25519 → X25519 conversion + nacl.box.Open). `EphemeralSalt` is
// kept for backward compatibility with the phase-0 stub but is left empty
// when sealing succeeds — clients should rely on `Sealed` going forward.
type solanaRegisterStealthResponse struct {
	StealthPDA    string                   `json:"stealthPda"`
	Bump          uint8                    `json:"bump"`
	IssuedAt      int64                    `json:"issuedAt"`
	Sealed        sealedSaltJSON           `json:"sealed"`
	EphemeralSalt string                   `json:"ephemeralSalt,omitempty"` // deprecated; phase-0 fallback only
}

// sealedSaltJSON mirrors stealth.SolanaSealedSalt over JSON. All three
// fields are hex-encoded for round-trip safety; clients use `tweetnacl`
// (or any libsodium binding) to open them after converting their ed25519
// secret key to X25519.
type sealedSaltJSON struct {
	EphemeralX25519Pub string `json:"ephemeralX25519Pub"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

type solanaDeriveStealthPDARequest struct {
	UserPubkey    string `json:"userPubkey" binding:"required"`
	EphemeralSalt string `json:"ephemeralSalt" binding:"required"` // hex
	ProgramID     string `json:"programId" binding:"required"`
}

type solanaDeriveStealthPDAResponse struct {
	StealthPDA string `json:"stealthPda"`
	Bump       uint8  `json:"bump"`
}

// SolanaRegisterStealth issues a fresh ephemeral salt + computes the stealth
// PDA. Returns both so the FE can encrypt the salt for the user and use the
// PDA as the recipient when creating an intent.
func (api *RelayerAPI) SolanaRegisterStealth(c *gin.Context) {
	var req solanaRegisterStealthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userPub, err := solana.PublicKeyFromBase58(strings.TrimSpace(req.UserPubkey))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userPubkey: " + err.Error()})
		return
	}
	progID, err := solana.PublicKeyFromBase58(strings.TrimSpace(req.ProgramID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid programId: " + err.Error()})
		return
	}

	var salt [32]byte
	if _, err := rand.Read(salt[:]); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "salt generation failed"})
		return
	}

	pda, bump, err := solana.FindProgramAddress(
		[][]byte{[]byte("stealth"), userPub.Bytes(), salt[:]},
		progID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PDA derive failed: " + err.Error()})
		return
	}

	// Encrypt the salt for the user via ed25519→X25519 + NaCl box. Only
	// the holder of the matching Solana wallet secret key can recover it.
	var userPubArr [32]byte
	copy(userPubArr[:], userPub.Bytes())
	sealed, err := stealth.SealSolanaSalt(userPubArr, salt[:])
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "salt sealing failed: " + err.Error()})
		return
	}
	// Wipe the plaintext salt from this goroutine's memory; the sealed
	// blob is the only artifact the response carries forward.
	for i := range salt {
		salt[i] = 0
	}

	c.JSON(http.StatusOK, solanaRegisterStealthResponse{
		StealthPDA: pda.String(),
		Bump:       bump,
		IssuedAt:   time.Now().Unix(),
		Sealed: sealedSaltJSON{
			EphemeralX25519Pub: hex.EncodeToString(sealed.EphemeralX25519Pub[:]),
			Nonce:              hex.EncodeToString(sealed.Nonce[:]),
			Ciphertext:         hex.EncodeToString(sealed.Ciphertext),
		},
	})
}

// SolanaDeriveStealthPDA is a pure-derivation helper. Useful for the FE to
// recompute a PDA from a stored (userPubkey, ephemeralSalt) pair without
// hitting Solana RPC.
func (api *RelayerAPI) SolanaDeriveStealthPDA(c *gin.Context) {
	var req solanaDeriveStealthPDARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userPub, err := solana.PublicKeyFromBase58(strings.TrimSpace(req.UserPubkey))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userPubkey: " + err.Error()})
		return
	}
	progID, err := solana.PublicKeyFromBase58(strings.TrimSpace(req.ProgramID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid programId: " + err.Error()})
		return
	}
	salt, err := hex.DecodeString(strings.TrimPrefix(req.EphemeralSalt, "0x"))
	if err != nil || len(salt) != 32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ephemeralSalt must be 32 bytes hex"})
		return
	}

	pda, bump, err := solana.FindProgramAddress(
		[][]byte{[]byte("stealth"), userPub.Bytes(), salt},
		progID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PDA derive failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, solanaDeriveStealthPDAResponse{
		StealthPDA: pda.String(),
		Bump:       bump,
	})
}

// SolanaIsIntentSolved is a thin wrapper exposing whether a (srcChainId,
// srcIntentId) pair has been solved on the destination Solana program.
//
// Phase 0 returns 501; phase 1 wires this through a SolanaClient held by
// the relayer.
func (api *RelayerAPI) SolanaIsIntentSolved(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented in phase 0 — wire pkg/blockchain.SolanaClient.IsIntentSolvedOnChain2 here",
	})
}
