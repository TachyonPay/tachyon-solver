package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zk-tachyon/tachyon-prod/pkg/rebalancer"
)

// RebalancerAPI exposes the rebalancer over HTTP. All endpoints are admin-only.
type RebalancerAPI struct {
	rb *rebalancer.Rebalancer
}

func NewRebalancerAPI(rb *rebalancer.Rebalancer) *RebalancerAPI {
	return &RebalancerAPI{rb: rb}
}

// RegisterAdminRoutes mounts /rebalance/* under an already-authed group.
func (api *RebalancerAPI) RegisterAdminRoutes(r *gin.RouterGroup) {
	g := r.Group("/rebalance")
	g.GET("/snapshot", api.Snapshot)
	g.GET("/dry-run", api.DryRun)
	g.POST("/execute", api.Execute)
}

// Snapshot returns the current balances and computed deficits/surpluses without
// building a plan. Useful for monitoring.
func (api *RebalancerAPI) Snapshot(c *gin.Context) {
	if api.rb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rebalancer not configured"})
		return
	}
	snap, plan, err := api.rb.DryRun(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":  true,
		"buckets":  bucketsResponse(snap, api.rb.Config()),
		"deficits": len(plan.Actions),
		"notes":    plan.Notes,
	})
}

// DryRun snapshots and plans without executing any tx.
func (api *RebalancerAPI) DryRun(c *gin.Context) {
	if api.rb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rebalancer not configured"})
		return
	}
	snap, plan, err := api.rb.DryRun(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"buckets": bucketsResponse(snap, api.rb.Config()),
		"actions": actionsResponse(plan.Actions),
		"notes":   plan.Notes,
	})
}

// Execute snapshots, plans, and dispatches each action through the wired executor.
// Returns 503 until a live executor is wired (Phase 1B).
func (api *RebalancerAPI) Execute(c *gin.Context) {
	if api.rb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rebalancer not configured"})
		return
	}
	snap, plan, txs, err := api.rb.Execute(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":     err.Error(),
			"completed": txs,
			"buckets":   bucketsResponse(snap, api.rb.Config()),
			"actions":   actionsResponse(planActions(plan)),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"buckets": bucketsResponse(snap, api.rb.Config()),
		"actions": actionsResponse(plan.Actions),
		"txs":     txs,
		"notes":   plan.Notes,
	})
}

// helpers ---

func bucketsResponse(snap *rebalancer.Snapshot, cfg *rebalancer.Config) []gin.H {
	if snap == nil {
		return nil
	}
	out := make([]gin.H, 0, len(snap.Balances))
	for _, b := range snap.Buckets(cfg) {
		out = append(out, gin.H{
			"key":       b.Key.String(),
			"balance":   b.Balance.String(),
			"target":    b.Target.String(),
			"threshold": b.Threshold.String(),
			"floor":     b.Floor.String(),
			"deficit":   b.Deficit.String(),
			"surplus":   b.Surplus.String(),
		})
	}
	return out
}

func actionsResponse(actions []rebalancer.Action) []gin.H {
	out := make([]gin.H, 0, len(actions))
	for _, a := range actions {
		out = append(out, gin.H{
			"kind":     string(a.Kind),
			"src":     a.SrcChainID,
			"dst":     a.DstChainID,
			"srcToken": string(a.SrcToken),
			"dstToken": string(a.DstToken),
			"amount":   a.Amount.String(),
			"reason":   a.Reason,
		})
	}
	return out
}

func planActions(p *rebalancer.Plan) []rebalancer.Action {
	if p == nil {
		return nil
	}
	return p.Actions
}
