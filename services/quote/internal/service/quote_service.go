// Package service 提供报价生成与校验的业务逻辑。
// 报价默认 5 分钟有效,校验涵盖存在性、过期、已消费与钱包绑定,
// 并通过事务内 SELECT FOR UPDATE 实现原子消费。
package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	catalogRepo "github.com/ancf-commerce/ancf/services/catalog/internal/repository"
	"github.com/ancf-commerce/ancf/services/quote/internal/model"
	"github.com/ancf-commerce/ancf/services/quote/internal/repository"
)

// QuoteService provides business logic for quote generation and validation.
type QuoteService struct {
	repo          *repository.QuoteRepository
	skuRepo       *catalogRepo.SKURepository
	quoteValidity time.Duration
}

// NewQuoteService creates a new QuoteService.
// quoteValidity defaults to 5 minutes if not explicitly configured.
func NewQuoteService(repo *repository.QuoteRepository, skuRepo *catalogRepo.SKURepository) *QuoteService {
	return &QuoteService{
		repo:          repo,
		skuRepo:       skuRepo,
		quoteValidity: 5 * time.Minute,
	}
}

// GenerateQuote creates a server-authoritative price quote for the given request.
//
// Steps:
//  1. Validates wallet and network parameters are present
//  2. Looks up each SKU via the catalog repository and verifies it is active
//  3. Computes line totals and the aggregate total_minor
//  4. Generates a unique quote_id with 128 bits of entropy
//  5. Sets expires_at to now + quoteValidity (default 5 minutes)
//  6. Persists the quote to the database
//  7. Returns a QuoteResponse ready for the API layer
func (s *QuoteService) GenerateQuote(ctx context.Context, req *model.QuoteRequest) (*model.QuoteResponse, error) {
	// Validate required fields.
	if req.Wallet == "" {
		return nil, fmt.Errorf("wallet is required")
	}
	if req.Network == "" {
		return nil, fmt.Errorf("network is required")
	}
	if len(req.Lines) == 0 {
		return nil, fmt.Errorf("at least one line item is required")
	}

	// Generate unique quote_id: "quote_" + 16 bytes random hex (32 chars).
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate quote id: %w", err)
	}
	quoteID := fmt.Sprintf("quote_%s", hex.EncodeToString(b))

	now := time.Now().UTC()
	expiresAt := now.Add(s.quoteValidity)

	// Resolve SKU prices and build quote lines.
	var lines []model.QuoteLine
	var totalMinor int64

	for _, line := range req.Lines {
		if line.SkuID == "" {
			return nil, fmt.Errorf("sku_id is required for each line")
		}
		if line.Quantity <= 0 {
			return nil, fmt.Errorf("quantity must be positive for sku %s", line.SkuID)
		}

		sku, err := s.skuRepo.GetBySKUID(ctx, line.SkuID)
		if err != nil {
			return nil, fmt.Errorf("failed to look up sku %s: %w", line.SkuID, err)
		}
		if sku == nil {
			return nil, fmt.Errorf("sku %s not found or is not active", line.SkuID)
		}

		lineTotal := sku.PriceAmountMinor * int64(line.Quantity)

		lines = append(lines, model.QuoteLine{
			SkuID:          line.SkuID,
			Quantity:       line.Quantity,
			UnitPriceMinor: strconv.FormatInt(sku.PriceAmountMinor, 10),
			LineTotalMinor: strconv.FormatInt(lineTotal, 10),
		})
		totalMinor += lineTotal
	}

	// Marshal lines to JSON for storage.
	linesJSON, err := json.Marshal(lines)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal quote lines: %w", err)
	}

	// Persist quote to database.
	quote := &model.Quote{
		QuoteID:    quoteID,
		Wallet:     req.Wallet,
		Network:    req.Network,
		Currency:   "vUSDC",
		TotalMinor: totalMinor,
		Scale:      6,
		ExpiresAt:  expiresAt,
		Consumed:   false,
		Lines:      linesJSON,
		CreatedAt:  now,
	}

	if err := s.repo.Create(ctx, quote); err != nil {
		return nil, fmt.Errorf("failed to persist quote: %w", err)
	}

	// Build response with lines as JSON raw message for the API layer.
	linesRaw := json.RawMessage(linesJSON)

	return &model.QuoteResponse{
		QuoteID:    quoteID,
		Currency:   "vUSDC",
		TotalMinor: strconv.FormatInt(totalMinor, 10),
		Scale:      6,
		ExpiresAt:  expiresAt,
		Lines:      linesRaw,
	}, nil
}

