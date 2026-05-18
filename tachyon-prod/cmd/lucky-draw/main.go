package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/luckydraw"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})

	cfg, err := luckydraw.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load lucky draw config")
	}

	store, err := storage.NewStore(cfg.DBPath)
	if err != nil {
		log.WithError(err).Fatal("Failed to open PebbleDB")
	}
	defer store.Close()

	svc, err := luckydraw.NewService(cfg, store)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialise lucky draw service")
	}

	router := gin.Default()
	svc.RegisterRoutes(router, cfg.AdminAPIKey)

	go func() {
		addr := ":" + cfg.Port
		log.WithField("addr", addr).Info("Lucky draw service listening")
		if err := router.Run(addr); err != nil {
			log.WithError(err).Fatal("Lucky draw HTTP server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Lucky draw service shutting down")
}
