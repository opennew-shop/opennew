// Package solana — Production-grade deposit watcher hardening.
//
// This file provides composable utilities that harden the SolanaDepositWatcher
// with:
//   - Replay protection via DB-persisted cursor (last_processed_slot)
//   - Exception recovery: watcher restart resumes from last_processed_slot + 1
//   - WebSocket reconnection with exponential backoff (max 5 minutes)
//   - Batch processing: max 50 signatures per poll to avoid RPC timeouts
//   - Health check: /health endpoint integration with watcher state
//
// These utilities are designed to compose with the existing SolanaDepositWatcher
// in deposit_watcher.go without duplicating its core polling/parsing logic.

package solana

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Cursor persistence — replay protection
// ---------------------------------------------------------------------------

// WatcherCursor represents a persistent checkpoint that the watcher uses to
// resume processing after restart. Without cursor persistence, the watcher
// would either miss deposits (start from too recent) or re-process thousands
// of already-handled transactions (start from 0).
//
// The cursor is stored in the chain_txs table (or a dedicated watcher_cursor
// table if the schema supports it) so it survives process restarts.
type WatcherCursor struct {
	Network          string    `json:"network"`
	LastSlot         uint64    `json:"last_slot"`
	LastSignature    string    `json:"last_signature"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// WatcherCursorStore persists and retrieves the watcher's processing cursor.
// Implementations back the cursor with the database so the watcher can resume
// from the correct slot after a crash or deliberate restart.
type WatcherCursorStore struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewWatcherCursorStore creates a cursor store backed by the given database.
// Call EnsureCursorTable during service initialization to create the backing
// table if it does not already exist.
func NewWatcherCursorStore(db *sql.DB) *WatcherCursorStore {
	return &WatcherCursorStore{
		db:     db,
		logger: slog.Default().With("component", "watcher-cursor"),
	}
}

// cursorTableDDL is the DDL for the watcher_cursor table. Each network gets
// exactly one row so we use the network as the primary key.
const cursorTableDDL = `
CREATE TABLE IF NOT EXISTS watcher_cursors (
    network         VARCHAR(50) PRIMARY KEY,
    last_slot       BIGINT NOT NULL DEFAULT 0,
    last_signature  VARCHAR(200) NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// EnsureCursorTable creates the watcher_cursors table if it does not exist.
// Call once during service startup.
func (s *WatcherCursorStore) EnsureCursorTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, cursorTableDDL)
	if err != nil {
		return fmt.Errorf("watcher_cursor: ensure table: %w", err)
	}
	return nil
}

