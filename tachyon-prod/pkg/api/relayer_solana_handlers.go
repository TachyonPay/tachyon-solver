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
		g.GET("/intent/:srcChainId/:srcIntentId/solved", api.SolanaIsIntentSolved)
	}
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
