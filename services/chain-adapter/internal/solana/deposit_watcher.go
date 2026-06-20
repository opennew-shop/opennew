package solana

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/model"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
)

// // DepositHandler is the callback invoked when a new deposit is detected.
// type DepositHandler func(ctx context.Context, event *model.DepositEvent) error

// SolanaDepositWatcher monitors the Solana blockchain for incoming USDC
// deposits to the platform's reserve addresses. It combines WebSocket
// subscriptions (for real-time updates) with periodic HTTP polling (for
// reliability and catch-up on missed slots).
//
// Deposit detection flow:
//
//  1. On start, determine the starting slot (last processed + 1, or recent).
//  2. For each configured reserve address, poll getSignaturesForAddress
//     at the configured interval.
//  3. For each new signature, fetch the parsed transaction via getTransaction.
//  4. Decode SPL Token transfer instructions whose destination is a reserve
//     address and whose mint is a recognized asset (USDC).
//  5. Build a DepositEvent and invoke the handler callback.
//  6. Persist the event via ChainRepository (deduplicated by tx_hash).
//  7. Update the last processed slot cursor.
//
// The watcher uses a WebSocket connection to the RPC endpoint's
// `logsSubscribe` or `accountSubscribe` for near-real-time detection, with
// HTTP polling as a fallback every PollInterval to ensure no transactions
// are missed during WebSocket disconnects.
type SolanaDepositWatcher struct {
	rpcURL           string
	wsURL            string
	reserveAddresses map[string]string // assetSymbol -> Solana public key
	assetMints       map[string]string // assetSymbol -> expected SPL mint address (whitelist)
	handler          DepositHandler
	chainRepo        *repository.ChainRepository
	pollInterval     time.Duration
	commitment       string // "confirmed" or "finalized"
	minConfirmations uint64 // minimum confirmations before processing (default 32)

	// Outbox support: when db and outboxRepo are set, processEvent writes
	// deposit_detected events within the same DB transaction as the chain_tx save.
	db         *sql.DB
	outboxRepo *repository.OutboxRepository

	mu       sync.RWMutex
	lastSlot uint64
	lastSig  string // before cursor for getSignaturesForAddress pagination
	running  bool
	cancelFn context.CancelFunc
	logger   *slog.Logger
}

// DepositHandler is the callback invoked when a new deposit is detected on-chain.
// The handler should be idempotent — the watcher may fire more than once for
// the same deposit (e.g. during WebSocket reconnection).
type DepositHandler func(ctx context.Context, event *model.DepositEvent) error

// NewSolanaDepositWatcher creates a new SolanaDepositWatcher.
//
// Parameters:
//   - rpcURL: Solana HTTP RPC endpoint (e.g. https://api.mainnet-beta.solana.com)
//   - wsURL: Solana WebSocket RPC endpoint (e.g. wss://api.mainnet-beta.solana.com)
//   - reserveAddresses: map of asset symbol to Solana reserve wallet address
//   - chainRepo: repository for persisting processed transactions
//   - handler: callback invoked for each detected deposit
func NewSolanaDepositWatcher(
	rpcURL string,
	wsURL string,
	reserveAddresses map[string]string,
	chainRepo *repository.ChainRepository,
	handler DepositHandler,
) *SolanaDepositWatcher {
	return &SolanaDepositWatcher{
		rpcURL:           rpcURL,
		wsURL:            wsURL,
		reserveAddresses: reserveAddresses,
		assetMints: map[string]string{
			"USDC":  USDCMainnetMint,
			"vUSDC": USDCMainnetMint,
		},
		handler:          handler,
		chainRepo:        chainRepo,
		pollInterval:     10 * time.Second,
		commitment:       "confirmed",
		minConfirmations: 32,
		lastSlot:         0,
		lastSig:          "",
		running:          false,
		logger:           slog.Default().With("component", "solana-watcher", "rpc", rpcURL),
	}
}

// SetPollInterval overrides the default poll interval (10s).
func (w *SolanaDepositWatcher) SetPollInterval(d time.Duration) {
	w.pollInterval = d
}

