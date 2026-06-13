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

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/handler"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/model"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/service"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/watcher"
)

func main() {
	// Structured JSON logger.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	port := os.Getenv("CHAIN_ADAPTER_PORT")
	if port == "" {
		port = "8084"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://ancf:ancf_dev@localhost:5432/ancf_commerce?sslmode=disable"
	}

	solanaRPCEndpoint := os.Getenv("SOLANA_RPC_ENDPOINT")
	if solanaRPCEndpoint == "" {
		solanaRPCEndpoint = "https://api.mainnet-beta.solana.com"
	}

	sonicRPCEndpoint := os.Getenv("SONIC_RPC_ENDPOINT")
	if sonicRPCEndpoint == "" {
		sonicRPCEndpoint = "https://rpc.sonicl2.com"
	}

	logger.Info("starting ANCF Chain Adapter Service",
		"port", port,
		"solana_rpc", solanaRPCEndpoint,
		"sonic_rpc", sonicRPCEndpoint,
	)

	// ------------------------------------------------------------------
	// Database connection
	// ------------------------------------------------------------------
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
	db.SetConnLifetime(5 * time.Minute)

	// ------------------------------------------------------------------
	// Wire up layers
	// ------------------------------------------------------------------
	chainRepo := repository.NewChainRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)

	// MintService HTTP client for the deposit processor.
	mintURL := os.Getenv("MINT_SERVICE_URL")
	if mintURL == "" {
		mintURL = "http://localhost:8087"
	}
	mintClient := service.NewMintServiceClient(mintURL)

	// Reserve address map — loaded from the database at startup.
	// In Phase 3, this maps to the placeholder addresses seeded in 001_init.sql.
	reserveSolana := map[string]string{
		"vUSDC": os.Getenv("SOLANA_RESERVE_USDC"),
	}
	if reserveSolana["vUSDC"] == "" {
		// Fallback to DB seed value.
		acct, err := chainRepo.GetReserveAccount(context.Background(), string(model.NetworkSolanaMainnet), "vUSDC")
		if err != nil {
			logger.Warn("failed to load Solana vUSDC reserve account from DB, using placeholder", "error", err)
			reserveSolana["vUSDC"] = "RESERVE_WALLET_SOL_PLACEHOLDER"
		} else if acct != nil {
			reserveSolana["vUSDC"] = acct.Address
		} else {
			reserveSolana["vUSDC"] = "RESERVE_WALLET_SOL_PLACEHOLDER"
		}
	}

	reserveSonic := map[string]string{
		"vUSDC": os.Getenv("SONIC_RESERVE_USDC"),
	}
	if reserveSonic["vUSDC"] == "" {
		acct, err := chainRepo.GetReserveAccount(context.Background(), string(model.NetworkSonicL2), "vUSDC")
		if err != nil {
			logger.Warn("failed to load Sonic vUSDC reserve account from DB, using placeholder", "error", err)
			reserveSonic["vUSDC"] = "RESERVE_WALLET_SONIC_PLACEHOLDER"
		} else if acct != nil {
			reserveSonic["vUSDC"] = acct.Address
		} else {
			reserveSonic["vUSDC"] = "RESERVE_WALLET_SONIC_PLACEHOLDER"
		}
	}

	// ------------------------------------------------------------------
	// Deposit watchers (Phase 3 skeleton — polling is no-op until RPC
	// integration is implemented; the simulate-deposit endpoint drives
	// the pipeline for testing).
	// ------------------------------------------------------------------

	// Solana deposit watcher.
	solanaWatcher := watcher.NewSolanaDepositWatcher(
		solanaRPCEndpoint,
		reserveSolana,
		chainRepo,
	)
	// Wire outbox support for cross-service eventual consistency.
	solanaWatcher.SetOutbox(db, outboxRepo)
	// The event handler is wired after construction. In Phase 3 this is nil
	// and the simulate-deposit endpoint bypasses the watcher.

	// Sonic-L2 deposit watcher (skeleton — same pattern as Solana).
	_ = watcher.NewDepositWatcher(
		model.NetworkSonicL2,
		sonicRPCEndpoint,
		reserveSonic,
		chainRepo,
		nil, // eventHandler wired post-construction
	)
	// sonicWatcher.Start(ctx) — deferred to Phase 3 RPC integration.

	// ------------------------------------------------------------------
	// Simulate deposit bridge (Phase 3 development tool)
	// ------------------------------------------------------------------
	// The simulateDepositFn directly feeds a synthetic DepositEvent into
	// the pipeline: saves to chain_txs, then invokes the event handler.
	// In production this function should be nil (disabled).
	simulateDepositFn := func(event *model.DepositEvent) error {
		logger.Info("simulate-deposit invoked",
			"network", event.Network,
			"tx_hash", event.TxHash,
			"from", event.FromAddress,
			"to", event.ToAddress,
			"amount", event.AmountMinor,
		)
		return solanaWatcher.ProcessDepositEvent(context.Background(), event)
	}

	// ------------------------------------------------------------------
	// HTTP handlers and router
	// ------------------------------------------------------------------
	chainHandler := handler.NewChainHandler(chainRepo, simulateDepositFn)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   "chain-adapter-service",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"checks": gin.H{
				"database": "connected",
				"watchers": gin.H{
					"solana": gin.H{
						"running":   solanaWatcher.IsRunning(),
						"last_block": solanaWatcher.LastBlock(),
					},
				},
			},
		})
	})

	// Chain API
	chain := r.Group("/api/v1/chain")
	chain.GET("/tx/:tx_hash", chainHandler.GetChainTx)
	chain.GET("/reserve", chainHandler.ListReserveAccounts)
	chain.GET("/reserve/:asset_symbol", chainHandler.GetReserveAccount)
	chain.POST("/simulate-deposit", chainHandler.SimulateDeposit)

	logger.Info("routes registered",
		"GET_health", "/health",
		"GET_chain_tx", "/api/v1/chain/tx/:tx_hash",
		"GET_chain_reserve_list", "/api/v1/chain/reserve",
		"GET_chain_reserve_detail", "/api/v1/chain/reserve/:asset_symbol",
		"POST_chain_simulate_deposit", "/api/v1/chain/simulate-deposit",
	)

	// ------------------------------------------------------------------
	// Start deposit watchers (Phase 3: polling is skeleton, no-op).
	// ------------------------------------------------------------------
	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()
	solanaWatcher.Start(watcherCtx)
	logger.Info("Solana deposit watcher started (skeleton mode)")

	// ------------------------------------------------------------------
	// Start outbox deposit processor (SUB-020: cross-service outbox
	// eventual consistency). The processor polls the outbox table for
	// deposit_detected events and delivers them to MintService.
	// ------------------------------------------------------------------
	depositProcessor := service.NewDepositProcessor(outboxRepo, mintClient)
	processorCtx, processorCancel := context.WithCancel(context.Background())
	defer processorCancel()
	depositProcessor.Start(processorCtx)
	logger.Info("Deposit outbox processor started",
		"poll_interval", "2s",
		"mint_url", mintURL,
	)

	// ------------------------------------------------------------------
	// HTTP server with graceful shutdown
	// ------------------------------------------------------------------
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		logger.Info("Chain Adapter Service listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-done
	logger.Info("shutting down Chain Adapter Service", "signal", sig.String())

	watcherCancel()
	solanaWatcher.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Chain Adapter Service stopped gracefully")
}
