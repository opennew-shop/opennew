// Package main 是 checkout 结算服务的程序入口。
// 负责加载环境配置、建立 PostgreSQL 连接池、装配依赖（订单/报价/SKU/Outbox 仓储与结算服务），
// 注册 prepare/commit HTTP 路由，并以优雅关闭方式启动 Gin 服务。
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"

	catalogRepo "github.com/ancf-commerce/ancf/services/catalog/internal/repository"
	"github.com/ancf-commerce/ancf/services/checkout/internal/handler"
	"github.com/ancf-commerce/ancf/services/checkout/internal/repository"
	"github.com/ancf-commerce/ancf/services/checkout/internal/service"
	quoteRepo "github.com/ancf-commerce/ancf/services/quote/internal/repository"
	quoteSvc "github.com/ancf-commerce/ancf/services/quote/internal/service"
)

// main 启动 checkout 服务：读取环境配置、连接并校验 PostgreSQL、配置连接池、
// 装配依赖、注册路由，并监听 SIGINT/SIGTERM 实现优雅关闭。
func main() {
	// Read configuration from environment variables.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	port := os.Getenv("CHECKOUT_PORT")
	if port == "" {
		port = "8082"
	}

	domain := os.Getenv("ANCF_DOMAIN")
	if domain == "" {
		domain = "ancf-commerce.local"
	}

	shopID := os.Getenv("ANCF_SHOP_ID")
	if shopID == "" {
		shopID = "shop_default_v1"
	}

	// Open database connection.
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("[FATAL] Failed to open database: %v", err)
	}
	defer db.Close()

	// Verify database connectivity.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("[FATAL] Database ping failed: %v", err)
	}
	log.Printf("[INFO] Connected to PostgreSQL")

	// Configure connection pool.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Wire dependencies.
	// 依赖装配链：订单/报价/SKU/Outbox 仓储 → 报价服务 → 结算服务 → HTTP 处理器。
	orderRepo := repository.NewOrderRepository(db)
	qRepo := quoteRepo.NewQuoteRepository(db)
	skuRepo := catalogRepo.NewSKURepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	qSvc := quoteSvc.NewQuoteService(qRepo, skuRepo)
	checkoutSvc := service.NewCheckoutService(db, orderRepo, qRepo, qSvc, skuRepo, outboxRepo, domain, shopID)
	checkoutHandler := handler.NewCheckoutHandler(checkoutSvc)

	// Set up Gin router.
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Health check.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "checkout-service",
			"version": "0.1.0",
		})
	})

	// Checkout endpoints.
	r.POST("/api/v1/cli/checkout/prepare", checkoutHandler.Prepare)
	r.POST("/api/v1/cli/checkout/commit", checkoutHandler.Commit)

	// Start server with graceful shutdown.
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[INFO] Checkout Service listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server failed: %v", err)
		}
	}()

	<-done
	log.Printf("[INFO] Shutting down Checkout Service...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("[FATAL] Server forced to shutdown: %v", err)
	}

	log.Printf("[INFO] Checkout Service stopped gracefully")
}
