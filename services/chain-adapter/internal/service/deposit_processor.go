// Package service 实现 chain-adapter 的后台业务逻辑：
// 通过 Outbox 模式消费 deposit_detected 事件，并以 HTTP 方式可靠投递给
// MintService，实现链适配器与铸币服务之间的跨服务最终一致性。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
)

// DepositPayload is the deserialised payload of a deposit_detected outbox event.
type DepositPayload struct {
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

// ConfirmDepositRequest is the HTTP request body for MintService's deposit confirm endpoint.
type ConfirmDepositRequest struct {
	DepositIntentID string `json:"deposit_intent_id"`
	DepositTxID     string `json:"deposit_tx_id"`
	AmountMinor     int64  `json:"amount_minor"`
}

// DepositProcessor consumes the outbox table for deposit_detected events and
// reliably delivers them to the MintService via HTTP. It implements the
// cross-service outbox pattern for eventual consistency between chain-adapter
// (deposit detection) and mint-service (balance credit).
//
// Processing flow:
//  1. Poll outbox every 2s for pending deposit_detected events.
//  2. FOR UPDATE SKIP LOCKED to allow concurrent processor instances.
//  3. Call MintService.ConfirmDeposit (idempotent by deposit_tx_id).
//  4. On success -> MarkPublished. On failure -> retry 3 times with
//     exponential backoff (1s, 4s, 9s). Failures after 3 retries -> MarkFailed
//     and log an error for alerting.
type DepositProcessor struct {
	outboxRepo   *repository.OutboxRepository
	mintClient   *MintServiceClient
	logger       *slog.Logger
	maxRetries   int
	pollInterval time.Duration
}

// NewDepositProcessor creates a new DepositProcessor.
func NewDepositProcessor(
	outboxRepo *repository.OutboxRepository,
	mintClient *MintServiceClient,
) *DepositProcessor {
	return &DepositProcessor{
		outboxRepo:   outboxRepo,
		mintClient:   mintClient,
		logger:       slog.Default().With("component", "deposit-processor"),
		maxRetries:   3,
		pollInterval: 2 * time.Second,
	}
}

// SetPollInterval overrides the default poll interval (2s).
func (p *DepositProcessor) SetPollInterval(d time.Duration) {
	p.pollInterval = d
}

// SetMaxRetries overrides the default max retry count (3).
func (p *DepositProcessor) SetMaxRetries(n int) {
	p.maxRetries = n
}

// Start launches the background processing loop. The goroutine exits when ctx
// is cancelled. Call Start once per process lifetime.
func (p *DepositProcessor) Start(ctx context.Context) {
	p.logger.Info("starting deposit processor",
		"poll_interval", p.pollInterval.String(),
		"max_retries", p.maxRetries,
		"mint_endpoint", p.mintClient.BaseURL,
	)

	go func() {
		ticker := time.NewTicker(p.pollInterval)
		defer ticker.Stop()

		// Immediate first poll.
		p.processBatch(ctx)

		for {
			select {
			case <-ctx.Done():
				p.logger.Info("deposit processor stopped (context cancelled)")
				return
			case <-ticker.C:
				p.processBatch(ctx)
			}
		}
	}()
}

// processBatch fetches pending deposit_detected events (up to 10) and processes
// each one with retry and exponential backoff.
func (p *DepositProcessor) processBatch(ctx context.Context) {
	events, err := p.outboxRepo.FetchPending(ctx, "deposit_detected", 10)
	if err != nil {
		p.logger.Warn("failed to fetch pending outbox events", "error", err)
		return
	}

	if len(events) == 0 {
		return
	}

	p.logger.Debug("processing batch", "count", len(events))

	for _, evt := range events {
		p.processEvent(ctx, evt)
	}
}

// processEvent processes a single outbox event with retry logic.
func (p *DepositProcessor) processEvent(ctx context.Context, evt repository.OutboxEvent) {
	if evt.EventType != "deposit_detected" {
		// Only handle deposit_detected events; skip other types.
		return
	}

	var payload DepositPayload
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		p.logger.Error("failed to unmarshal deposit payload",
			"event_id", evt.EventID,
			"event_type", evt.EventType,
			"error", err,
		)
		// Unparseable payload: mark as failed immediately.
		_ = p.outboxRepo.MarkFailed(ctx, evt.EventID, fmt.Sprintf("payload unmarshal: %v", err))
		return
	}

	depositIntentID := payload.DepositIntentID
	if depositIntentID == "" {
		depositIntentID = deriveDepositIntentID(payload.TxHash, payload.FromAddress, payload.AssetSymbol)
	}

	req := ConfirmDepositRequest{
		DepositIntentID: depositIntentID,
		DepositTxID:     payload.TxHash,
		AmountMinor:     payload.AmountMinor,
	}

	var lastErr error
	for retry := 0; retry < p.maxRetries; retry++ {
		if err := p.mintClient.ConfirmDeposit(ctx, req); err == nil {
			// Success.
			_ = p.outboxRepo.MarkPublished(ctx, evt.EventID)
			p.logger.Info("deposit confirmed by mint service",
				"event_id", evt.EventID,
				"tx_hash", payload.TxHash,
				"deposit_intent_id", depositIntentID,
				"amount_minor", payload.AmountMinor,
			)
			return
		} else {
			lastErr = err
			// Exponential backoff: 1s, 4s, 9s
			backoff := time.Duration((retry+1)*(retry+1)) * time.Second
			p.logger.Warn("mint confirm deposit failed, retrying",
				"event_id", evt.EventID,
				"tx_hash", payload.TxHash,
				"retry", retry+1,
				"max_retries", p.maxRetries,
				"backoff", backoff.String(),
				"error", err,
			)
			time.Sleep(backoff)
		}
	}

	// All retries exhausted.
	_ = p.outboxRepo.MarkFailed(ctx, evt.EventID, lastErr.Error())
	p.logger.Error("deposit processing exhausted all retries",
		"event_id", evt.EventID,
		"tx_hash", payload.TxHash,
		"max_retries", p.maxRetries,
		"last_error", lastErr,
	)
}

