package privacy

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// PendingTTL is the age beyond which a `pending` intent is treated as
// abandoned. Drives both the periodic expiry sweeper and the on-submit
// collision-eviction path: if a colliding intent at the same (user,
// srcChain, srcToken, amount) is older than this, it gets auto-failed
// to make room for the new submit; if younger, the new submit is
// rejected with HTTP 409.
const PendingTTL = 5 * time.Minute

// API binds the HTTP handlers for the privacy router. Routes are kept
// public (no admin auth) — sessionID is the bearer of authority for
// status reads, and the encrypted POST body is its own integrity check.
type API struct {
	cfg     *Config
	store   *Store
	bridger *Bridger // nil in degraded mode (no chains configured)
}

func NewAPI(cfg *Config, store *Store, bridger *Bridger) *API {
	return &API{cfg: cfg, store: store, bridger: bridger}
}

// RegisterRoutes mounts the privacy router endpoints on r.
func (a *API) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", a.Health)
	r.GET("/pubkey", a.Pubkey)
	r.POST("/bridge", a.Submit)
	r.GET("/bridge/:id", a.Status)
	r.POST("/refund/:id", a.Refund)
}

// Health returns 200 + a small JSON status. Used by container orchestrators
// + Caddy upstreams to gate readiness.
func (a *API) Health(c *gin.Context) {
	chains := make([]string, 0, len(a.cfg.Chains))
	for id := range a.cfg.Chains {
		chains = append(chains, id)
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "privacy-router",
		"chains":  chains,
	})
}

// Pubkey returns the service's ECIES public key as uncompressed-65-byte
// hex, with 0x prefix. Clients use this to encrypt bridge intent payloads.
func (a *API) Pubkey(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"publicKey": a.cfg.ECIESPublicKeyHex(),
	})
}

// Submit accepts an ECIES-encrypted bridge intent, decrypts and validates
// it, persists with status=pending, and returns the sessionID to the
// caller. From here, the processor goroutine takes over.
func (a *API) Submit(c *gin.Context) {
	var req SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, SubmitResponse{Success: false, Error: "bad json: " + err.Error()})
		return
	}
	if req.Encrypted == "" {
		c.JSON(http.StatusBadRequest, SubmitResponse{Success: false, Error: "missing 'encrypted' field"})
		return
	}

	intent, registerStealthSig, err := DecryptAndValidate(a.cfg.ECIESPrivateKey, req.Encrypted)
	if err != nil {
		log.WithError(err).Warn("Bridge submit rejected")
		c.JSON(http.StatusBadRequest, SubmitResponse{Success: false, Error: err.Error()})
		return
	}

	// Mode 1 (relayer-managed stealth): caller supplied an EIP-712
	// RegisterStealth signature instead of a destStealthSafe. Bridge
	// the call into the relayer's existing stealth recipient registry
	// — the same one tachyon uses — so the user can later recover via
	// /recipient/derive-stealth-key + /recipient/deploy-safe with
	// just a wallet sig (no client-side keypair to persist).
	if registerStealthSig != "" {
		if err := RegisterRecipient(a.cfg, intent.UserAddress, registerStealthSig); err != nil {
			log.WithError(err).WithField("sessionID", intent.SessionID).Warn("RegisterRecipient failed")
			c.JSON(http.StatusBadGateway, SubmitResponse{Success: false, Error: "register recipient with relayer: " + err.Error()})
			return
		}
		_, safeAddr, err := GenerateStealth(a.cfg, intent.UserAddress, intent.DestChainID, intent.SessionID)
		if err != nil {
			log.WithError(err).WithField("sessionID", intent.SessionID).Warn("GenerateStealth failed")
			c.JSON(http.StatusBadGateway, SubmitResponse{Success: false, Error: "generate stealth via relayer: " + err.Error()})
			return
		}
		intent.DestStealthSafe = safeAddr
		log.WithFields(log.Fields{
			"sessionID":  intent.SessionID,
			"userAddr":   intent.UserAddress,
			"destChain":  intent.DestChainID,
			"safeAddr":   safeAddr,
		}).Info("Relayer-managed stealth resolved for privacy intent")
	}

	// Reject duplicate sessionIDs — clients must pick a fresh one each time.
	if existing, _ := a.store.Get(intent.SessionID); existing != nil {
		c.JSON(http.StatusConflict, SubmitResponse{
			Success:   false,
			SessionID: intent.SessionID,
			Error:     "sessionId already submitted",
		})
		return
	}

	// Reject if the user has another in-flight intent at the SAME
	// (sourceChain, sourceToken, amount). Single shared TEE Safe per
	// chain means the deposit watcher matches by tuple — two pending
	// intents at the same amount would race for the next deposit and
	// only one would actually bridge. If the colliding intent is
	// older than PendingTTL it's auto-expired and the new submit
	// proceeds; otherwise the FE should wait or vary the amount.
	if collision, _ := a.store.FindActiveCollision(intent.UserAddress, intent.SourceChainID, intent.SourceToken, intent.Amount); collision != nil {
		age := time.Since(collision.Created)
		if age < PendingTTL {
			c.JSON(http.StatusConflict, SubmitResponse{
				Success:   false,
				SessionID: intent.SessionID,
				Error: fmt.Sprintf("user has an in-flight intent (sessionId=%s, status=%s, age=%s) at the same (chain, token, amount); wait for it to settle or vary the amount",
					collision.SessionID, collision.Status, age.Truncate(time.Second)),
			})
			return
		}
		// Collision exists but old enough to expire. Mark it failed so
		// it stops competing for deposits, then accept the new submit.
		if err := a.store.SetError(collision.SessionID, "expired: superseded by newer intent at same (chain, token, amount)"); err != nil {
			log.WithError(err).WithField("sessionID", collision.SessionID).Warn("Failed to expire colliding intent on submit")
		} else {
			log.WithFields(log.Fields{
				"expired":   collision.SessionID,
				"new":       intent.SessionID,
				"age":       age,
				"userAddr":  collision.UserAddress,
				"src":       collision.SourceChainID + ":" + string(collision.SourceToken),
				"amount":    collision.Amount.String(),
			}).Info("Auto-expired stale colliding intent on new submit")
		}
	}

	if err := a.store.Save(intent); err != nil {
		log.WithError(err).Error("Failed to persist bridge intent")
		c.JSON(http.StatusInternalServerError, SubmitResponse{Success: false, Error: "store: " + err.Error()})
		return
	}

	log.WithFields(log.Fields{
		"sessionID":   intent.SessionID,
		"userAddress": intent.UserAddress,
		"src":         intent.SourceChainID + ":" + string(intent.SourceToken),
		"dst":         intent.DestChainID + ":" + string(intent.DestToken),
		"amount":      intent.Amount.String(),
	}).Info("Privacy bridge intent accepted")

	c.JSON(http.StatusOK, SubmitResponse{
		Success:         true,
		SessionID:       intent.SessionID,
		DestStealthSafe: intent.DestStealthSafe,
	})
}

