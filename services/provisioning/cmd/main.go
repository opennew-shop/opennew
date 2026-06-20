// Package main 是 ANCF 服务开通服务 (Provisioning Service) 的入口。
// 它采用 Outbox 驱动：后台监听 order_committed 事件并异步开通算力租用等服务，
// 同时注册管理端手动开通/状态查询与面向用户的访问凭据获取 HTTP 路由。
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

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"

	ledgerRepo "github.com/ancf-commerce/ancf/services/ledger/repository"
	"github.com/ancf-commerce/ancf/services/provisioning/internal/handler"
	"github.com/ancf-commerce/ancf/services/provisioning/internal/repository"
	"github.com/ancf-commerce/ancf/services/provisioning/internal/service"
)

// main 初始化日志、数据库连接池与各分层，启动后台 Outbox 监听协程，
// 注册健康检查、管理端与 CLI 路由，并在收到中断信号时取消监听并优雅关闭 HTTP 服务。
func main() {
	// Structured JSON logger.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	port := os.Getenv("PROVISIONING_PORT")
	if port == "" {
		port = "8085"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	// Outbox polling interval (default 2 seconds).
	pollInterval := 2 * time.Second
	if v := os.Getenv("PROVISIONING_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			pollInterval = d
		}
	}

	logger.Info("starting ANCF Provisioning Service",
		"port", port,
		"poll_interval", pollInterval.String(),
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
	//   provisioning repo -> provisioning service
	//   ledger repo (from ledger service) -> provisioning service
	provRepo := repository.NewProvisioningRepository(db)
	ledgerRepository := ledgerRepo.NewLedgerRepository(db)
	provService := service.NewProvisioningService(db, provRepo, ledgerRepository)
	provHandler := handler.NewProvisioningHandler(provService)

	// Start the outbox listener in the background.
	// This polls the outbox table for order_committed events and processes them.
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	provService.StartOutboxListener(rootCtx, pollInterval)

	// Gin router.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Health check.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   "provisioning-service",
			"version":   "0.1.0",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"checks": gin.H{
				"database":        "connected",
				"outbox_listener": "running",
			},
		})
	})

	// Admin API: manual provisioning and status queries.
	admin := r.Group("/api/v1/admin")
	admin.POST("/provision/:intent_id", provHandler.ManualProvision)
	admin.GET("/provision-status/:intent_id", provHandler.GetProvisioningStatus)

	// CLI API: user-facing access token retrieval.
	cli := r.Group("/api/v1/cli")
	cli.GET("/provision-access/:intent_id", provHandler.GetProvisionAccess)

	logger.Info("routes registered",
		"GET_health", "/health",
		"POST_admin_provision", "/api/v1/admin/provision/:intent_id",
		"GET_admin_provision_status", "/api/v1/admin/provision-status/:intent_id",
		"GET_cli_provision_access", "/api/v1/cli/provision-access/:intent_id",
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
		logger.Info("Provisioning Service listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-done
	logger.Info("shutting down Provisioning Service", "signal", sig.String())

	// Cancel the outbox listener context.
	rootCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Provisioning Service stopped gracefully")
}
