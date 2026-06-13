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
	"github.com/redis/go-redis/v9"

	"github.com/ancf-commerce/ancf/services/api-gateway/internal/config"
	handlers "github.com/ancf-commerce/ancf/services/api-gateway/internal/handler"
	"github.com/ancf-commerce/ancf/services/api-gateway/internal/middleware"
	"github.com/ancf-commerce/ancf/services/catalog/internal/repository"
	"github.com/ancf-commerce/ancf/services/catalog/internal/service"
)

// MOCK_BASE is the base URL of the mock/test server used during development.
// When real Go services are not yet deployed, the API Gateway proxies all
// business-logic endpoints to this mock server.
//
// In production, replace with service-specific URLs:
//
//	api.POST("/quote", handler.ReverseProxy("http://quote-service:8081/api/v1/cli/quote"))
//	api.POST("/checkout/prepare", handler.ReverseProxy("http://checkout-service:8082/api/v1/cli/checkout/prepare"))
//
// Or use ProxyWithFallback for automatic failover:
//
//	api.POST("/quote", handler.ProxyWithFallback(
//	    "http://quote-service:8081/api/v1/cli/quote",
//	    MOCK_BASE+"/api/v1/cli/quote",
//	))
const MOCK_BASE = "http://127.0.0.1:9080"