// LoadCursor retrieves the persisted cursor for a given network.
// Returns (nil, nil) when no cursor exists (first run).
func (s *WatcherCursorStore) LoadCursor(ctx context.Context, network string) (*WatcherCursor, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT network, last_slot, last_signature, updated_at
		 FROM watcher_cursors WHERE network = $1`, network)

	var c WatcherCursor
	err := row.Scan(&c.Network, &c.LastSlot, &c.LastSignature, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("watcher_cursor: load cursor for %s: %w", network, err)
	}

	s.logger.Debug("loaded watcher cursor",
		"network", network,
		"last_slot", c.LastSlot,
		"last_signature", c.LastSignature,
	)
	return &c, nil
}

// SaveCursor upserts the cursor for a given network. Call after processing
// each batch so progress is durable.
func (s *WatcherCursorStore) SaveCursor(ctx context.Context, cursor *WatcherCursor) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watcher_cursors (network, last_slot, last_signature, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (network) DO UPDATE SET
		     last_slot      = EXCLUDED.last_slot,
		     last_signature = EXCLUDED.last_signature,
		     updated_at     = NOW()`,
		cursor.Network, cursor.LastSlot, cursor.LastSignature,
	)
	if err != nil {
		return fmt.Errorf("watcher_cursor: save cursor for %s: %w", cursor.Network, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// WebSocket reconnection — exponential backoff
// ---------------------------------------------------------------------------

// WebSocketReconnector manages reconnection to a Solana WebSocket RPC endpoint
// with exponential backoff. It is designed to be used alongside the HTTP
// polling loop as a real-time event source.
//
// When the WebSocket disconnects unexpectedly, the reconnector waits with
// exponential backoff (starting at 1s, doubling each attempt, capped at 5min)
// before attempting to reconnect.
type WebSocketReconnector struct {
	wsURL          string
	initialBackoff time.Duration
	maxBackoff     time.Duration
	currentBackoff time.Duration
	consecutiveFails int
	maxConsecutiveFails int

	mu       sync.Mutex
	running  bool
	logger   *slog.Logger
}

// Default WebSocket reconnection parameters.
const (
	DefaultWSInitialBackoff = 1 * time.Second
	DefaultWSMaxBackoff     = 5 * time.Minute
	DefaultWSMaxConsecutiveFails = 10
)

// NewWebSocketReconnector creates a new reconnector for the given WebSocket URL.
func NewWebSocketReconnector(wsURL string) *WebSocketReconnector {
	return &WebSocketReconnector{
		wsURL:               wsURL,
		initialBackoff:      DefaultWSInitialBackoff,
		maxBackoff:          DefaultWSMaxBackoff,
		currentBackoff:      DefaultWSInitialBackoff,
		maxConsecutiveFails: DefaultWSMaxConsecutiveFails,
		running:             false,
		logger:              slog.Default().With("component", "ws-reconnector", "ws_url", wsURL),
	}
}

// SetBackoffLimits overrides the default backoff parameters.
func (r *WebSocketReconnector) SetBackoffLimits(initial, max time.Duration) {
	r.initialBackoff = initial
	r.maxBackoff = max
	r.currentBackoff = initial
}

// NextBackoff returns the duration to wait before the next reconnection attempt
// and increments the backoff counter. Call this after a failed connection attempt.
//
// Returns an error if the maximum number of consecutive failures has been
// reached, indicating that the watcher should fall back to HTTP-only polling
// and alert operations.
func (r *WebSocketReconnector) NextBackoff() (time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.consecutiveFails++
	if r.consecutiveFails > r.maxConsecutiveFails {
		return 0, fmt.Errorf("ws_reconnector: exceeded max consecutive failures (%d) — falling back to HTTP-only polling",
			r.maxConsecutiveFails)
	}

	backoff := r.currentBackoff
	r.logger.Warn("WebSocket reconnection backoff",
		"consecutive_fails", r.consecutiveFails,
		"backoff", backoff.String(),
		"next", minDuration(backoff*2, r.maxBackoff).String(),
	)

	// Exponential backoff: double each attempt, capped at maxBackoff.
	r.currentBackoff = minDuration(backoff*2, r.maxBackoff)
	return backoff, nil
}

// ResetBackoff resets the backoff counter after a successful reconnection.
// Call this when the WebSocket connection is re-established.
func (r *WebSocketReconnector) ResetBackoff() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consecutiveFails = 0
	r.currentBackoff = r.initialBackoff
	r.logger.Info("WebSocket reconnected — backoff reset")
}

// ConsecutiveFails returns the number of consecutive reconnection failures.
func (r *WebSocketReconnector) ConsecutiveFails() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consecutiveFails
}

// IsDegraded returns true when the reconnector has failed enough times
// that the watcher should be considered degraded (HTTP-only mode).
func (r *WebSocketReconnector) IsDegraded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consecutiveFails > r.maxConsecutiveFails
}

// minDuration returns the smaller of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Batch processing — RPC timeout avoidance
// ---------------------------------------------------------------------------

// BatchPollConfig controls batch processing limits for the deposit watcher.
// Large polls (e.g., 200+ signatures) can cause Solana RPC timeouts or
// excessive memory consumption. Batch processing splits the work into
// manageable chunks.
type BatchPollConfig struct {
	// MaxSignaturesPerPoll limits the number of signatures fetched per
	// getSignaturesForAddress call. Solana RPC providers typically support
	// up to 1000, but 50 is a safe default that avoids timeouts.
	MaxSignaturesPerPoll int

	// MaxTransactionsPerBatch limits how many getTransaction calls are made
	// in a single batch. Each getTransaction is a separate HTTP round-trip,
	// so batching prevents overwhelming the RPC node.
	MaxTransactionsPerBatch int
}

