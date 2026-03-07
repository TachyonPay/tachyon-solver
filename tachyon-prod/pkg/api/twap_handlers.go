package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zk-tachyon/tachyon-prod/pkg/twap"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// TWAPAPI wraps TWAP engine for HTTP handlers
type TWAPAPI struct {
	engine *twap.Engine
}

// NewTWAPAPI creates a new TWAP API handler
func NewTWAPAPI(engine *twap.Engine) *TWAPAPI {
	return &TWAPAPI{engine: engine}
}

// RegisterTWAPRoutes registers TWAP-specific routes on the gin router
func (t *TWAPAPI) RegisterTWAPRoutes(router *gin.Engine) {
	router.GET("/twap/health", t.healthCheck)
	router.GET("/twap/ecies-pubkey", t.getECIESPubKey)
	router.GET("/twap/address", t.getTEEAddress)
	router.POST("/twap/create", t.createOrder)
	router.GET("/twap/status/:orderId", t.getOrderStatus)
	router.POST("/twap/cancel/:orderId", t.cancelOrder)
	router.GET("/twap/orders/:userAddress", t.listOrders)
}

func (t *TWAPAPI) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "tachyon-twap",
	})
}

func (t *TWAPAPI) getECIESPubKey(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"publicKey": t.engine.GetECIESPublicKey(),
	})
}

func (t *TWAPAPI) getTEEAddress(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"address": t.engine.GetAddress(),
		"message": "Approve this address for transferFrom on your source token",
	})
}

func (t *TWAPAPI) createOrder(c *gin.Context) {
	var req types.CreateTWAPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.TWAPOrderResponse{
			Success: false,
			Error:   "invalid request: " + err.Error(),
		})
		return
	}

	order, err := t.engine.DecryptAndCreateOrder(req.EncryptedData)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.TWAPOrderResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, types.TWAPOrderResponse{
		Success: true,
		OrderID: order.ID,
		Status:  order.Status,
	})
}

func (t *TWAPAPI) getOrderStatus(c *gin.Context) {
	orderID := c.Param("orderId")

	order, ok := t.engine.GetOrder(orderID)
	if !ok {
		c.JSON(http.StatusNotFound, types.TWAPStatusResponse{
			Success: false,
			Error:   "order not found",
		})
		return
	}

	c.JSON(http.StatusOK, types.TWAPStatusResponse{
		Success:        true,
		OrderID:        order.ID,
		Status:         order.Status,
		TranchesFilled: order.TranchesFilled,
		TotalTranches:  order.TotalTranches,
		AmountSpent:    order.AmountSpent,
	})
}

func (t *TWAPAPI) cancelOrder(c *gin.Context) {
	orderID := c.Param("orderId")

	if err := t.engine.CancelOrder(orderID); err != nil {
		c.JSON(http.StatusBadRequest, types.TWAPOrderResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, types.TWAPOrderResponse{
		Success: true,
		OrderID: orderID,
		Status:  "cancelled",
	})
}

func (t *TWAPAPI) listOrders(c *gin.Context) {
	userAddress := c.Param("userAddress")

	orders := t.engine.ListOrders(userAddress)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"orders":  orders,
	})
}
