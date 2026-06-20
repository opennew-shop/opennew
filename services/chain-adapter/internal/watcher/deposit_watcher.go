// Package watcher 实现各区块链网络的充值监听器（deposit watcher）：
// 轮询 RPC 检测打入储备地址的转账，去重后写入 chain_txs，并在同一事务内
// 写入 Outbox 事件以驱动下游铸币。提供网络无关的基类与 Solana 具体实现。
package watcher

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/model"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
)

// DepositEventHandler is the callback interface that receives parsed deposit
// events. Implementations (e.g. mint-service) should handle the event
// idempotently — the watcher may fire more than once for the same deposit.
type DepositEventHandler interface {
	OnDeposit(ctx context.Context, event *model.DepositEvent) error
}

// DepositWatcher polls a blockchain RPC endpoint for incoming deposits to
// the configured reserve addresses. It is network-agnostic; network-specific
// logic (transaction parsing, address filtering) is delegated to the
// concrete watcher implementations (SolanaDepositWatcher, etc.).
type DepositWatcher struct {
	Network          model.Network
	RpcEndpoint      string
	ReserveAddresses map[string]string // assetSymbol -> address
	ChainRepo        *repository.ChainRepository
	EventHandler     DepositEventHandler
	PollInterval     time.Duration

	// Outbox support: when DB and OutboxRepo are set, processEvent writes
	// deposit_detected events to the outbox table within the same DB
	// transaction as the chain_tx save, enabling cross-service eventual consistency.
	DB         *sql.DB
	OutboxRepo *repository.OutboxRepository

	mu        sync.RWMutex
	lastBlock int64
	running   bool
	logger    *slog.Logger
}

// NewDepositWatcher creates a new DepositWatcher with sensible defaults.
func NewDepositWatcher(
	network model.Network,
	rpcEndpoint string,
	reserveAddresses map[string]string,
	chainRepo *repository.ChainRepository,
	handler DepositEventHandler,
) *DepositWatcher {
	return &DepositWatcher{
		Network:          network,
		RpcEndpoint:      rpcEndpoint,
		ReserveAddresses: reserveAddresses,
		ChainRepo:        chainRepo,
		EventHandler:     handler,
		PollInterval:     10 * time.Second,
		lastBlock:        0,
		running:          false,
		logger:           slog.Default().With("component", "deposit-watcher", "network", string(network)),
	}
}

// LastBlock returns the most recent block number that has been processed.
func (w *DepositWatcher) LastBlock() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastBlock
}

// SetOutbox enables outbox event publishing. Call after construction, before Start.
func (w *DepositWatcher) SetOutbox(db *sql.DB, outboxRepo *repository.OutboxRepository) {
	w.DB = db
	w.OutboxRepo = outboxRepo
}

// IsRunning reports whether the watcher polling loop is active.
func (w *DepositWatcher) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

// Start begins the polling loop in a background goroutine. The loop exits
// when ctx is cancelled or the watcher is stopped via Stop.
func (w *DepositWatcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		w.logger.Warn("deposit watcher is already running")
		return
	}
	w.running = true
	w.mu.Unlock()

	w.logger.Info("starting deposit watcher",
		"rpc_endpoint", w.RpcEndpoint,
		"poll_interval", w.PollInterval.String(),
		"reserve_addresses", len(w.ReserveAddresses),
	)

	go func() {
		ticker := time.NewTicker(w.PollInterval)
		defer ticker.Stop()

		// Run an immediate poll on start.
		w.pollDeposits(ctx)

		for {
			select {
			case <-ctx.Done():
				w.logger.Info("deposit watcher stopped (context cancelled)")
				w.mu.Lock()
				w.running = false
				w.mu.Unlock()
				return
			case <-ticker.C:
				w.pollDeposits(ctx)
			}
		}
	}()
}

// Stop marks the watcher as not running. The polling loop is stopped via
// context cancellation — this method is informational.
func (w *DepositWatcher) Stop() {
	w.mu.Lock()
	w.running = false
	w.mu.Unlock()
}

// pollDeposits is the core polling logic. In Phase 3 this provides a
// skeleton implementation that concrete watchers (Solana, Sonic-L2) override.
//
// The general flow:
//  1. Query the RPC for new blocks/transactions since lastBlock.
//  2. Filter transactions that transfer to a known reserve address.
//  3. Check if the tx_hash has already been processed (chain_txs table).
//  4. Parse the transaction into a DepositEvent.
//  5. Persist the event to chain_txs (status = confirmed).
//  6. Invoke eventHandler.OnDeposit(event) to trigger downstream services.
//
// This base implementation is a no-op; concrete watchers must implement
// network-specific parsing.
func (w *DepositWatcher) pollDeposits(ctx context.Context) {
	w.logger.Debug("pollDeposits - base no-op (override in concrete watcher)")
}

