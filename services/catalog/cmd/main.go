// Package main 启动 catalog 商品目录服务：装配数据库连接与各层依赖（仓库、目录服务、
// 向量嵌入、混合检索、RAG），注册商品搜索与 Agent 商品管理的 HTTP 路由，并以优雅关闭方式运行 Gin 服务。
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

	"github.com/ancf-commerce/ancf/services/catalog/internal/handler"
	"github.com/ancf-commerce/ancf/services/catalog/internal/repository"
	"github.com/ancf-commerce/ancf/services/catalog/internal/service"
)

// main 是 catalog 服务进程入口：读取环境变量配置、建立并校验数据库连接、装配各层依赖与路由，
// 然后启动 HTTP 服务并在收到中断 / SIGTERM 信号时优雅关闭。
func main() {
	// Read configuration from environment variables.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	port := os.Getenv("CATALOG_PORT")
	if port == "" {
		port = "8083"
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
	skuRepo := repository.NewSKURepository(db)
	catalogSvc := service.NewCatalogService(db, skuRepo)

	// Initialize embedding service with mock provider (Phase 1).
	// Phase 2+: replace with OpenAIEmbeddingProvider + pgvector repository.
	mockProvider := service.NewMockEmbeddingProvider()
	embeddingSvc := service.NewEmbeddingService(mockProvider, nil)

	hybridSvc := service.NewHybridSearchService(catalogSvc, embeddingSvc)
	ragSvc := service.NewRAGService(hybridSvc)

	searchHandler := handler.NewHybridSearchHandler(catalogSvc, hybridSvc, ragSvc)
	productHandler := handler.NewProductHandler(catalogSvc)

	// Set up Gin router.
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Health check.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "catalog-service",
			"version": "0.1.0",
		})
	})

	// Search endpoint (supports ?mode=hybrid|keyword|vector).
	r.GET("/api/v1/cli/search", searchHandler.Search)

	// Agent RAG semantic search endpoint.
	r.GET("/api/v1/cli/rag-search", searchHandler.RagSearch)

	// Agent Product Upload endpoints.
	r.POST("/api/v1/catalog/products", productHandler.CreateProduct)
	r.PUT("/api/v1/catalog/products/:sku_id", productHandler.UpdateProduct)
	r.DELETE("/api/v1/catalog/products/:sku_id", productHandler.DeleteProduct)
	r.GET("/api/v1/catalog/products", productHandler.ListProducts)
	r.GET("/api/v1/catalog/products/:sku_id", productHandler.GetProduct)

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
		log.Printf("[INFO] Catalog Service listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server failed: %v", err)
		}
	}()

	<-done
	log.Printf("[INFO] Shutting down Catalog Service...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("[FATAL] Server forced to shutdown: %v", err)
	}

	log.Printf("[INFO] Catalog Service stopped gracefully")
}
