package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zk-tachyon/tachyon-prod/pkg/relayer"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// RelayerAPI handles HTTP requests for the relayer service
type RelayerAPI struct {
	relayer *relayer.Relayer
}

// NewRelayerAPI creates a new relayer API handler
func NewRelayerAPI(r *relayer.Relayer) *RelayerAPI {
	return &RelayerAPI{relayer: r}
}

// RegisterRoutes registers public routes
func (api *RelayerAPI) RegisterRoutes(r *gin.Engine) {
	// Public read endpoints
	r.GET("/health", api.HealthCheck)
	r.GET("/ecies-pubkey", api.GetECIESPublicKey)
	r.GET("/get-recipients/:intentId", api.GetRecipients)
	r.GET("/intent-details/:intentId", api.GetIntentDetails)
	r.POST("/verify", api.VerifyIntent)
	r.POST("/get-routes", api.GetRoutes)
	r.GET("/get-routes", api.GetRoutesQuery)

	// Endpoints that require solver/user interaction (not admin-protected)
	r.POST("/store-recipients", api.StoreRecipients)
	r.POST("/settle", api.SettleIntent)
	r.POST("/register-solver", api.RegisterSolver)

	// Stealth address / recipient endpoints
	r.POST("/recipient/register", api.RegisterRecipient)
	r.GET("/recipient/addresses", api.GetStealthAddresses)
	r.POST("/recipient/generate-stealth", api.GenerateStealthAddress)
	r.POST("/recipient/derive-stealth-key", api.DeriveStealthKey)
	r.POST("/recipient/deploy-safe", api.DeploySafe)
	r.POST("/recipient/relay-proxy", api.RelayProxy)
}

// RegisterAdminRoutes registers admin-only routes (call with auth middleware group)
func (api *RelayerAPI) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/list-stored-intents", api.ListStoredIntents)
	r.DELETE("/delete-recipients/:intentId", api.DeleteRecipients)
}

func (api *RelayerAPI) HealthCheck(c *gin.Context) {
	networks := api.relayer.GetNetworks()
	chainInfo := api.relayer.GetChainInfo()

	c.JSON(http.StatusOK, types.HealthResponse{
		Status:    "healthy",
		Service:   "bridge-relayer",
		Timestamp: time.Now().Format(time.RFC3339),
		Networks:  networks,
		ChainInfo: chainInfo,
	})
}

func (api *RelayerAPI) GetECIESPublicKey(c *gin.Context) {
	publicKey := api.relayer.GetECIESPublicKey()

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"publicKey": publicKey,
		"message":   "Use this public key to encrypt recipient information",
	})
}

func (api *RelayerAPI) StoreRecipients(c *gin.Context) {
	var req types.StoreRecipientsRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	err := api.relayer.StoreRecipients(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to store recipients",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"intentId": req.IntentID,
		"message":  "Recipients stored successfully",
	})
}

// GetRecipients retrieves recipient information for the winning solver only
func (api *RelayerAPI) GetRecipients(c *gin.Context) {
	intentID := c.Param("intentId")
	sourceChainID := c.Query("sourceChainId") // Optional: for disambiguation when same intent ID exists on multiple chains

	requesterAddr := c.GetHeader("X-Solver-Address")
	if requesterAddr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "X-Solver-Address header required",
		})
		return
	}

	info, err := api.relayer.GetRecipientsForWinner(c.Request.Context(), intentID, sourceChainID, requesterAddr)
	if err != nil {
		errMsg := err.Error()
		statusCode := http.StatusForbidden
		if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "not deposited") {
			statusCode = http.StatusNotFound
		}
		c.JSON(statusCode, gin.H{
			"success": false,
			"error":   errMsg,
		})
		return
	}

	c.JSON(http.StatusOK, info)
}

func (api *RelayerAPI) ListStoredIntents(c *gin.Context) {
	intents := api.relayer.ListStoredIntents()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"count":   len(intents),
		"intents": intents,
	})
}

func (api *RelayerAPI) DeleteRecipients(c *gin.Context) {
	intentID := c.Param("intentId")

	err := api.relayer.DeleteRecipients(intentID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Recipients deleted successfully",
	})
}

func (api *RelayerAPI) VerifyIntent(c *gin.Context) {
	var req types.VerifyRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	solved, err := api.relayer.VerifyIntentSolved(c.Request.Context(), req.Network, req.SourceChainID, req.IntentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"intentId": req.IntentID,
		"network":  req.Network,
		"solved":   solved,
	})
}

