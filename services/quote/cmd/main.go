// Package main 是报价服务(quote)的入口,
// 提供 5 分钟 TTL 的服务端权威报价 HTTP 服务。
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

	catalogRepo "github.com/ancf-commerce/ancf/services/catalog/repository"
	"github.com/ancf-commerce/ancf/services/quote/internal/handler"
	"github.com/ancf-commerce/ancf/services/quote/internal/repository"
	"github.com/ancf-commerce/ancf/services/quote/internal/service"
)

// main 启动报价服务:读取环境配置、连接 PostgreSQL、装配报价与 SKU 依赖,
// 注册健康检查与报价生成路由,并监听信号实现优雅关闭。
func main() {
	// Read configuration from environment variables.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	port := os.Getenv("QUOTE_PORT")
	if port == "" {
		port = "8081"
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
	quoteRepo := repository.NewQuoteRepository(db)
	skuRepo := catalogRepo.NewSKURepository(db)
	quoteSvc := service.NewQuoteService(quoteRepo, skuRepo)
	quoteHandler := handler.NewQuoteHandler(quoteSvc)

	// Set up Gin router.
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Health check.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "quote-service",
			"version": "0.1.0",
		})
	})

	// Quote endpoint.
	r.POST("/api/v1/cli/quote", quoteHandler.GenerateQuote)

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
		log.Printf("[INFO] Quote Service listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server failed: %v", err)
		}
	}()

	<-done
	log.Printf("[INFO] Shutting down Quote Service...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("[FATAL] Server forced to shutdown: %v", err)
	}

	log.Printf("[INFO] Quote Service stopped gracefully")
}