func main() {
	cfg := config.Load()

	// Initialize structured JSON logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))

	logger.Info("starting ANCF API Gateway",
		"port", cfg.Port,
		"log_level", cfg.LogLevel,
		"rate_limit_enabled", cfg.RateLimit.Enabled,
	)

	// Database connection
	databaseURL := cfg.DatabaseURL
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	var db *sql.DB
	var err error
	db, err = sql.Open("postgres", databaseURL)
	if err != nil {
		logger.Warn("database connection failed, catalog endpoints will not be available", "error", err)
	} else {
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pingCancel()
		if err := db.PingContext(pingCtx); err != nil {
			logger.Warn("database ping failed, catalog endpoints will not be available", "error", err)
			db.Close()
			db = nil
		} else {
			logger.Info("connected to PostgreSQL")
			db.SetMaxOpenConns(25)
			db.SetMaxIdleConns(10)
			db.SetConnMaxLifetime(5 * time.Minute)
		}
	}
	if db != nil {
		defer db.Close()
	}

	// Initialize Redis client for rate limiting
	var redisClient *redis.Client
	if cfg.RateLimit.Enabled {
		opts, redisParseErr := redis.ParseURL(cfg.RedisURL)
		if redisParseErr != nil {
			logger.Warn("failed to parse Redis URL, rate limiting will use in-memory fallback",
				"redis_url", cfg.RedisURL,
				"error", redisParseErr,
			)
		} else {
			redisClient = redis.NewClient(opts)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := redisClient.Ping(ctx).Err(); err != nil {
				logger.Warn("failed to connect to Redis, rate limiting will use in-memory fallback",
					"error", err,
				)
				redisClient = nil
			} else {
				logger.Info("connected to Redis for rate limiting")
			}
		}
	}
	if redisClient != nil {
		defer redisClient.Close()
	}

	// Set Gin mode based on log level
	if cfg.LogLevel == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create Gin router
	r := gin.New()

	// --- Global Middleware Chain ---
	// 1. Request logging (request_id, method, path, status, duration)
	r.Use(middleware.RequestLogger())

	// 2. CORS (allow Agent local renderer cross-origin requests)
	r.Use(middleware.CORS())

	// 3. Gin recovery (prevents crashes from panics)
	r.Use(gin.Recovery())

	// --- Public Endpoints (no authentication required) ---
	r.GET("/health", handlers.HealthCheck())
	r.GET("/.well-known/agent-rules.json", handlers.ServeManifest(cfg))

	// --- API Routes (API Key authentication required) ---
	api := r.Group("/api/v1/cli")
	api.Use(middleware.APIKeyAuth(cfg))
	api.Use(middleware.RateLimit(cfg, redisClient))

	// GET /api/v1/cli/search (display-only, no schema validation)
	// Supports modes: hybrid (RRF fusion), keyword (FTS), vector (semantic).
	if db != nil {
		skuRepo := repository.NewSKURepository(db)
		catalogSvc := service.NewCatalogService(db, skuRepo)

		// Initialize embedding service with mock provider (Phase 1).
		// Phase 2+: replace with OpenAIEmbeddingProvider + pgvector repository.
		mockProvider := service.NewMockEmbeddingProvider()
		embeddingSvc := service.NewEmbeddingService(mockProvider, nil)

		hybridSvc := service.NewHybridSearchService(catalogSvc, embeddingSvc)
		ragSvc := service.NewRAGService(hybridSvc)

		catalogHandler := handlers.NewCatalogSearchHandler(catalogSvc, hybridSvc, ragSvc)
		api.GET("/search", catalogHandler)
		logger.Info("catalog search endpoint registered (hybrid: keyword + vector RRF)")

		// GET /api/v1/cli/rag-search (Agent RAG semantic product discovery)
		ragHandler := handlers.NewRAGSearchHandler(ragSvc, catalogSvc, hybridSvc)
		api.GET("/rag-search", ragHandler)
		logger.Info("Agent RAG search endpoint registered at GET /api/v1/cli/rag-search")
	} else {
		api.GET("/search", searchPlaceholder())
		api.GET("/rag-search", searchPlaceholder())
		logger.Info("catalog search endpoints registered as placeholders (no database)")
	}

	// POST /api/v1/cli/quote (dev: proxy to mock; prod: replace with real service)
	api.POST("/quote",
		middleware.SchemaValidator("schemas"),
		handlers.ReverseProxy(MOCK_BASE+"/api/v1/cli/quote"),
	)

	// Checkout endpoints (dev: proxy to mock; prod: replace with real service)
	// Requires schema validation + HTTP signature
	checkout := api.Group("/checkout")
	checkout.Use(middleware.HTTPSignature())
	checkout.POST("/prepare",
		middleware.SchemaValidator("schemas"),
		handlers.ReverseProxy(MOCK_BASE+"/api/v1/cli/checkout/prepare"),
	)
	checkout.POST("/commit",
		middleware.SchemaValidator("schemas"),
		handlers.ReverseProxy(MOCK_BASE+"/api/v1/cli/checkout/commit"),
	)

	// --- Wallet endpoints (dev: proxy to mock; prod: replace with real service) ---
	wallet := r.Group("/api/v1/wallet")
	wallet.Use(middleware.APIKeyAuth(cfg))
	wallet.Use(middleware.RateLimit(cfg, redisClient))
	wallet.GET("/balance", handlers.ReverseProxy(MOCK_BASE+"/api/v1/wallet/balance"))
	wallet.POST("/deposit-intents", handlers.ReverseProxy(MOCK_BASE+"/api/v1/wallet/deposit-intents"))
	wallet.POST("/redeem", handlers.ReverseProxy(MOCK_BASE+"/api/v1/wallet/redeem"))
	wallet.POST("/deposit-confirm", handlers.ReverseProxy(MOCK_BASE+"/api/v1/wallet/deposit-confirm"))
	wallet.GET("/mint-status", handlers.ReverseProxy(MOCK_BASE+"/api/v1/wallet/mint-status"))
	wallet.GET("/redeem-status", handlers.ReverseProxy(MOCK_BASE+"/api/v1/wallet/redeem-status"))
	wallet.GET("/reserve-info", handlers.ReverseProxy(MOCK_BASE+"/api/v1/wallet/reserve-info"))

	// --- Server ---
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		logger.Info("API Gateway listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-done
	logger.Info("shutting down API Gateway", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("API Gateway stopped gracefully")
}

// --- Placeholder Handlers ---
// searchPlaceholder returns 501 when the database is not available.
// All other business endpoints (quote, checkout, wallet) now use ReverseProxy
// to the mock server (MOCK_BASE) and no longer need placeholder handlers.

// searchPlaceholder returns 501 Not Implemented for GET /api/v1/cli/search.
func searchPlaceholder() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": gin.H{
				"code":       "NOT_IMPLEMENTED",
				"message":    "Search API is not yet available (no database connection).",
				"request_id": c.GetString("request_id"),
			},
		})
	}
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