// SetOutbox enables outbox event publishing for the Solana deposit watcher.
// Call after construction, before Start.
func (w *SolanaDepositWatcher) SetOutbox(db *sql.DB, outboxRepo *repository.OutboxRepository) {
	w.db = db
	w.outboxRepo = outboxRepo
}

// SetMinConfirmations sets the minimum number of confirmations required before
// a deposit is considered final and the handler is invoked.
func (w *SolanaDepositWatcher) SetMinConfirmations(n uint64) {
	w.minConfirmations = n
}

// SetAssetMints replaces the mint whitelist used during deposit decoding.
// The map key is the asset symbol configured for the reserve account, and the
// value is the authoritative SPL/Token-2022 mint address expected on-chain.
func (w *SolanaDepositWatcher) SetAssetMints(assetMints map[string]string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	copied := make(map[string]string, len(assetMints))
	for symbol, mint := range assetMints {
		copied[symbol] = mint
	}
	w.assetMints = copied
}

// IsRunning reports whether the watcher loop is active.
func (w *SolanaDepositWatcher) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

// LastSlot returns the most recently processed slot number.
func (w *SolanaDepositWatcher) LastSlot() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastSlot
}

// ProcessDepositEvent exposes the same persistence path used by live polling.
// It is used only by the development simulator when explicitly enabled.
func (w *SolanaDepositWatcher) ProcessDepositEvent(ctx context.Context, event *model.DepositEvent) error {
	return w.processEvent(ctx, event)
}

// Start begins the deposit watching loop. It initializes the starting slot
// (from chain state or recent confirmed slot), then starts both the HTTP
// polling loop and the WebSocket subscription loop in background goroutines.
//
// The goroutines exit when ctx is cancelled. Call Stop() to gracefully shut
// down (it cancels the internal context and waits for goroutines to exit).
func (w *SolanaDepositWatcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return fmt.Errorf("solana watcher: already running")
	}
	w.running = true

	ctx, cancel := context.WithCancel(ctx)
	w.cancelFn = cancel
	w.mu.Unlock()

	w.logger.Info("starting Solana deposit watcher",
		"reserve_addresses", len(w.reserveAddresses),
		"poll_interval", w.pollInterval.String(),
		"min_confirmations", w.minConfirmations,
		"commitment", w.commitment,
	)

	// Determine starting slot.
	rpcClient := NewRPCClient(w.rpcURL)
	slot, err := rpcClient.GetSlot(ctx, w.commitment)
	if err != nil {
		w.logger.Error("failed to get current slot — starting from 0", "error", err)
		slot = 0
	}
	w.mu.Lock()
	if w.lastSlot == 0 {
		// Start from slightly behind current to catch recent transactions.
		if slot > 200 {
			w.lastSlot = slot - 200
		} else {
			w.lastSlot = 0
		}
	}
	w.mu.Unlock()

	w.logger.Info("starting slot determined", "current_slot", slot, "start_from", w.lastSlot)

	// Start HTTP polling goroutine.
	go w.pollLoop(ctx, rpcClient)

	// Start WebSocket subscription goroutine (Phase 4: deferred).
	// go w.wsSubscribeLoop(ctx)

	return nil
}

// pollLoop periodically polls getSignaturesForAddress and processes new
// transactions. It runs until ctx is cancelled.
func (w *SolanaDepositWatcher) pollLoop(ctx context.Context, rpcClient *RPCClient) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	// Immediate first poll.
	w.poll(ctx, rpcClient)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("poll loop exiting (context cancelled)")
			return
		case <-ticker.C:
			w.poll(ctx, rpcClient)
		}
	}
}