// MarkQuoteConsumed atomically marks a quote as consumed.
// This prevents the quote from being reused after checkout commit.
// Returns true if the quote was successfully marked consumed.
// Returns false if the quote was already consumed or does not exist.
func (s *QuoteService) MarkQuoteConsumed(ctx context.Context, quoteID string) (bool, error) {
	return s.repo.MarkConsumed(ctx, quoteID)
}

// ValidateQuote checks whether a quote is valid for use in checkout.
//
// It verifies:
//   - The quote exists in the database
//   - The quote has not expired
//   - The quote has not been consumed
//   - The wallet address matches (if wallet is non-empty)
//
// Returns the quote if valid, or an error describing why it is invalid.
func (s *QuoteService) ValidateQuote(ctx context.Context, quoteID string, wallet string) (*model.Quote, error) {
	q, err := s.repo.GetByQuoteID(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve quote %s: %w", quoteID, err)
	}
	if q == nil {
		return nil, fmt.Errorf("quote %s not found", quoteID)
	}

	// Check expiration.
	expired, err := s.repo.IsExpired(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("failed to check quote expiration: %w", err)
	}
	if expired {
		return nil, fmt.Errorf("quote %s has expired", quoteID)
	}

	// Check consumption status.
	if q.Consumed {
		return nil, fmt.Errorf("quote %s has already been consumed", quoteID)
	}

	// Verify wallet binding if provided.
	if wallet != "" && q.Wallet != wallet {
		return nil, fmt.Errorf("wallet %s does not match quote wallet %s", wallet, q.Wallet)
	}

	return q, nil
}

// MarkQuoteConsumedTx marks a quote as consumed within a database transaction.
// This is the transactional counterpart of MarkQuoteConsumed, used inside the
// checkout commit transaction to ensure atomicity with order status update.
func (s *QuoteService) MarkQuoteConsumedTx(ctx context.Context, tx *sql.Tx, quoteID string) (bool, error) {
	return s.repo.MarkConsumedWithTx(ctx, tx, quoteID)
}

// LockAndValidateQuoteTx locks a quote row (SELECT FOR UPDATE) and validates it within a transaction.
//
// It verifies:
//   - The quote exists
//   - The quote has not expired
//   - The quote has not been consumed
//   - The wallet address matches (if wallet is non-empty)
//
// The SELECT FOR UPDATE lock prevents concurrent consumption.
// Returns the locked quote if valid, or an error.
func (s *QuoteService) LockAndValidateQuoteTx(ctx context.Context, tx *sql.Tx, quoteID string, wallet string) (*model.Quote, error) {
	q, err := s.repo.LockQuoteForUpdate(ctx, tx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("failed to lock quote %s: %w", quoteID, err)
	}
	if q == nil {
		return nil, fmt.Errorf("quote %s not found", quoteID)
	}

	// Check expiration within the lock.
	if q.ExpiresAt.Before(time.Now().UTC()) {
		return nil, fmt.Errorf("quote %s has expired", quoteID)
	}

	// Check consumption status within the lock.
	if q.Consumed {
		return nil, fmt.Errorf("quote %s has already been consumed", quoteID)
	}

	// Verify wallet binding.
	if wallet != "" && q.Wallet != wallet {
		return nil, fmt.Errorf("wallet %s does not match quote wallet %s", wallet, q.Wallet)
	}

	return q, nil
}
