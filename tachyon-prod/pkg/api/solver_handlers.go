package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zk-tachyon/tachyon-prod/pkg/middleware"
	"github.com/zk-tachyon/tachyon-prod/pkg/solver"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// SolverAPI handles HTTP requests for the solver service
type SolverAPI struct {
	solver *solver.Solver
}

// NewSolverAPI creates a new solver API handler
func NewSolverAPI(s *solver.Solver) *SolverAPI {
	return &SolverAPI{solver: s}
}

// RegisterRoutes registers public routes
func (api *SolverAPI) RegisterRoutes(r *gin.Engine) {
	// Public read endpoints
	r.GET("/health", api.HealthCheck)
	r.GET("/status", api.GetStatus)
	r.GET("/active-intents", api.GetActiveIntents)
	r.GET("/intent-status/:intentId", api.GetIntentStatus)
	r.GET("/intent-status/:intentId/:network", api.GetIntentStatusByNetwork)

	// User transaction history — wallet signature required
	r.GET("/transactions", middleware.WalletSignatureAuth(), api.GetTransactions)
}

// RegisterAdminRoutes registers admin-only routes (call with auth middleware group)
func (api *SolverAPI) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("/start", api.StartSolver)
	r.POST("/stop", api.StopSolver)
}

func (api *SolverAPI) HealthCheck(c *gin.Context) {
	activeIntents := api.solver.GetActiveIntents()

	c.JSON(http.StatusOK, types.HealthResponse{
		Status:        "healthy",
		Service:       "bridge-solver",
		Timestamp:     time.Now().Format(time.RFC3339),
		ActiveIntents: len(activeIntents),
	})
}

func (api *SolverAPI) GetStatus(c *gin.Context) {
	activeIntents := api.solver.GetActiveIntents()
	balances := api.solver.GetBalances(c.Request.Context())

	response := gin.H{
		"isRunning":     api.solver.IsRunning(),
		"activeIntents": activeIntents,
		"walletAddress": api.solver.GetWalletAddress(),
		"balances":      balances,
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	c.JSON(http.StatusOK, response)
}

func (api *SolverAPI) GetActiveIntents(c *gin.Context) {
	activeIntents := api.solver.GetActiveIntents()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"count":   len(activeIntents),
		"intents": activeIntents,
	})
}

// GetIntentStatus returns the status of a specific intent
func (api *SolverAPI) GetIntentStatus(c *gin.Context) {
	intentID := c.Param("intentId")

	activeIntents := api.solver.GetActiveIntents()
	for _, status := range activeIntents {
		if status.IntentID == intentID {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"status":  status,
			})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{
		"success": false,
		"error":   "Intent not found",
	})
}

func (api *SolverAPI) GetIntentStatusByNetwork(c *gin.Context) {
	intentID := c.Param("intentId")
	network := c.Param("network")

	status, err := api.solver.GetIntentStatus(intentID, network)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"status":  status,
	})
}

func (api *SolverAPI) StartSolver(c *gin.Context) {
	if api.solver.IsRunning() {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Solver is already running",
		})
		return
	}

	err := api.solver.Start()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Solver started successfully",
	})
}

func (api *SolverAPI) StopSolver(c *gin.Context) {
	if !api.solver.IsRunning() {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Solver is not running",
		})
		return
	}

	api.solver.Stop()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Solver stopped successfully",
	})
}

// GetTransactions returns paginated transaction history for the authenticated user.
// User address is derived from the verified wallet signature — no ?user= param needed.
// Query params: page (default 1), limit (default 20, max 100), status ("pending"|"completed"|"")
func (api *SolverAPI) GetTransactions(c *gin.Context) {
	user := c.GetString("verified_address")
	status := c.Query("status")

	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	records, total, err := api.solver.GetTransactionsByUser(user, status, page, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.TransactionPageResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	totalPages := 1
	if total > 0 {
		totalPages = (total + limit - 1) / limit
	}

	c.JSON(http.StatusOK, types.TransactionPageResponse{
		Success:      true,
		Transactions: records,
		Page:         page,
		Limit:        limit,
		Total:        total,
		TotalPages:   totalPages,
	})
}
