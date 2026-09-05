package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liquor-store/security-api/internal/config"
	"github.com/liquor-store/security-api/internal/notifications"
	"github.com/liquor-store/security-api/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("invalid database URL", "error", err)
		os.Exit(1)
	}
	poolConfig.MaxConns = 10
	db, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	pingContext, cancelPing := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelPing()
	if err := db.Ping(pingContext); err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}

	runtimeContext, stopRuntime := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopRuntime()

	var reviewService *notifications.SecureReviewService
	if cfg.PublicAPIBaseURL != "" && cfg.EvidenceOriginBaseURL != "" {
		reviewService, err = notifications.NewSecureReviewService(db, cfg.PublicAPIBaseURL, cfg.EvidenceOriginBaseURL, cfg.EvidenceOriginAuthToken, cfg.SecureVideoLinkTTL)
		if err != nil {
			logger.Error("configure secure evidence review", "error", err)
			os.Exit(1)
		}
	}
	apiServer := server.New(cfg, db, logger)
	apiServer.SetSecureReviewService(reviewService)
	httpServer := &http.Server{
		Addr: cfg.Address(), Handler: apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second,
	}
	if cfg.NotificationWorkerEnabled {
		bindingContext, cancelBindings := context.WithTimeout(runtimeContext, 10*time.Second)
		err = notifications.CheckCredentialBindings(bindingContext, db, cfg.NotificationCredentialBindings)
		cancelBindings()
		if err != nil {
			logger.Error("notification credential configuration is incomplete", "error", err)
			os.Exit(1)
		}
		resolver := notifications.NewEnvCredentialResolver(cfg.NotificationCredentialBindings...)
		telegram := notifications.NewTelegramSender(resolver, notifications.TelegramSenderOptions{})
		whatsApp := notifications.NewWhatsAppSender(resolver, notifications.WhatsAppSenderOptions{})
		worker := notifications.NewWorker(db, logger, []notifications.Sender{telegram, whatsApp}, reviewService, notifications.WorkerOptions{
			PollInterval: cfg.NotificationPollInterval, LeaseDuration: cfg.NotificationLeaseDuration, BatchSize: cfg.NotificationBatchSize,
		})
		go worker.Run(runtimeContext)
		logger.Info("notification worker enabled", "batchSize", cfg.NotificationBatchSize, "secureReviewLinks", reviewService != nil)
	}
	go func() {
		logger.Info("API listening", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("API stopped", "error", err)
			os.Exit(1)
		}
	}()

	<-runtimeContext.Done()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
