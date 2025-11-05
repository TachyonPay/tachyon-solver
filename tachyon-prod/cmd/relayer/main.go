package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/api"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
	"github.com/zk-tachyon/tachyon-prod/pkg/relayer"
)

func main() {
	// Initialize logger
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.InfoLevel)

	log.Info("Starting Tachyon Relayer Service...")

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	log.WithFields(log.Fields{
		"networks": len(cfg.Networks),
		"port":     cfg.RelayerPort,
	}).Info("Configuration loaded")

	// Create relayer instance
	relayerInstance, err := relayer.NewRelayer(cfg)
	if err != nil {
		log.WithError(err).Fatal("Failed to create relayer")
	}
	defer relayerInstance.Close()

	log.Info("Relayer initialized successfully")

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	// Add CORS middleware
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// Register API routes
	relayerAPI := api.NewRelayerAPI(relayerInstance)
	relayerAPI.RegisterRoutes(router)

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in a goroutine
	go func() {
		addr := fmt.Sprintf(":%s", cfg.RelayerPort)
		log.WithField("address", addr).Info("Starting HTTP server")

		if err := router.Run(addr); err != nil {
			log.WithError(err).Fatal("Failed to start HTTP server")
		}
	}()

	// Wait for shutdown signal
	<-quit
	log.Info("Shutting down relayer...")

	log.Info("Relayer shutdown complete")
}