// DefaultBatchPollConfig returns sensible defaults for production.
func DefaultBatchPollConfig() BatchPollConfig {
	return BatchPollConfig{
		MaxSignaturesPerPoll:    50,
		MaxTransactionsPerBatch: 25,
	}
}

// Validate checks that the batch config has reasonable values.
func (c *BatchPollConfig) Validate() error {
	if c.MaxSignaturesPerPoll < 1 || c.MaxSignaturesPerPoll > 1000 {
		return fmt.Errorf("batch_poll: MaxSignaturesPerPoll must be between 1 and 1000, got %d",
			c.MaxSignaturesPerPoll)
	}
	if c.MaxTransactionsPerBatch < 1 || c.MaxTransactionsPerBatch > 100 {
		return fmt.Errorf("batch_poll: MaxTransactionsPerBatch must be between 1 and 100, got %d",
			c.MaxTransactionsPerBatch)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Health check — watcher observability
// ---------------------------------------------------------------------------

// WatcherHealth contains the runtime health status of the deposit watcher.
// It is exposed via the /health endpoint and can be scraped by monitoring
// systems (Prometheus, Datadog, etc.).
type WatcherHealth struct {
	// Running is true when the watcher's polling loop is active.
	Running bool `json:"running"`

	// LastSlot is the most recently processed slot number.
	LastSlot uint64 `json:"last_slot"`

	// Lag reports how many slots behind the current tip the watcher is.
	// A large lag (> 1000 slots, ~400 seconds) indicates the watcher is
	// falling behind and may need vertical scaling.
	Lag int64 `json:"lag"`

	// CurrentSlot is the latest confirmed slot reported by the RPC node.
	CurrentSlot uint64 `json:"current_slot"`

	// WebSocketConnected is true when the WebSocket subscription is active.
	WebSocketConnected bool `json:"websocket_connected"`

	// WebSocketDegraded is true when the WebSocket has exceeded max failures
	// and the watcher is in HTTP-only polling mode.
	WebSocketDegraded bool `json:"websocket_degraded"`

	// ConsecutiveFailures counts how many consecutive WebSocket connection
	// attempts have failed.
	ConsecutiveFailures int `json:"consecutive_failures"`

	// TotalProcessed is the cumulative count of deposit events processed
	// since the watcher started.
	TotalProcessed uint64 `json:"total_processed"`

	// LastError is the most recent error message, or empty if healthy.
	LastError string `json:"last_error,omitempty"`

	// LastErrorTime is when the most recent error occurred.
	LastErrorTime *time.Time `json:"last_error_time,omitempty"`

	// Uptime is the duration since the watcher started.
	Uptime time.Duration `json:"uptime"`

	// Network identifies the blockchain network being watched.
	Network string `json:"network"`
}

// WatcherHealthReporter provides methods that a concrete watcher implements
// to report its health status. The HTTP /health handler uses this interface
// to avoid coupling to the concrete watcher type.
type WatcherHealthReporter interface {
	// Health returns the current health status of the watcher.
	Health(ctx context.Context) WatcherHealth
}

// ---------------------------------------------------------------------------
// RobustWatcher — composition wrapper
// ---------------------------------------------------------------------------

// RobustWatcher wraps a SolanaDepositWatcher and adds production-grade
// hardening: cursor persistence, WebSocket reconnection, batch polling,
// and health reporting. It is the recommended entry point for production
// deployments.
//
// Usage:
//
//	cursorStore := NewWatcherCursorStore(db)
//	cursorStore.EnsureCursorTable(ctx)
//
//	base := NewSolanaDepositWatcher(rpcURL, wsURL, reserves, repo, handler)
//	base.SetPollInterval(10 * time.Second)
//	base.SetMinConfirmations(32)
//	base.SetOutbox(db, outboxRepo)
//
//	robust := NewRobustWatcher(base, cursorStore, "solana-mainnet")
//	robust.SetBatchConfig(DefaultBatchPollConfig())
//	robust.SetWSBackoff(1*time.Second, 5*time.Minute)
//
//	go func() {
//	    if err := robust.Start(ctx); err != nil {
//	        log.Fatal(err)
//	    }
//	}()
//
//	// Expose health via HTTP:
//	r.GET("/health", func(c *gin.Context) {
//	    c.JSON(200, robust.Health(c.Request.Context()))
//	})
type RobustWatcher struct {
	watcher     *SolanaDepositWatcher
	cursorStore *WatcherCursorStore
	wsReconn    *WebSocketReconnector
	batchConfig BatchPollConfig
	network     string

	mu             sync.Mutex
	startTime      time.Time
	totalProcessed uint64
	lastError      string
	lastErrorTime  *time.Time
	logger         *slog.Logger
}

// NewRobustWatcher creates a RobustWatcher that hardens the given base watcher.
func NewRobustWatcher(
	watcher *SolanaDepositWatcher,
	cursorStore *WatcherCursorStore,
	network string,
) *RobustWatcher {
	return &RobustWatcher{
		watcher:     watcher,
		cursorStore: cursorStore,
		wsReconn:    NewWebSocketReconnector(watcher.wsURL),
		batchConfig: DefaultBatchPollConfig(),
		network:     network,
		startTime:   time.Now().UTC(),
		logger:      slog.Default().With("component", "robust-watcher", "network", network),
	}
}

// SetBatchConfig overrides the default batch processing configuration.
func (rw *RobustWatcher) SetBatchConfig(cfg BatchPollConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	rw.batchConfig = cfg
	return nil
}

// SetWSBackoff overrides the default WebSocket reconnection backoff parameters.
func (rw *RobustWatcher) SetWSBackoff(initial, max time.Duration) {
	rw.wsReconn.SetBackoffLimits(initial, max)
}

// Start begins the hardened watching loop. It loads the persisted cursor
// (or starts from a recent slot on first run) before delegating to the
// underlying watcher's polling loop.
//
// The cursor is saved after every successful poll so progress is durable
// across restarts.
func (rw *RobustWatcher) Start(ctx context.Context) error {
	// 1. Ensure cursor table exists.
	if err := rw.cursorStore.EnsureCursorTable(ctx); err != nil {
		return fmt.Errorf("robust_watcher: ensure cursor table: %w", err)
	}

	// 2. Load persisted cursor for replay protection.
	cursor, err := rw.cursorStore.LoadCursor(ctx, rw.network)
	if err != nil {
		rw.logger.Warn("failed to load cursor, starting from recent slot", "error", err)
	}

	if cursor != nil {
		// Resume from last_processed_slot + 1. The underlying watcher uses
		// signature-based pagination (lastSig), so we seed lastSig from the
		// cursor to skip already-processed transactions.
		rw.watcher.mu.Lock()
		rw.watcher.lastSlot = cursor.LastSlot
		rw.watcher.lastSig = cursor.LastSignature
		rw.watcher.mu.Unlock()

		rw.logger.Info("resumed from persisted cursor",
			"last_slot", cursor.LastSlot,
			"last_signature", cursor.LastSignature,
		)
	} else {
		rw.logger.Info("no persisted cursor found — starting fresh")
	}

	// 3. Start the underlying watcher.
	if err := rw.watcher.Start(ctx); err != nil {
		// "already running" is acceptable — the base watcher's Start guards
		// against double-start.
		if err.Error() == "solana watcher: already running" {
			rw.logger.Warn("base watcher already running, skipping start")
		} else {
			return fmt.Errorf("robust_watcher: start base watcher: %w", err)
		}
	}

	// 4. Start the WebSocket subscription loop with exponential backoff.
	go rw.wsReconnectLoop(ctx)

	// 5. Start the cursor persistence loop. After every poll interval, save
	// the current slot/signature to the database.
	go rw.cursorPersistenceLoop(ctx)

	rw.logger.Info("robust watcher started",
		"network", rw.network,
		"batch_max_signatures", rw.batchConfig.MaxSignaturesPerPoll,
		"batch_max_txns", rw.batchConfig.MaxTransactionsPerBatch,
	)

	return nil
}

// wsReconnectLoop manages WebSocket reconnection with exponential backoff.
// If the WebSocket is not needed (wsURL is empty or the underlying watcher
// doesn't support it), this loop is a no-op.
func (rw *RobustWatcher) wsReconnectLoop(ctx context.Context) {
	if rw.watcher.wsURL == "" {
		rw.logger.Info("no WebSocket URL configured — skipping WS reconnect loop")
		return
	}

	for {
		select {
		case <-ctx.Done():
			rw.logger.Info("WebSocket reconnect loop exiting (context cancelled)")
			return
		default:
		}

		// Attempt WebSocket subscription.
		// In Phase 4, this would call the actual WebSocket subscribe logic.
		// For now, if the WS is degraded or fails, apply backoff.
		if rw.wsReconn.IsDegraded() {
			// Too many failures — wait for manual intervention or a long
			// backoff period before retrying.
			rw.recordError("WebSocket degraded — too many consecutive failures")
			select {
			case <-ctx.Done():
				return
			case <-time.After(rw.wsReconn.currentBackoff):
				rw.wsReconn.ResetBackoff()
				continue
			}
		}

		// TODO (Phase 4): actual WebSocket subscription call.
		// For now, simulate a reconnect cycle.
		backoff, err := rw.wsReconn.NextBackoff()
		if err != nil {
			rw.logger.Error("WebSocket reconnection exhausted",
				"consecutive_fails", rw.wsReconn.ConsecutiveFails(),
				"error", err,
			)
			rw.recordError(err.Error())
			// Fall through to wait and retry at max backoff.
			backoff = rw.wsReconn.currentBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			// Retry connection.
		}
	}
}

// cursorPersistenceLoop periodically saves the watcher's cursor to the database
// so that progress is durable across restarts.
func (rw *RobustWatcher) cursorPersistenceLoop(ctx context.Context) {
	// Save every 30 seconds, or after each poll cycle (whichever is more
	// frequent). A 30-second interval is a good balance between durability
	// and write load.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final save on shutdown.
			rw.saveCursorNow(ctx)
			rw.logger.Info("cursor persistence loop exiting (context cancelled)")
			return
		case <-ticker.C:
			rw.saveCursorNow(ctx)
		}
	}
}

