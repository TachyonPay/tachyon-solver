package luckydraw

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts all lucky draw routes onto r.
//
// Public:
//
//	GET  /lucky-draw/health
//	POST /lucky-draw/enter
//	GET  /lucky-draw/status?stealth=0x...
//
// Admin (only registered when AdminAPIKey is set):
//
//	GET  /lucky-draw/admin/entries  (X-Admin-Key header required)
func (s *Service) RegisterRoutes(r *gin.Engine, adminKey string) {
	g := r.Group("/lucky-draw")
	{
		g.GET("/health", s.healthHandler)
		g.POST("/enter", s.enterHandler)
		g.GET("/status", s.statusHandler)
	}

	if adminKey != "" {
		admin := r.Group("/lucky-draw/admin")
		admin.Use(adminAuthMiddleware(adminKey))
		{
			admin.GET("/entries", s.listEntriesHandler)
		}
	}
}

func (s *Service) healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":          "ok",
		"service":         "lucky-draw",
		"supportedChains": s.SupportedChains(),
		"range":           gin.H{"minUSDC": s.cfg.MinUSDC, "maxUSDC": s.cfg.MaxUSDC},
	})
}

// enterHandler handles POST /lucky-draw/enter.
//
// Body:
//
//	{
//	  "proxyAddress": "0x...",  // proxy Safe on the destination chain
//	  "chainId":      "8453",   // decimal chain ID — the bridge destination
//	  "signature":    "0x..."   // EIP-712 LuckyDrawEntry, 65 bytes hex
//	}
func (s *Service) enterHandler(c *gin.Context) {
	var req EnterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	result, err := s.Enter(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"won":            result.Won,
		"stealthAddress": result.StealthAddress,
		"proxy":          result.ProxyAddress,
		"chainId":        result.ChainID,
		"amountUSDC":     result.AmountUSDC,
		"amountRaw":      result.AmountRaw,
		"txHash":         result.TxHash,
	})
}

// statusHandler handles GET /lucky-draw/status?stealth=0x...
//
// The stealth address is chain-agnostic — one lookup covers all chains.
// The frontend derives it from the stealth private key: crypto.PubkeyToAddress(key.PublicKey).
func (s *Service) statusHandler(c *gin.Context) {
	stealthAddr := c.Query("stealth")
	if stealthAddr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "stealth query param required (0x-prefixed Ethereum address)",
		})
		return
	}

	result, err := s.Status(stealthAddr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "status": result})
}

func (s *Service) listEntriesHandler(c *gin.Context) {
	entries, err := s.ListEntries()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"count":   len(entries),
		"entries": entries,
	})
}

func adminAuthMiddleware(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-Admin-Key") != key {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
