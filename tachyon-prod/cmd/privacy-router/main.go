// privacy-router is the partial-privacy bridging service.
//
// Lifecycle of a single bridge:
//
//   1. Client encrypts a BridgeIntentPayload with the service's ECIES
//      public key (from /pubkey) and POSTs the hex blob to /bridge.
//   2. Service decrypts, validates the route against addresses.go, and
//      persists the BridgeIntent with status=pending.
//   3. The client funds the source side by sending the source token
//      directly to the chain's TEE hot Safe (v0 — no per-session
//      inbound stealth Safe yet).
//   4. Per-chain deposit watcher matches the Transfer event to the
//      pending intent by (chain, token, amount, from) and transitions
//      it to status=swept.
//   5. Dispatch loop groups swept intents by (src,dst,srcTok,dstTok)
//      and fires when count ≥ 3 OR the oldest waited > 2 min.
//   6. Each dispatched intent gets a random 10-120s jitter, then a
//      Stargate send from the TEE Safe to the user's destination
//      stealth Safe. Status → bridging.
//   7. A delivery poller watches the dest balance; on confirmation,
//      status → delivered.
//
// Status reads via GET /bridge/:id are public (sessionID is the bearer).
// Operator hands TEE Safes off to Nitro Enclaves before this carries
// real volume — the .env-on-disk private keys are a single-host
// honeypot.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/middleware"
	"github.com/zk-tachyon/tachyon-prod/pkg/privacy"
)

func main() {
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	log.SetLevel(log.InfoLevel)
	log.Info("Starting Tachyon privacy-router")

	// Load .env if present (matches relayer/solver behavior). Inside the
	// docker container the env is injected via env_file directive, so the
	// .env file is absent and godotenv.Load returns an error we ignore.
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../../.env")
	_ = godotenv.Load("../../../.env")

	cfg, err := privacy.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load privacy config")
	}
	log.WithFields(log.Fields{
		"chains":     len(cfg.Chains),
		"port":       cfg.Port,
		"relayerURL": cfg.RelayerURL,
	}).Info("Privacy config loaded")

	store, err := privacy.NewStore(cfg.DBPath)
	if err != nil {
		log.WithError(err).Fatal("Failed to open privacy store")
	}
	defer store.Close()

	rootCtx, cancelRoot := context.WithCancel(context.Background())

	// Bridger + processor only come online once at least one chain has
	// both an RPC and a TEE Safe address. In degraded mode (no Safes
	// deployed yet) the HTTP server still answers /pubkey + /health so
	// clients can encrypt and submit; intents land in pending and stay
	// there until the operator populates TEE_SAFE_* and restarts.
	var processor *privacy.Processor
	if len(cfg.Chains) > 0 {
		bridger, err := privacy.NewBridger(cfg)
		if err != nil {
			log.WithError(err).Fatal("Failed to init Bridger")
		}
		processor, err = privacy.NewProcessor(cfg, store, bridger)
		if err != nil {
			log.WithError(err).Fatal("Failed to init Processor")
		}
		processor.Start(rootCtx)
		log.WithField("chains", len(cfg.Chains)).Info("Privacy processor LIVE")
	} else {
		log.Warn("No TEE Safes configured (TEE_SAFE_* empty) — running in degraded mode: HTTP only, no on-chain processing")
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	rateLimiter := middleware.NewRateLimiter(120, time.Minute)
	router.Use(middleware.RateLimitMiddleware(rateLimiter))
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Signature, X-Timestamp")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	api := privacy.NewAPI(cfg, store)
	api.RegisterRoutes(router)

	// Announce ourselves to the relayer twice (once per bridge type) so
	// frontends can discover routes per kind via GET /solvers?type=...
	// We do this in a goroutine because the relayer may not yet be up
	// when this container boots; failure is non-fatal and we just log.
	go func() {
		if err := privacy.RegisterWithRelayer(cfg); err != nil {
			log.WithError(err).Warn("Privacy router relayer registration failed (will retry on next boot)")
		}
	}()

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: router}
	go func() {
		log.WithField("address", srv.Addr).Info("HTTP server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("HTTP server crashed")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down privacy-router")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.WithError(err).Warn("HTTP server shutdown error")
	}
	cancelRoot()
	if processor != nil {
		processor.Stop()
	}
	log.Info("Privacy-router shutdown complete")
	fmt.Println("done")
}
