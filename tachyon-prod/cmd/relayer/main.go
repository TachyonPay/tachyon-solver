package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/api"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/middleware"
	"github.com/zk-tachyon/tachyon-prod/pkg/relayer"
)

func main() {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.InfoLevel)

	log.Info("Starting Tachyon Relayer Service")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	log.WithFields(log.Fields{
		"chains": len(cfg.Chains),
		"port":   cfg.RelayerPort,
	}).Info("Configuration loaded")

	relayerInstance, err := relayer.NewRelayer(cfg)
	if err != nil {
		log.WithError(err).Fatal("Failed to create relayer")
	}
	defer relayerInstance.Close()

	log.Info("Relayer initialized successfully")

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	// Rate limiting middleware
	rateLimiter := middleware.NewRateLimiter(100, time.Minute) // 100 requests per minute per IP
	router.Use(middleware.RateLimitMiddleware(rateLimiter))

	// CORS middleware
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Solver-Address, X-Signature, X-Timestamp")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	relayerAPI := api.NewRelayerAPI(relayerInstance)
	
	// Register public routes
	relayerAPI.RegisterRoutes(router)

	// Register admin routes with API key auth
	adminGroup := router.Group("/admin")
	adminGroup.Use(middleware.APIKeyAuth())
	relayerAPI.RegisterAdminRoutes(adminGroup)

	// Check if admin API key is configured
	if os.Getenv("ADMIN_API_KEY") == "" {
		log.Warn("⚠️  ADMIN_API_KEY not set - admin endpoints will be unavailable")
		log.Warn("   Set ADMIN_API_KEY environment variable to enable admin access")
	} else {
		log.Info("Admin endpoints enabled at /admin/*")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		addr := fmt.Sprintf(":%s", cfg.RelayerPort)
		log.WithField("address", addr).Info("Starting HTTP server")

		if err := router.Run(addr); err != nil {
			log.WithError(err).Fatal("Failed to start HTTP server")
		}
	}()

	<-quit
	log.Info("Shutting down relayer")

	log.Info("Relayer shutdown complete")
}