// poll performs a single polling cycle across all reserve addresses.
func (w *SolanaDepositWatcher) poll(ctx context.Context, rpcClient *RPCClient) {
	// Build reverse lookup: Solana address -> asset symbol.
	addrToSymbol := make(map[string]string, len(w.reserveAddresses))
	for symbol, addr := range w.reserveAddresses {
		addrToSymbol[addr] = symbol
	}

	for assetSymbol, reserveAddr := range w.reserveAddresses {
		sigs, err := rpcClient.GetSignaturesForAddress(ctx, reserveAddr, 20, w.lastSig)
		if err != nil {
			w.logger.Warn("getSignaturesForAddress failed",
				"address", reserveAddr,
				"asset", assetSymbol,
				"error", err,
			)
			continue
		}

		for _, sig := range sigs {
			// Skip if already processed.
			existing, err := w.chainRepo.GetByTxHash(ctx, string(model.NetworkSolanaMainnet), sig.Signature)
			if err != nil {
				w.logger.Warn("failed to check tx existence",
					"sig", sig.Signature,
					"error", err,
				)
				continue
			}
			if existing != nil {
				// Update lastSig to advance the cursor.
				w.mu.Lock()
				w.lastSig = sig.Signature
				w.mu.Unlock()
				continue
			}

			// Skip if tx has an error (failed transactions).
			if sig.Err != nil {
				w.logger.Debug("skipping failed transaction",
					"sig", sig.Signature,
				)
				continue
			}

			// Fetch the full parsed transaction.
			parsedTx, err := rpcClient.GetTransaction(ctx, sig.Signature)
			if err != nil {
				w.logger.Warn("getTransaction failed",
					"sig", sig.Signature,
					"error", err,
				)
				continue
			}
			if parsedTx.Meta != nil && parsedTx.Meta.Err != nil {
				w.logger.Debug("skipping transaction with meta error",
					"sig", sig.Signature,
				)
				continue
			}

			// Decode SPL Token transfers targeting our reserve address.
			event := w.decodeSPLTransfer(parsedTx, sig.Signature, sig.Memo, reserveAddr, assetSymbol)
			if event == nil {
				continue
			}

			// Wait for minimum confirmations.
			currentSlot, slotErr := rpcClient.GetSlot(ctx, w.commitment)
			// M-01 FIX: guard against unsigned underflow when currentSlot < BlockNumber
			// (RPC node jitter/rollback). Underflow would wrap to ~2^64 and bypass minConfirmations.
			if slotErr != nil || currentSlot == 0 || currentSlot < uint64(event.BlockNumber) {
				continue // slot unavailable or anomalous — retry next poll
			}
			if (currentSlot - uint64(event.BlockNumber)) < w.minConfirmations {
				w.logger.Debug("deposit tx has insufficient confirmations",
					"sig", event.TxHash,
					"slot", event.BlockNumber,
					"current_slot", currentSlot,
					"confirmations", currentSlot-uint64(event.BlockNumber),
					"required", w.minConfirmations,
				)
				// Leave lastSig unchanged so we re-process this on the next poll.
				// Note: this means the caller must handle deduplication via the
				// ChainRepository (GetByTxHash checks for existing).
				continue
			}
			event.Confirmations = int(currentSlot - uint64(event.BlockNumber))

			// Process the event.
			if err := w.processEvent(ctx, event); err != nil {
				w.logger.Error("failed to process deposit event",
					"sig", event.TxHash,
					"error", err,
				)
			}

			// Advance cursor.
			if sig.Slot > w.lastSlot {
				w.mu.Lock()
				w.lastSlot = sig.Slot
				w.lastSig = sig.Signature
				w.mu.Unlock()
			}
		}
	}
}

