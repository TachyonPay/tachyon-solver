package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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

// RegisterRoutes registers all solver API routes
func (api *SolverAPI) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", api.HealthCheck)
	r.GET("/status", api.GetStatus)
	r.GET("/active-intents", api.GetActiveIntents)
	r.GET("/intent-status/:intentId", api.GetIntentStatus)
	r.GET("/intent-status/:intentId/:network", api.GetIntentStatusByNetwork)
	r.POST("/start", api.StartSolver)
	r.POST("/stop", api.StopSolver)
}

// HealthCheck returns the health status of the solver
func (api *SolverAPI) HealthCheck(c *gin.Context) {
	activeIntents := api.solver.GetActiveIntents()

	c.JSON(http.StatusOK, types.HealthResponse{
		Status:        "healthy",
		Service:       "bridge-solver",
		Timestamp:     time.Now().Format(time.RFC3339),
		ActiveIntents: len(activeIntents),
	})
}

// GetStatus returns detailed solver status
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

// GetActiveIntents returns all active intents
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

	// Try to find the intent in any network
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

// GetIntentStatusByNetwork returns the status of a specific intent on a specific network
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

// StartSolver starts the solver
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

// StopSolver stops the solver
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