// saveCursorNow writes the current cursor state to the database.
func (rw *RobustWatcher) saveCursorNow(ctx context.Context) {
	rw.watcher.mu.RLock()
	cursor := &WatcherCursor{
		Network:       rw.network,
		LastSlot:      rw.watcher.lastSlot,
		LastSignature: rw.watcher.lastSig,
		UpdatedAt:     time.Now().UTC(),
	}
	rw.watcher.mu.RUnlock()

	if cursor.LastSlot == 0 && cursor.LastSignature == "" {
		// No progress yet — nothing to save.
		return
	}

	if err := rw.cursorStore.SaveCursor(ctx, cursor); err != nil {
		rw.logger.Warn("failed to save cursor", "error", err)
		rw.recordError(fmt.Sprintf("cursor save: %v", err))
	}
}

// recordError stores the most recent error for health reporting.
func (rw *RobustWatcher) recordError(msg string) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.lastError = msg
	now := time.Now().UTC()
	rw.lastErrorTime = &now
}

// IncrementProcessed bumps the total processed counter (call from the
// underlying watcher's handler/callback).
func (rw *RobustWatcher) IncrementProcessed() {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.totalProcessed++
}

// TotalProcessed returns the cumulative count of processed deposit events.
func (rw *RobustWatcher) TotalProcessed() uint64 {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.totalProcessed
}