// Status returns the lifecycle state of an intent by sessionID.
func (a *API) Status(c *gin.Context) {
	sid := c.Param("id")
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "missing sessionID"})
		return
	}
	intent, err := a.store.Get(sid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	if intent == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "not found"})
		return
	}
	c.JSON(http.StatusOK, StatusResponse{
		Success:         true,
		SessionID:       intent.SessionID,
		Status:          intent.Status,
		SourceChainID:   intent.SourceChainID,
		DestChainID:     intent.DestChainID,
		Created:         intent.Created,
		Updated:         intent.Updated,
		SweepTxHash:     intent.SweepTxHash,
		BridgeTxHash:    intent.BridgeTxHash,
		DeliveredTxHash: intent.DeliveredTxHash,
		RefundTxHash:    intent.RefundTxHash,
		Refundable:      intent.Status == StatusSwept || intent.Status == StatusFailed,
		Error:           intent.Error,
	})
}

// Refund returns the deposited funds from the TEE Safe back to the
// original user EOA when an intent gets stuck (status=swept) or
// reverts mid-bridge (status=failed). sessionID is the bearer of
// authority — the refund always targets intent.UserAddress (decrypted
// from the original encrypted payload), so a bad-actor caller with
// a leaked sessionID can only do the legitimate user a favor.
func (a *API) Refund(c *gin.Context) {
	sid := c.Param("id")
	if sid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "missing sessionID"})
		return
	}
	if a.bridger == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "privacy router in degraded mode (no chains configured)"})
		return
	}
	intent, err := a.store.Get(sid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	if intent == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "not found"})
		return
	}
	switch intent.Status {
	case StatusSwept, StatusFailed:
		// allowed
	case StatusRefunded:
		c.JSON(http.StatusOK, gin.H{
			"success":      true,
			"sessionId":    sid,
			"refundTxHash": intent.RefundTxHash,
			"message":      "already refunded",
		})
		return
	default:
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"error":   fmt.Sprintf("intent in status %q is not refundable (need swept or failed)", intent.Status),
		})
		return
	}

	prev := intent.Status
	txHash, err := a.bridger.Refund(c.Request.Context(), intent)
	if err != nil {
		log.WithError(err).WithField("sessionID", sid).Error("Refund failed")
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	if err := a.store.SetTxHash(sid, "refund", txHash); err != nil {
		log.WithError(err).WithField("sessionID", sid).Warn("SetTxHash refund persisted failed")
	}
	if err := a.store.ChangeStatus(sid, prev, StatusRefunded); err != nil {
		log.WithError(err).WithField("sessionID", sid).Warn("ChangeStatus to refunded failed")
	}
	log.WithFields(log.Fields{
		"sessionID": sid,
		"txHash":    txHash,
		"recipient": intent.UserAddress,
		"chain":     intent.SourceChainID,
	}).Info("Privacy bridge: refund delivered")
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"sessionId":    sid,
		"refundTxHash": txHash,
		"recipient":    intent.UserAddress,
		"sourceChain":  intent.SourceChainID,
		"amount":       intent.Amount.String(),
	})
}
