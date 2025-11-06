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
	"github.com/zk-tachyon/tachyon-prod/pkg/solver"
)

func main() {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.InfoLevel)

	log.Info("Starting Tachyon Solver Service")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	log.WithFields(log.Fields{
		"chains": len(cfg.Chains),
		"port":   cfg.SolverPort,
	}).Info("Configuration loaded")

	solverInstance, err := solver.NewSolver(cfg)
	if err != nil {
		log.WithError(err).Fatal("Failed to create solver")
	}
	defer solverInstance.Close()

	log.Info("Solver initialized successfully")

	if err := solverInstance.Start(); err != nil {
		log.WithError(err).Warn("Failed to auto-start solver")
	}

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

	solverAPI := api.NewSolverAPI(solverInstance)
	solverAPI.RegisterRoutes(router)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		addr := fmt.Sprintf(":%s", cfg.SolverPort)
		log.WithField("address", addr).Info("Starting HTTP server")

		if err := router.Run(addr); err != nil {
			log.WithError(err).Fatal("Failed to start HTTP server")
		}
	}()

	<-quit
	log.Info("Shutting down solver")

	solverInstance.Stop()
	log.Info("Solver shutdown complete")
}
