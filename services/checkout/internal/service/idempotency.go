package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ancf-commerce/ancf/services/checkout/internal/model"
	"github.com/ancf-commerce/ancf/services/checkout/internal/repository"
)

// IdempotencyResult represents the outcome of an idempotency pre-check.
type IdempotencyResult int

const (
	// IdempotencyNew indicates no existing key was found; the request should proceed.
	IdempotencyNew IdempotencyResult = iota
	// IdempotencyReplay indicates a matching key was found; return the cached response.
	IdempotencyReplay
	// IdempotencyConflict indicates a key exists but the request body hash differs (409).
	IdempotencyConflict
)

// IdempotencyCheckResult bundles the result of an idempotency lookup with any cached response.
type IdempotencyCheckResult struct {
	Result         IdempotencyResult
	CachedResponse *model.CommitResponse // non-nil only for IdempotencyReplay
	ConflictError  error                 // non-nil only for IdempotencyConflict
}

// CheckAndResolveIdempotency performs a full idempotency pre-check before the commit transaction.
//
// Steps:
//  1. Compute SHA-256 hash of the request body
//  2. Query idempotency_keys: SELECT status_code, response_body WHERE key = $1 AND expires_at > NOW()
//  3. If key exists and hash matches -> return cached CommitResponse (IdempotencyReplay)
//  4. If key exists but hash does NOT match -> return 409 Conflict (IdempotencyConflict)
//  5. If key does not exist -> return IdempotencyNew (proceed with transaction)
func CheckAndResolveIdempotency(ctx context.Context, repo *repository.OrderRepository, key string, req *model.CommitRequest) (*IdempotencyCheckResult, error) {
	// Compute request body hash.
	bodyJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("idempotency: failed to marshal request body: %w", err)
	}
	bodyHash := ComputeBodyHashRaw(bodyJSON)

	// Query the idempotency_keys table.
	cached, err := repo.CheckIdempotencyKey(ctx, key, bodyHash)
	if err != nil {
		// Error returned by CheckIdempotencyKey means hash mismatch (409 Conflict).
		return &IdempotencyCheckResult{
			Result:        IdempotencyConflict,
			ConflictError: fmt.Errorf("idempotency key %s was used with a different request body", key),
		}, nil
	}

	if cached != nil {
		// Key exists and hash matches -> replay cached response.
		var commitResp model.CommitResponse
		if err := json.Unmarshal([]byte(cached.ResponseBody), &commitResp); err != nil {
			return nil, fmt.Errorf("idempotency: failed to unmarshal cached response: %w", err)
		}
		return &IdempotencyCheckResult{
			Result:         IdempotencyReplay,
			CachedResponse: &commitResp,
		}, nil
	}

	// Key does not exist or has expired -> proceed with new request.
	return &IdempotencyCheckResult{
		Result: IdempotencyNew,
	}, nil
}
