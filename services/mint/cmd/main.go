package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/gin-gonic/gin"

	chainRepo "github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
	ledgerRepo "github.com/ancf-commerce/ancf/services/ledger/internal/repository"
	ledgerSvc "github.com/ancf-commerce/ancf/services/ledger/internal/service"
	"github.com/ancf-commerce/ancf/services/mint/internal/handler"
	"github.com/ancf-commerce/ancf/services/mint/internal/repository"
	"github.com/ancf-commerce/ancf/services/mint/internal/service"
)

func main() {
	// Structured JSON logger.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	port := os.Getenv("MINT_PORT")
	if port == "" {
		port = "8087"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	logger.Info("starting ANCF Mint Service",
		"port", port,
	)

	// Database connection.
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		logger.Error("failed to ping database", "error", err)
		os.Exit(1)
	}
	logger.Info("connected to PostgreSQL")

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Wire up layers:
	//   mint + redemption repos -> services
	//   ledger repo -> ledger service -> mint + redemption services
	//   outbox repo (shared with chain-adapter) -> redemption service
	mintRepo := repository.NewMintRepository(db)
	redemptionRepo := repository.NewRedemptionRepository(db)
	outboxRepo := chainRepo.NewOutboxRepository(db)
	ledgerRepository := ledgerRepo.NewLedgerRepository(db)
	ledgerService := ledgerSvc.NewLedgerService(ledgerRepository, db)
	mintService := service.NewMintService(db, mintRepo, ledgerService)
	redemptionService := service.NewRedemptionService(redemptionRepo, ledgerService, outboxRepo, db)
	reconciliationService := service.NewReconciliationService(db, mintRepo)
	mintHandler := handler.NewMintHandler(mintService)
	redemptionHandler := handler.NewRedemptionHandler(redemptionService)
	reconciliationHandler := handler.NewReconciliationHandler(reconciliationService)

	// Gin router.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Health check.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   "mint-service",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"checks": gin.H{
				"database": "connected",
			},
		})
	})

	// Wallet API (mint + redemption endpoints).
	wallet := r.Group("/api/v1/wallet")
	// Mint endpoints.
	wallet.POST("/deposit-intents", mintHandler.CreateDepositIntent)
	wallet.POST("/deposit-confirm", mintHandler.ConfirmDeposit)
	wallet.GET("/mint-status", mintHandler.GetMintStatus)
	wallet.GET("/reserve-info", mintHandler.GetReserveInfo)
	// Redemption endpoints.
	wallet.POST("/redeem", redemptionHandler.CreateRedemption)
	wallet.POST("/redeem/:request_id/process", redemptionHandler.ProcessRedemption)
	wallet.GET("/redeem-status", redemptionHandler.GetRedemptionStatus)
	wallet.POST("/redeem/:request_id/payout", redemptionHandler.CompletePayout)
	wallet.POST("/redeem/:request_id/release", redemptionHandler.ReleaseFunds)

	// Admin API (reconciliation endpoints).
	admin := r.Group("/api/v1/admin")
	admin.POST("/reconcile", reconciliationHandler.TriggerReconciliation)
	admin.GET("/reconciliation-status", reconciliationHandler.GetReconciliationStatus)
	admin.POST("/reconcile/daily", reconciliationHandler.TriggerDailyReconciliation)

	logger.Info("routes registered",
		"GET_health", "/health",
		"POST_deposit_intents", "/api/v1/wallet/deposit-intents",
		"POST_deposit_confirm", "/api/v1/wallet/deposit-confirm",
		"GET_mint_status", "/api/v1/wallet/mint-status",
		"GET_reserve_info", "/api/v1/wallet/reserve-info",
		"POST_redeem", "/api/v1/wallet/redeem",
		"POST_redeem_process", "/api/v1/wallet/redeem/:request_id/process",
		"GET_redeem_status", "/api/v1/wallet/redeem-status",
		"POST_redeem_payout", "/api/v1/wallet/redeem/:request_id/payout",
		"POST_redeem_release", "/api/v1/wallet/redeem/:request_id/release",
		"POST_admin_reconcile", "/api/v1/admin/reconcile",
		"GET_admin_reconciliation_status", "/api/v1/admin/reconciliation-status",
		"POST_admin_reconcile_daily", "/api/v1/admin/reconcile/daily",
	)

	// HTTP server.
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		logger.Info("Mint Service listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-done
	logger.Info("shutting down Mint Service", "signal", sig.String())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Mint Service stopped gracefully")
}