// decodeSPLTransfer inspects a parsed Solana transaction for SPL Token
// transfers where the destination is one of our reserve addresses and the
// mint is a recognized asset (USDC).
//
// It examines both top-level and inner instructions for SPL Token transfer
// instructions (program: "spl-token", type: "transfer").
//
// Returns nil if no relevant transfer is found.
func (w *SolanaDepositWatcher) decodeSPLTransfer(
	tx *ParsedTransaction,
	txSig string,
	memo *string,
	reserveAddr string,
	assetSymbol string,
) *model.DepositEvent {
	if tx.Meta == nil {
		return nil
	}

	// The SPL Token program ID for detecting transfer instructions.
	// We check both Token and Token-2022 program IDs.
	// splTokenPrograms := []string{TokenProgramID, Token2022ProgramID}

	// Build a map from account index to public key for resolving token balances.
	accountKeys := make(map[int]string)
	if tx.Transaction != nil {
		for i, ak := range tx.Transaction.AccountKeys {
			accountKeys[i] = ak.Pubkey
		}
	}

	// Extract USDC transfers from post-token-balances changes.
	// A SPL Token transfer to our reserve address will show:
	//   - postTokenBalances: reserve address owns tokens of mint=USDC
	//   - A matching preTokenBalances entry showing a lower amount
	//
	// We iterate post-token-balances to find entries where:
	//   - The owner is the reserve address
	//   - The mint is USDC
	//   - The post balance > pre balance
	if tx.Meta.PostTokenBalances != nil && tx.Meta.PreTokenBalances != nil {
		for _, postBal := range tx.Meta.PostTokenBalances {
			owner := postBal.Owner
			_ = accountKeys // available for cross-referencing

			if owner != reserveAddr {
				continue
			}

			// S-01 FIX: validate token mint against whitelist — reject any non-USDC token.
			// Without this, an attacker can deposit a worthless self-minted SPL token
			// to the reserve address and have it credited as real vUSDC.
			expectedMint, ok := w.assetMints[assetSymbol]
			if !ok || expectedMint == "" {
				w.logger.Warn("no mint whitelist configured for asset; rejecting", "asset", assetSymbol)
				continue
			}
			if postBal.Mint != expectedMint {
				w.logger.Warn("rejecting deposit: token mint not in whitelist",
					"got_mint", postBal.Mint, "expected_mint", expectedMint, "asset", assetSymbol)
				continue
			}

			// This token balance entry is for the reserve address.
			// Find the pre-balance for the same account index.
			var preAmount uint64
			for _, preBal := range tx.Meta.PreTokenBalances {
				if preBal.AccountIndex == postBal.AccountIndex &&
					preBal.Mint == postBal.Mint {
					if _, err := fmt.Sscanf(preBal.UITokenAmount.Amount, "%d", &preAmount); err != nil {
						preAmount = 0
					}
					break
				}
			}

			var postAmount uint64
			if _, err := fmt.Sscanf(postBal.UITokenAmount.Amount, "%d", &postAmount); err != nil {
				continue
			}

			if postAmount <= preAmount {
				continue // no net increase
			}

			amount := postAmount - preAmount
			if amount == 0 {
				continue
			}

			// Extract the sender from pre-token-balances.
			// The sender is the wallet previously holding these tokens.
			fromAddress := ""
			for _, preBal := range tx.Meta.PreTokenBalances {
				if preBal.AccountIndex == postBal.AccountIndex &&
					preBal.Mint == postBal.Mint {
					fromAddress = preBal.Owner
					break
				}
			}
			if fromAddress == "" {
				fromAddress = "unknown"
			}

			blockTime := int64(0)
			if tx.BlockTime != nil {
				blockTime = *tx.BlockTime
			}

			eventTime := time.Unix(blockTime, 0).UTC()
			if blockTime == 0 {
				eventTime = time.Now().UTC()
			}

			return &model.DepositEvent{
				Network:         string(model.NetworkSolanaMainnet),
				TxHash:          txSig,
				FromAddress:     fromAddress,
				ToAddress:       reserveAddr,
				AmountMinor:     int64(amount),
				AssetSymbol:     assetSymbol,
				MintAddress:     postBal.Mint,
				DepositIntentID: parseDepositIntentMemo(memo, assetSymbol),
				BlockNumber:     int64(tx.Slot),
				Timestamp:       eventTime,
			}
		}
	}

	return nil
}

// parseDepositIntentMemo 从链上转账的 memo 字段解析 deposit_intent_id。
// 约定格式为 "ancf-deposit:<assetSymbol>:di_xxx"，仅当三段格式、资产符号匹配
// 且第三段以 "di_" 前缀开头时才返回该 ID，否则返回空串。
func parseDepositIntentMemo(memo *string, assetSymbol string) string {
	if memo == nil {
		return ""
	}
	parts := strings.Split(*memo, ":")
	if len(parts) != 3 || parts[0] != "ancf-deposit" || parts[1] != assetSymbol {
		return ""
	}
	if !strings.HasPrefix(parts[2], "di_") {
		return ""
	}
	return parts[2]
}

