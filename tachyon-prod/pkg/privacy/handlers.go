package privacy

import (
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// API binds the HTTP handlers for the privacy router. Routes are kept
// public (no admin auth) — sessionID is the bearer of authority for
// status reads, and the encrypted POST body is its own integrity check.
type API struct {
	cfg   *Config
	store *Store
}

func NewAPI(cfg *Config, store *Store) *API {
	return &API{cfg: cfg, store: store}
}

// RegisterRoutes mounts the privacy router endpoints on r.
func (a *API) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", a.Health)
	r.GET("/pubkey", a.Pubkey)
	r.POST("/bridge", a.Submit)
	r.GET("/bridge/:id", a.Status)
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

	intent, err := DecryptAndValidate(a.cfg.ECIESPrivateKey, req.Encrypted)
	if err != nil {
		log.WithError(err).Warn("Bridge submit rejected")
		c.JSON(http.StatusBadRequest, SubmitResponse{Success: false, Error: err.Error()})
		return
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

	c.JSON(http.StatusOK, SubmitResponse{Success: true, SessionID: intent.SessionID})
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
		Error:           intent.Error,
	})
}