// deriveDepositIntentID derives a deposit_intent_id from the tx_hash.
// In production, the memo attached to the on-chain transfer encodes the
// deposit_intent_id prefixed with "ancf-deposit:". For deposits without
// a memo or without a pre-created deposit intent, we derive an ID from
// the tx_hash so the mint service can match it idempotently.
func deriveDepositIntentID(txHash, wallet, assetSymbol string) string {
	// Convention: "di_" prefix + first 32 chars of tx_hash.
	// This uniquely identifiers the deposit for idempotency purposes.
	suffix := txHash
	if len(suffix) > 32 {
		suffix = suffix[:32]
	}
	return "di_" + suffix
}

// MintServiceClient is an HTTP client for calling the MintService API.
type MintServiceClient struct {
	BaseURL        string
	InternalAPIKey string
	HTTPClient     *http.Client
	logger         *slog.Logger
}

// NewMintServiceClient creates a new MintServiceClient.
func NewMintServiceClient(baseURL string, internalAPIKey string) *MintServiceClient {
	return &MintServiceClient{
		BaseURL:        strings.TrimRight(baseURL, "/"),
		InternalAPIKey: internalAPIKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: slog.Default().With("component", "mint-client"),
	}
}

// ConfirmDeposit calls POST /api/v1/internal/deposit-confirm on the MintService.
// The MintService.ConfirmDeposit is idempotent by deposit_tx_id, so repeated
// calls for the same deposit are safe.
func (c *MintServiceClient) ConfirmDeposit(ctx context.Context, req ConfirmDepositRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("mint client: marshal request: %w", err)
	}

	url := c.BaseURL + "/api/v1/internal/deposit-confirm"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("mint client: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.InternalAPIKey != "" {
		httpReq.Header.Set("X-Internal-API-Key", c.InternalAPIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("mint client: post %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mint client: %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	return nil
}
