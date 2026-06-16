// Package main 是 ANCF mint（铸币）服务的程序入口。
// 它负责装配各层依赖（mint/redemption/reconciliation 仓库与服务、
// 复用的 ledger 双分录服务以及 chain-adapter 的 outbox 仓库），
// 注册 HTTP 路由（钱包侧铸币/赎回、服务间内部接口、管理端储备对账），
// 并启动带优雅停机的 HTTP 服务（默认监听 :8087）。
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
	"github.com/ancf-commerce/ancf/services/mint/internal/middleware"
	"github.com/ancf-commerce/ancf/services/mint/internal/repository"
	"github.com/ancf-commerce/ancf/services/mint/internal/service"
)

// main 加载配置、连接 PostgreSQL、装配各层依赖并注册路由，
// 随后启动 HTTP 服务并监听中断信号以执行优雅停机。
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
	internalAPIKey := os.Getenv("INTERNAL_API_KEY")

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
	wallet.GET("/mint-status", mintHandler.GetMintStatus)
	wallet.GET("/reserve-info", mintHandler.GetReserveInfo)
	// Redemption endpoints.
	wallet.POST("/redeem", redemptionHandler.CreateRedemption)
	wallet.GET("/redeem-status", redemptionHandler.GetRedemptionStatus)

	internal := r.Group("/api/v1/internal")
	internal.Use(middleware.InternalAPIKeyAuth(internalAPIKey))
	internal.POST("/deposit-confirm", mintHandler.ConfirmDeposit)
	internal.POST("/redeem/:request_id/process", redemptionHandler.ProcessRedemption)
	internal.POST("/redeem/:request_id/payout", redemptionHandler.CompletePayout)
	internal.POST("/redeem/:request_id/release", redemptionHandler.ReleaseFunds)

	// Admin API (reconciliation endpoints).
	admin := r.Group("/api/v1/admin")
	admin.POST("/reconcile", reconciliationHandler.TriggerReconciliation)
	admin.GET("/reconciliation-status", reconciliationHandler.GetReconciliationStatus)
	admin.POST("/reconcile/daily", reconciliationHandler.TriggerDailyReconciliation)

	logger.Info("routes registered",
		"GET_health", "/health",
		"POST_deposit_intents", "/api/v1/wallet/deposit-intents",
		"GET_mint_status", "/api/v1/wallet/mint-status",
		"GET_reserve_info", "/api/v1/wallet/reserve-info",
		"POST_redeem", "/api/v1/wallet/redeem",
		"GET_redeem_status", "/api/v1/wallet/redeem-status",
		"POST_internal_deposit_confirm", "/api/v1/internal/deposit-confirm",
		"POST_internal_redeem_process", "/api/v1/internal/redeem/:request_id/process",
		"POST_internal_redeem_payout", "/api/v1/internal/redeem/:request_id/payout",
		"POST_internal_redeem_release", "/api/v1/internal/redeem/:request_id/release",
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