// processEvent handles a single deposit event: saves to chain_txs and invokes
// the callback. If outbox support is configured (DB and OutboxRepo are set),
// the chain_tx save and outbox insert occur within the same DB transaction to
// guarantee exactly-once delivery to the deposit processor.
func (w *DepositWatcher) processEvent(ctx context.Context, event *model.DepositEvent) error {
	// 1. Check for duplicate (already processed).
	existing, err := w.ChainRepo.GetByTxHash(ctx, event.Network, event.TxHash)
	if err != nil {
		return err
	}
	if existing != nil {
		w.logger.Debug("deposit tx already processed, skipping",
			"tx_hash", event.TxHash,
			"status", existing.Status,
		)
		return nil
	}

	// 2. Save to chain_txs, with outbox event in same transaction when configured.
	rawJSON, _ := json.Marshal(event)
	confirmations := event.Confirmations
	if confirmations <= 0 {
		confirmations = 32
	}
	chainTx := &model.ChainTx{
		Network:       event.Network,
		TxHash:        event.TxHash,
		TxType:        model.TxTypeDeposit,
		Status:        model.TxStatusFinalized,
		Confirmations: confirmations,
		RawJSON:       rawJSON,
	}

	if w.DB != nil && w.OutboxRepo != nil {
		// Outbox pattern: save chain_tx and outbox event in one transaction.
		tx, txErr := w.DB.BeginTx(ctx, nil)
		if txErr != nil {
			w.logger.Error("failed to begin tx for chain_tx + outbox", "tx_hash", event.TxHash, "error", txErr)
			return txErr
		}
		defer tx.Rollback()

		if err := w.ChainRepo.SaveChainTxWithTx(ctx, tx, chainTx); err != nil {
			if errors.Is(err, repository.ErrDuplicateChainTx) {
				return nil
			}
			w.logger.Error("failed to save chain tx (with tx)", "tx_hash", event.TxHash, "error", err)
			return err
		}

		if err := w.ChainRepo.IncrementReserveConfirmedWithTx(ctx, tx, event.Network, event.AssetSymbol, event.AmountMinor); err != nil {
			w.logger.Error("failed to increment reserve balance", "tx_hash", event.TxHash, "error", err)
			return err
		}

		// Write outbox event so the DepositProcessor picks it up.
		outboxEvent := &repository.OutboxEvent{
			EventID:       generateWOutboxID("evt_"),
			EventType:     "deposit_detected",
			AggregateType: "chain_deposit",
			AggregateID:   event.TxHash,
			Payload:       mustMarshalOutboxPayload(event),
		}
		if err := w.OutboxRepo.InsertWithTx(ctx, tx, outboxEvent); err != nil {
			w.logger.Error("failed to insert outbox event", "tx_hash", event.TxHash, "error", err)
			return err
		}

		if err := tx.Commit(); err != nil {
			w.logger.Error("failed to commit chain_tx + outbox", "tx_hash", event.TxHash, "error", err)
			return err
		}
	} else {
		// No outbox configured: save chain_tx directly (backward compatible).
		if err := w.ChainRepo.SaveChainTx(ctx, chainTx); err != nil {
			w.logger.Error("failed to save chain tx", "tx_hash", event.TxHash, "error", err)
			return err
		}
	}

	// 3. Invoke the event handler callback (after commit so outbox event is visible).
	if w.EventHandler != nil {
		if err := w.EventHandler.OnDeposit(ctx, event); err != nil {
			w.logger.Error("deposit event handler failed",
				"tx_hash", event.TxHash,
				"error", err,
			)
			// Do NOT return an error here — the tx + outbox are already persisted.
			// The deposit processor will handle downstream delivery via outbox.
		}
	}

	w.logger.Info("deposit processed",
		"network", event.Network,
		"tx_hash", event.TxHash,
		"from", event.FromAddress,
		"to", event.ToAddress,
		"amount_minor", event.AmountMinor,
		"asset", event.AssetSymbol,
		"block", event.BlockNumber,
	)
	return nil
}

// generateWOutboxID creates a random hex-encoded ID with the given prefix.
func generateWOutboxID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// mustMarshalOutboxPayload builds the JSON payload for a deposit_detected outbox event.
func mustMarshalOutboxPayload(event *model.DepositEvent) []byte {
	type depositPayload struct {
		Network         string `json:"network"`
		TxHash          string `json:"tx_hash"`
		FromAddress     string `json:"from_address"`
		ToAddress       string `json:"to_address"`
		AmountMinor     int64  `json:"amount_minor"`
		AssetSymbol     string `json:"asset_symbol"`
		MintAddress     string `json:"mint_address"`
		DepositIntentID string `json:"deposit_intent_id,omitempty"`
		BlockNumber     int64  `json:"block_number"`
		Confirmations   int    `json:"confirmations"`
	}
	data, err := json.Marshal(depositPayload{
		Network:         event.Network,
		TxHash:          event.TxHash,
		FromAddress:     event.FromAddress,
		ToAddress:       event.ToAddress,
		AmountMinor:     event.AmountMinor,
		AssetSymbol:     event.AssetSymbol,
		MintAddress:     event.MintAddress,
		DepositIntentID: event.DepositIntentID,
		BlockNumber:     event.BlockNumber,
		Confirmations:   event.Confirmations,
	})
	if err != nil {
		return []byte("{}")
	}
	return data
}