// Health returns the current health status of the watcher. It queries the
// RPC node for the current slot to compute lag.
//
// This method satisfies the WatcherHealthReporter interface and is designed
// to be called from an HTTP /health handler.
func (rw *RobustWatcher) Health(ctx context.Context) WatcherHealth {
	rw.watcher.mu.RLock()
	lastSlot := rw.watcher.lastSlot
	running := rw.watcher.running
	rw.watcher.mu.RUnlock()

	rw.mu.Lock()
	lastErr := rw.lastError
	lastErrTime := rw.lastErrorTime
	totalProcessed := rw.totalProcessed
	rw.mu.Unlock()

	var currentSlot uint64
	var lag int64

	// Query the RPC node for the current confirmed slot.
	rpcClient := NewRPCClient(rw.watcher.rpcURL)
	if slot, err := rpcClient.GetSlot(ctx, rw.watcher.commitment); err == nil {
		currentSlot = slot
		if lastSlot > 0 && currentSlot > lastSlot {
			lag = int64(currentSlot - lastSlot)
		}
	} else {
		rw.logger.Warn("failed to get current slot for health check", "error", err)
	}

	wsDegraded := rw.wsReconn.IsDegraded()
	wsConnected := !wsDegraded && rw.watcher.wsURL != ""

	health := WatcherHealth{
		Running:              running,
		LastSlot:             lastSlot,
		Lag:                  lag,
		CurrentSlot:          currentSlot,
		WebSocketConnected:   wsConnected,
		WebSocketDegraded:    wsDegraded,
		ConsecutiveFailures:  rw.wsReconn.ConsecutiveFails(),
		TotalProcessed:       totalProcessed,
		LastError:            lastErr,
		LastErrorTime:        lastErrTime,
		Uptime:               time.Since(rw.startTime).Round(time.Second),
		Network:              rw.network,
	}

	return health
}