func (api *RelayerAPI) SettleIntent(c *gin.Context) {
	var req types.SettleRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	response, err := api.relayer.SettleIntent(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if !response.Success {
		c.JSON(http.StatusBadRequest, response)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (api *RelayerAPI) RegisterSolver(c *gin.Context) {
	var req types.RegisterSolverRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	response, err := api.relayer.RegisterSolver(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, response)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (api *RelayerAPI) GetRoutes(c *gin.Context) {
	var filter types.GetRoutesRequest

	if err := c.ShouldBindJSON(&filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	response := api.relayer.GetRoutes(&filter)
	c.JSON(http.StatusOK, response)
}

func (api *RelayerAPI) GetRoutesQuery(c *gin.Context) {
	sourceChain := c.Query("sourceChain")
	if sourceChain == "" {
		sourceChain = c.Query("origin")
	}

	originAsset := c.Query("originAsset")
	if originAsset == "" {
		originAsset = c.Query("tokenA")
	}

	filter := types.GetRoutesRequest{
		SourceChain: sourceChain,
		DestChain:   c.Query("destChain"),
		TokenA:      originAsset,
		TokenB:      c.Query("tokenB"),
	}

	response := api.relayer.GetRoutes(&filter)
	c.JSON(http.StatusOK, response)
}

// RegisterRecipient handles recipient registration and key generation
func (api *RelayerAPI) RegisterRecipient(c *gin.Context) {
	var req types.RegisterRecipientRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.RegisterRecipientResponse{
			Success: false,
			Error:   "Invalid request body: " + err.Error(),
		})
		return
	}

	response, err := api.relayer.RegisterRecipient(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.RegisterRecipientResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetStealthAddresses returns all stealth addresses for a recipient
func (api *RelayerAPI) GetStealthAddresses(c *gin.Context) {
	address := c.Query("address")
	signature := c.Query("signature")

	if address == "" || signature == "" {
		c.JSON(http.StatusBadRequest, types.GetStealthAddressesResponse{
			Success: false,
			Error:   "address and signature query parameters required",
		})
		return
	}

	response, err := api.relayer.GetStealthAddresses(address, signature)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.GetStealthAddressesResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GenerateStealthAddress generates a new stealth address for a recipient (internal/TEE use)
func (api *RelayerAPI) GenerateStealthAddress(c *gin.Context) {
	var req types.GenerateStealthRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request body: " + err.Error(),
		})
		return
	}

	result, err := api.relayer.GenerateStealthAddress(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"stealthAddress": result.StealthAddress,
		"safeAddress":    result.SafeAddress,
	})
}

// DeriveStealthKey derives the stealth private key for a recipient
func (api *RelayerAPI) DeriveStealthKey(c *gin.Context) {
	var req types.DeriveStealthKeyRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.DeriveStealthKeyResponse{
			Success: false,
			Error:   "Invalid request body: " + err.Error(),
		})
		return
	}

	result, err := api.relayer.DeriveStealthKey(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.DeriveStealthKeyResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// DeploySafe deploys a Safe on a stealth address
func (api *RelayerAPI) DeploySafe(c *gin.Context) {
	var req types.DeploySafeRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.DeploySafeResponse{
			Success: false,
			Error:   "Invalid request body: " + err.Error(),
		})
		return
	}

	response, err := api.relayer.DeploySafe(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.DeploySafeResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// RelayProxy broadcasts a pre-signed Safe transaction. The caller signs the Safe tx hash
// with their stealth private key; the relayer pays gas and calls execTransaction.
func (api *RelayerAPI) RelayProxy(c *gin.Context) {
	var req types.RelayProxyRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.RelayProxyResponse{
			Success: false,
			Error:   "Invalid request body: " + err.Error(),
		})
		return
	}

	response, err := api.relayer.RelayProxy(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.RelayProxyResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetIntentDetails retrieves complete intent details including all transaction hashes
func (api *RelayerAPI) GetIntentDetails(c *gin.Context) {
	intentID := c.Param("intentId")
	sourceChainID := c.Query("sourceChainId")

	if sourceChainID == "" {
		c.JSON(http.StatusBadRequest, types.GetIntentDetailsResponse{
			Success: false,
			Error:   "sourceChainId query parameter is required",
		})
		return
	}

	details, err := api.relayer.GetIntentDetails(sourceChainID, intentID)
	if err != nil {
		c.JSON(http.StatusNotFound, types.GetIntentDetailsResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, types.GetIntentDetailsResponse{
		Success: true,
		Intent:  details,
	})
}
