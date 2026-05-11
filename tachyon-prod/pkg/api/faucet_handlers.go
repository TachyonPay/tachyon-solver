package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterFaucetRoutes registers the SPL token faucet endpoints under
// /solana/faucet/*. It is a no-op when the relayer has no faucet configured.
func (api *RelayerAPI) RegisterFaucetRoutes(r *gin.Engine) {
	if !api.relayer.HasFaucet() {
		return
	}
	g := r.Group("/solana/faucet")
	{
		g.POST("/claim", api.FaucetClaim)
		g.GET("/status", api.FaucetStatus)
	}
}

type faucetClaimRequest struct {
	Wallet string `json:"wallet" binding:"required"`
}

// FaucetClaim mints 100 tokens to the given Solana wallet address.
// Rate limited to once per configured cooldown period per wallet.
//
// POST /solana/faucet/claim
// Body: { "wallet": "<base58 pubkey>" }
func (api *RelayerAPI) FaucetClaim(c *gin.Context) {
	var req faucetClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	result, err := api.relayer.FaucetClaim(c.Request.Context(), req.Wallet)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "rate limited") {
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"signature": result.Signature,
		"wallet":    result.Wallet,
		"amountRaw": result.AmountRaw,
		"tokenMint": result.TokenMint,
	})
}

// FaucetStatus reports whether a wallet can currently claim and when the
// cooldown expires.
//
// GET /solana/faucet/status?wallet=<base58 pubkey>
func (api *RelayerAPI) FaucetStatus(c *gin.Context) {
	wallet := c.Query("wallet")
	if wallet == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "wallet query parameter is required",
		})
		return
	}

	result, err := api.relayer.FaucetStatus(wallet)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "faucet": result})
}