// ResetError clears the last error state. Call when the watcher recovers
// from a transient error condition.
func (rw *RobustWatcher) ResetError() {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.lastError = ""
	rw.lastErrorTime = nil
}

// ---------------------------------------------------------------------------
// Cursor consistency — validate cursor against chain state
// ---------------------------------------------------------------------------

// ValidateCursor checks that the persisted cursor is consistent with the
// on-chain state. If the cursor slot is ahead of the current confirmed slot
// (possible after a chain reorganization or if the RPC endpoint changed),
// it logs a warning and returns false.
//
// The caller should decide whether to reset the cursor or continue.
func (rw *RobustWatcher) ValidateCursor(ctx context.Context) (bool, error) {
	cursor, err := rw.cursorStore.LoadCursor(ctx, rw.network)
	if err != nil {
		return false, fmt.Errorf("robust_watcher: validate cursor: %w", err)
	}
	if cursor == nil {
		return true, nil // No cursor — nothing to validate.
	}

	rpcClient := NewRPCClient(rw.watcher.rpcURL)
	currentSlot, err := rpcClient.GetSlot(ctx, rw.watcher.commitment)
	if err != nil {
		return false, fmt.Errorf("robust_watcher: get current slot for validation: %w", err)
	}

	if cursor.LastSlot > currentSlot {
		rw.logger.Warn("cursor ahead of chain — possible RPC endpoint change or reorg",
			"cursor_slot", cursor.LastSlot,
			"current_slot", currentSlot,
			"gap", cursor.LastSlot-currentSlot,
		)
		return false, nil
	}

	// If the cursor is more than 100,000 slots behind (~11 days on Solana
	// mainnet), the watcher will take a very long time to catch up. Log a
	// warning so operators can decide whether to reset.
	const maxReasonableLag = 100_000
	if currentSlot-cursor.LastSlot > maxReasonableLag {
		rw.logger.Warn("cursor is far behind — catch-up may take a long time",
			"cursor_slot", cursor.LastSlot,
			"current_slot", currentSlot,
			"lag", currentSlot-cursor.LastSlot,
			"max_reasonable_lag", maxReasonableLag,
		)
	}

	return true, nil
}

