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
	"github.com/zk-tachyon/tachyon-prod/pkg/rebalancer"
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

	// Rate limiting middleware
	rateLimiter := middleware.NewRateLimiter(100, time.Minute) // 100 requests per minute per IP
	router.Use(middleware.RateLimitMiddleware(rateLimiter))

	// CORS middleware
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Signature, X-Timestamp")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	solverAPI := api.NewSolverAPI(solverInstance)

	// Register public routes
	solverAPI.RegisterRoutes(router)

	// Register admin routes with API key auth
	adminGroup := router.Group("/admin")
	adminGroup.Use(middleware.APIKeyAuth())
	solverAPI.RegisterAdminRoutes(adminGroup)

	// Optional: rebalancer admin endpoints. Stays disabled unless REBALANCER_ENABLED=true.
	rbCfg, err := rebalancer.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load rebalancer config")
	}
	if rbCfg.Enabled {
		rb := rebalancer.New(rbCfg, solverInstance.Clients())

		// Wire the executor + quoter. Two modes, in priority order:
		//
		//   1. REBALANCER_USE_SAFE=true → SafeSigner: production path. Uses
		//      the solver's already-configured Safe (CHAIN_*_SAFE_ADDRESS +
		//      SAFE_SIGNER_KEY_1/2/3) for signing; PRIVATE_KEY's EOA pays gas.
		//   2. REBALANCER_TEST_PRIVATE_KEY set → EOASigner: bootstrap path,
		//      single key signs and pays gas, no Safe.
		//   3. Neither → dry-run only; /admin/rebalance/execute returns 503.
		switch {
		case rebalancer.UseSafe():
			safeSigner, err := rebalancer.NewSafeSignerFromClients(solverInstance.Clients())
			if err != nil {
				log.WithError(err).Fatal("Failed to wire Safe signer for rebalancer")
			}
			if safeSigner == nil {
				log.Fatal("REBALANCER_USE_SAFE=true but no chain clients available")
			}
			rb.SetExecutor(rebalancer.NewSafeExecutor(safeSigner, rbCfg))
			rb.SetQuoter(safeSigner)
			log.WithField("safe", safeSigner.Address().Hex()).
				Warn("Rebalancer SAFE executor LIVE — funds and signing operations route through the Safe")

		case os.Getenv("REBALANCER_TEST_PRIVATE_KEY") != "":
			eoaSigner, err := rebalancer.NewEOASignerFromEnv(solverInstance.Clients())
			if err != nil {
				log.WithError(err).Fatal("Failed to load REBALANCER_TEST_PRIVATE_KEY")
			}
			if eoaSigner != nil {
				rb.SetExecutor(rebalancer.NewEOAExecutor(eoaSigner, rbCfg))
				rb.SetQuoter(eoaSigner)
				log.WithField("address", eoaSigner.Address().Hex()).
					Warn("Rebalancer EOA executor LIVE — /admin/rebalance/execute will sign with REBALANCER_TEST_PRIVATE_KEY")
			}

		default:
			log.Info("Rebalancer in dry-run-only mode (set REBALANCER_USE_SAFE=true OR REBALANCER_TEST_PRIVATE_KEY to enable execute)")
		}

		api.NewRebalancerAPI(rb).RegisterAdminRoutes(adminGroup)
		log.WithField("targets", len(rbCfg.Targets)).Info("Rebalancer enabled — admin endpoints at /admin/rebalance/*")
	} else {
		log.Info("Rebalancer disabled (set REBALANCER_ENABLED=true to enable)")
	}

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
