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
	"github.com/zk-tachyon/tachyon-prod/pkg/twap"
)

func main() {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.InfoLevel)

	log.Info("Starting Tachyon TWAP Service")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	engine, err := twap.NewEngine(cfg)
	if err != nil {
		log.WithError(err).Fatal("Failed to create TWAP engine")
	}
	defer engine.Close()

	log.WithField("address", engine.GetAddress()).Info("TWAP TEE wallet address")
	log.Info("TWAP engine initialized successfully")

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

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

	twapAPI := api.NewTWAPAPI(engine)
	twapAPI.RegisterTWAPRoutes(router)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	port := os.Getenv("TWAP_PORT")
	if port == "" {
		port = "3002"
	}

	go func() {
		addr := fmt.Sprintf(":%s", port)
		log.WithField("address", addr).Info("Starting TWAP HTTP server")

		if err := router.Run(addr); err != nil {
			log.WithError(err).Fatal("Failed to start HTTP server")
		}
	}()

	<-quit
	log.Info("Shutting down TWAP service")
}