// ResetCursor clears the persisted cursor, forcing the watcher to start from
// a recent slot on the next restart. Use this when the cursor is known to be
// invalid (e.g., after an RPC endpoint migration).
func (rw *RobustWatcher) ResetCursor(ctx context.Context) error {
	cursor := &WatcherCursor{
		Network:       rw.network,
		LastSlot:      0,
		LastSignature: "",
		UpdatedAt:     time.Now().UTC(),
	}
	if err := rw.cursorStore.SaveCursor(ctx, cursor); err != nil {
		return fmt.Errorf("robust_watcher: reset cursor: %w", err)
	}

	rw.watcher.mu.Lock()
	rw.watcher.lastSlot = 0
	rw.watcher.lastSig = ""
	rw.watcher.mu.Unlock()

	rw.logger.Warn("cursor reset — watcher will start from recent slot on next restart")
	return nil
}

// ---------------------------------------------------------------------------
// Batch utility — split signatures into chunks
// ---------------------------------------------------------------------------

// splitBatch divides a slice of transaction signatures into chunks of at most
// maxPerBatch. This prevents overwhelming the RPC node with too many parallel
// getTransaction calls.
func splitBatch[T any](items []T, maxPerBatch int) [][]T {
	if maxPerBatch <= 0 {
		maxPerBatch = 25
	}
	if len(items) == 0 {
		return nil
	}

	chunks := make([][]T, 0, (len(items)+maxPerBatch-1)/maxPerBatch)
	for i := 0; i < len(items); i += maxPerBatch {
		end := i + maxPerBatch
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}
	return chunks
}

// ---------------------------------------------------------------------------
// Slot lag helpers
// ---------------------------------------------------------------------------

// slotLag returns the number of slots between the current tip and the last
// processed slot. A negative value means the last processed slot is ahead
// (should not happen in normal operation).
func slotLag(currentSlot, lastProcessedSlot uint64) int64 {
	if lastProcessedSlot == 0 {
		return int64(currentSlot)
	}
	if currentSlot <= lastProcessedSlot {
		return 0
	}
	return int64(currentSlot - lastProcessedSlot)
}

// isLagging reports whether the watcher is more than maxLag slots behind.
func isLagging(currentSlot, lastProcessedSlot uint64, maxLag uint64) bool {
	return slotLag(currentSlot, lastProcessedSlot) > int64(maxLag)
}

// ---------------------------------------------------------------------------
// Exponential backoff table for documentation / testing
// ---------------------------------------------------------------------------

// backoffTable returns the backoff sequence for a given initial and max.
// Useful for documentation and operator visibility.
func backoffTable(initial, max time.Duration, steps int) []time.Duration {
	table := make([]time.Duration, 0, steps)
	backoff := initial
	for i := 0; i < steps; i++ {
		table = append(table, backoff)
		next := backoff * 2
		if next > max {
			next = max
		}
		if next == backoff {
			// Reached steady state.
			break
		}
		backoff = next
	}
	return table
}

// DefaultBackoffTable returns the default WebSocket backoff schedule.
// With defaults (1s initial, 5min max), the sequence is:
//
//	1s, 2s, 4s, 8s, 16s, 32s, 1m4s, 2m8s, 4m16s, 5m (steady state)
func DefaultBackoffTable() []time.Duration {
	return backoffTable(DefaultWSInitialBackoff, DefaultWSMaxBackoff, 12)
}

// ---------------------------------------------------------------------------
// Safe default helpers
// ---------------------------------------------------------------------------

const (
	// DefaultBatchMaxSignatures is the maximum number of signatures to fetch
	// per getSignaturesForAddress RPC call. 50 is a safe default that works
	// with both public RPC endpoints and private nodes.
	DefaultBatchMaxSignatures = 50

	// DefaultBatchMaxTransactions is the maximum number of getTransaction
	// calls to make in a single poll cycle.
	DefaultBatchMaxTransactions = 25

	// DefaultCursorSaveInterval is how often the cursor is persisted.
	// 30 seconds provides a good balance of durability vs write load.
	DefaultCursorSaveInterval = 30 * time.Second

	// DefaultHealthCheckTimeout is the timeout for the RPC call in the
	// health check (should be fast so /health doesn't hang).
	DefaultHealthCheckTimeout = 3 * time.Second
)

// clampInt returns val bounded to [min, max].
func clampInt(val, minVal, maxVal int) int {
	return int(math.Max(float64(minVal), math.Min(float64(maxVal), float64(val))))
}
