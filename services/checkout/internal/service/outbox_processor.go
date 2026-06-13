package service

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ancf-commerce/ancf/services/checkout/internal/repository"
)

// OutboxProcessor polls the outbox table for pending events and publishes them
// to Redis Streams. It implements the transactional outbox pattern:
// events are inserted in the checkout commit transaction and become visible
// to the processor only after the transaction commits.
type OutboxProcessor struct {
	repo        *repository.OutboxRepository
	db          *sql.DB
	redisClient *redis.Client
	streamName  string
}

// NewOutboxProcessor creates a new OutboxProcessor.
// repo is the OutboxRepository for database operations.
// db is the *sql.DB connection used to begin transactions for marking events as published.
// redisClient is the Redis client for publishing to streams.
// streamName is the Redis stream key (default: "ancf:events").
func NewOutboxProcessor(repo *repository.OutboxRepository, db *sql.DB, redisClient *redis.Client) *OutboxProcessor {
	return &OutboxProcessor{
		repo:        repo,
		db:          db,
		redisClient: redisClient,
		streamName:  "ancf:events",
	}
}

// Start launches the outbox processing loop in a background goroutine.
// It polls for pending events at the given interval and publishes them to Redis Streams.
// The loop exits when the context is cancelled.
func (p *OutboxProcessor) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("[outbox] processor stopping: %v", ctx.Err())
				return
			case <-ticker.C:
				p.processBatch(ctx)
			}
		}
	}()
	log.Printf("[outbox] processor started with interval %v", interval)
}

// processBatch fetches pending outbox events and publishes each to Redis Streams.
// It uses FOR UPDATE SKIP LOCKED to allow concurrent processor instances.
// Events that fail to publish are marked as 'failed'; successful ones are marked 'published'.
func (p *OutboxProcessor) processBatch(ctx context.Context) {
	events, err := p.repo.FetchPending(ctx, 10)
	if err != nil {
		log.Printf("[outbox] fetch error: %v", err)
		return
	}

	if len(events) == 0 {
		return
	}

	log.Printf("[outbox] processing %d pending events", len(events))

	for _, evt := range events {
		if err := p.publishToRedis(ctx, &evt); err != nil {
			log.Printf("[outbox] publish error for event %s: %v", evt.EventID, err)
			if markErr := p.repo.MarkFailed(ctx, evt.EventID, err.Error()); markErr != nil {
				log.Printf("[outbox] mark failed error for event %s: %v", evt.EventID, markErr)
			}
			continue
		}

		// Mark the event as published within its own transaction.
		tx, err := p.db.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("[outbox] begin tx error for event %s: %v", evt.EventID, err)
			continue
		}
		if err := p.repo.MarkPublished(ctx, tx, evt.EventID); err != nil {
			log.Printf("[outbox] mark published error for event %s: %v", evt.EventID, err)
			tx.Rollback()
			continue
		}
		if err := tx.Commit(); err != nil {
			log.Printf("[outbox] commit error for event %s: %v", evt.EventID, err)
		}
	}
}

// publishToRedis publishes a single outbox event to the Redis Streams.
// The event is published as an XADD to the configured stream with all fields
// as key-value pairs.
func (p *OutboxProcessor) publishToRedis(ctx context.Context, evt *repository.OutboxEvent) error {
	return p.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: p.streamName,
		Values: map[string]interface{}{
			"event_id":       evt.EventID,
			"event_type":     evt.EventType,
			"aggregate_type": evt.AggregateType,
			"aggregate_id":   evt.AggregateID,
			"payload":        string(evt.Payload),
			"timestamp":      evt.CreatedAt.Format(time.RFC3339),
		},
	}).Err()
}