// processEvent persists the deposit event to the database and invokes the
// handler callback. When outbox support is configured (db and outboxRepo are set),
// the chain_tx save and outbox event insert occur within the same DB transaction,
// ensuring exactly-once delivery of deposit_detected events to the DepositProcessor.
func (w *SolanaDepositWatcher) processEvent(ctx context.Context, event *model.DepositEvent) error {
	// 1. Deduplication check.
	existing, err := w.chainRepo.GetByTxHash(ctx, event.Network, event.TxHash)
	if err != nil {
		return fmt.Errorf("solana watcher: dedup check for %s: %w", event.TxHash, err)
	}
	if existing != nil {
		w.logger.Debug("deposit tx already processed, skipping",
			"tx_hash", event.TxHash,
			"status", existing.Status,
		)
		return nil
	}

	// 2. Persist to chain_txs, with outbox event in same transaction when configured.
	rawJSON, _ := json.Marshal(event)
	chainTx := &model.ChainTx{
		Network:       string(model.NetworkSolanaMainnet),
		TxHash:        event.TxHash,
		TxType:        model.TxTypeDeposit,
		Status:        model.TxStatusFinalized,
		Confirmations: event.Confirmations,
		RawJSON:       rawJSON,
	}
	if chainTx.Confirmations <= 0 {
		chainTx.Confirmations = int(w.minConfirmations)
	}

	if w.db != nil && w.outboxRepo != nil {
		// Outbox pattern: save chain_tx and outbox event in one transaction.
		dbTx, txErr := w.db.BeginTx(ctx, nil)
		if txErr != nil {
			return fmt.Errorf("solana watcher: begin tx for %s: %w", event.TxHash, txErr)
		}
		defer dbTx.Rollback()

		if err := w.chainRepo.SaveChainTxWithTx(ctx, dbTx, chainTx); err != nil {
			if errors.Is(err, repository.ErrDuplicateChainTx) {
				return nil
			}
			w.logger.Error("failed to save chain tx (with tx)", "tx_hash", event.TxHash, "error", err)
			return fmt.Errorf("solana watcher: save chain tx %s: %w", event.TxHash, err)
		}

		if err := w.chainRepo.IncrementReserveConfirmedWithTx(ctx, dbTx, event.Network, event.AssetSymbol, event.AmountMinor); err != nil {
			w.logger.Error("failed to increment reserve balance", "tx_hash", event.TxHash, "error", err)
			return fmt.Errorf("solana watcher: increment reserve %s: %w", event.TxHash, err)
		}

		outboxEvent := &repository.OutboxEvent{
			EventID:       generateSolOutboxID("evt_"),
			EventType:     "deposit_detected",
			AggregateType: "chain_deposit",
			AggregateID:   event.TxHash,
			Payload:       marshalOutboxPayload(event),
		}
		if err := w.outboxRepo.InsertWithTx(ctx, dbTx, outboxEvent); err != nil {
			w.logger.Error("failed to insert outbox event", "tx_hash", event.TxHash, "error", err)
			return fmt.Errorf("solana watcher: outbox insert %s: %w", event.TxHash, err)
		}

		if err := dbTx.Commit(); err != nil {
			return fmt.Errorf("solana watcher: commit chain_tx + outbox %s: %w", event.TxHash, err)
		}
	} else {
		if err := w.chainRepo.SaveChainTx(ctx, chainTx); err != nil {
			w.logger.Error("failed to save chain tx", "tx_hash", event.TxHash, "error", err)
			return fmt.Errorf("solana watcher: save chain tx %s: %w", event.TxHash, err)
		}
	}

	// 3. Invoke the handler callback (after commit so outbox event is visible).
	if w.handler != nil {
		if err := w.handler(ctx, event); err != nil {
			w.logger.Error("deposit handler failed",
				"tx_hash", event.TxHash,
				"error", err,
			)
			// Do NOT return error — the tx + outbox are already persisted.
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

// generateSolOutboxID creates a random hex-encoded ID with the given prefix.
func generateSolOutboxID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// marshalOutboxPayload builds the JSON payload for a deposit_detected outbox event.
func marshalOutboxPayload(event *model.DepositEvent) []byte {
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

// Stop gracefully stops the watcher and waits for goroutines to exit.
func (w *SolanaDepositWatcher) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	if w.cancelFn != nil {
		w.cancelFn()
	}
	w.mu.Unlock()

	w.logger.Info("Solana deposit watcher stopped")
}
