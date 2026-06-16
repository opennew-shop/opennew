// Package main 是账本服务(ledger)的入口,
// 对外提供双分录账本的钱包余额与分录查询 HTTP 服务。
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

	"github.com/ancf-commerce/ancf/services/ledger/internal/handler"
	"github.com/ancf-commerce/ancf/services/ledger/internal/repository"
	"github.com/ancf-commerce/ancf/services/ledger/internal/service"
)

// main 启动账本服务:初始化结构化日志、连接 PostgreSQL、装配 repository/service/handler 三层,
// 注册健康检查与钱包余额/分录查询路由,并监听信号实现优雅关闭。
func main() {
	// Structured JSON logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	port := os.Getenv("LEDGER_PORT")
	if port == "" {
		port = "8086"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	logger.Info("starting ANCF Ledger Service",
		"port", port,
	)

	// Database connection
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

	// Wire up layers
	ledgerRepo := repository.NewLedgerRepository(db)
	ledgerSvc := service.NewLedgerService(ledgerRepo, db)
	balanceHandler := handler.NewBalanceHandler(ledgerSvc)

	// Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   "ledger-service",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"checks": gin.H{
				"database": "connected",
			},
		})
	})

	// Wallet API
	wallet := r.Group("/api/v1/wallet")
	wallet.GET("/balance", balanceHandler.GetBalance)
	wallet.GET("/entries", balanceHandler.GetEntries)

	logger.Info("routes registered",
		"GET_health", "/health",
		"GET_balance", "/api/v1/wallet/balance",
		"GET_entries", "/api/v1/wallet/entries",
	)

	// HTTP server
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		logger.Info("Ledger Service listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-done
	logger.Info("shutting down Ledger Service", "signal", sig.String())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Ledger Service stopped gracefully")
}
