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

	"github.com/ancf-commerce/ancf/services/audit/internal/handler"
	"github.com/ancf-commerce/ancf/services/audit/internal/repository"
	"github.com/ancf-commerce/ancf/services/audit/internal/service"
)

func main() {
	// Structured JSON logger.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	port := os.Getenv("AUDIT_PORT")
	if port == "" {
		port = "8089"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	logger.Info("starting ANCF Audit Service",
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

	// Wire up layers: repository -> service -> handler.
	auditRepo := repository.NewAuditRepository(db)
	auditService := service.NewAuditService(db, auditRepo)
	auditHandler := handler.NewAuditHandler(auditService)

	// Gin router.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Health check.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   "audit-service",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"checks": gin.H{
				"database": "connected",
			},
		})
	})

	// Admin API — audit endpoints.
	// In production, add admin auth middleware (API key + role check).
	admin := r.Group("/api/v1/admin")
	admin.GET("/audit", auditHandler.QueryAuditEvents)
	admin.POST("/audit", auditHandler.RecordAdminAuditEvent)
	admin.GET("/audit/recent", auditHandler.GetRecentAuditEvents)
	admin.GET("/audit/:event_id", auditHandler.GetAuditEvent)

	logger.Info("routes registered",
		"GET_health", "/health",
		"GET_admin_audit", "/api/v1/admin/audit",
		"POST_admin_audit", "/api/v1/admin/audit",
		"GET_admin_audit_recent", "/api/v1/admin/audit/recent",
		"GET_admin_audit_by_id", "/api/v1/admin/audit/:event_id",
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
		logger.Info("Audit Service listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-done
	logger.Info("shutting down Audit Service", "signal", sig.String())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Audit Service stopped gracefully")
}
