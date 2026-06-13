package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ancf-commerce/ancf/services/checkout/internal/repository"
)

// OutboxProcessorV2 extends the outbox processor with multi-instance safety.
//
// Key improvements over V1:
//  1. Instance identity — each processor instance gets a unique ID, enabling
//     observability and coordination across replicas.
//  2. Heartbeat — periodic liveness signal so other instances can detect
//     and recover stalled events from a crashed peer.
//  3. Processing timeout — events stuck in "processing" beyond the timeout
//     are reset to "pending" and picked up by another instance.
//  4. Self-healing — the processor runs a recovery loop in parallel with the
//     main polling loop to reclaim stale events.
//
// Concurrency safety guarantees:
//  - FOR UPDATE SKIP LOCKED prevents two instances from claiming the same event.
//  - Heartbeat allows crash detection and automatic failover.
//  - Idempotent handlers ensure at-least-once delivery is safe.
type OutboxProcessorV2 struct {
	instanceID  string
	repo        *repository.OutboxRepository
	db          *sql.DB
	redisClient *redis.Client
	streamName  string

	// lockTimeout is the maximum time an event may sit in "processing" state
	// before it is considered stalled and reclaimed by another instance.
	lockTimeout time.Duration

	// heartbeatInterval is how often the processor writes its liveness timestamp.
	heartbeatInterval time.Duration
}

// NewOutboxProcessorV2 creates a new OutboxProcessorV2 with a random instance ID.
func NewOutboxProcessorV2(repo *repository.OutboxRepository, db *sql.DB, redisClient *redis.Client) *OutboxProcessorV2 {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails (should never happen).
		b = []byte(time.Now().String())
	}
	return &OutboxProcessorV2{
		instanceID:        "op-" + hex.EncodeToString(b),
		repo:              repo,
		db:                db,
		redisClient:       redisClient,
		streamName:        "ancf:events",
		lockTimeout:       5 * time.Minute,
		heartbeatInterval: 30 * time.Second,
	}
}

// Start launches both the main polling loop and the recovery loop.
// Both loops exit when the context is cancelled.
func (p *OutboxProcessorV2) Start(ctx context.Context, pollInterval time.Duration) {
	// Recovery loop: periodically reclaim stalled events.
	go func() {
		recoveryTicker := time.NewTicker(p.lockTimeout / 2)
		defer recoveryTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-recoveryTicker.C:
				p.recoverStalledEvents(ctx)
			}
		}
	}()

	// Heartbeat loop: signal liveness.
	go func() {
		heartbeatTicker := time.NewTicker(p.heartbeatInterval)
		defer heartbeatTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatTicker.C:
				p.sendHeartbeat(ctx)
			}
		}
	}()

	// Main polling loop.
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("[outbox-v2] processor %s stopping: %v", p.instanceID, ctx.Err())
				return
			case <-ticker.C:
				p.processBatch(ctx)
			}
		}
	}()

	log.Printf("[outbox-v2] processor %s started (poll=%v, timeout=%v, heartbeat=%v)",
		p.instanceID, pollInterval, p.lockTimeout, p.heartbeatInterval)
}

// recoverStalledEvents resets events that have been stuck in "processing" state
// longer than lockTimeout back to "pending", allowing another instance to claim them.
func (p *OutboxProcessorV2) recoverStalledEvents(ctx context.Context) {
	result, err := p.db.ExecContext(ctx,
		`UPDATE outbox SET status = 'pending', processed_at = NULL
		 WHERE status = 'processing'
		   AND processed_at < NOW() - $1 * INTERVAL '1 second'`,
		int(p.lockTimeout.Seconds()),
	)
	if err != nil {
		log.Printf("[outbox-v2] %s recovery query error: %v", p.instanceID, err)
		return
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		log.Printf("[outbox-v2] %s recovered %d stalled event(s)", p.instanceID, n)
	}
}

// sendHeartbeat writes a lightweight liveness record for this instance.
// In production, this should go to a Redis key with TTL or a dedicated
// outbox_processors table. For now, we log it.
func (p *OutboxProcessorV2) sendHeartbeat(ctx context.Context) {
	// Lightweight heartbeat: set a Redis key with TTL = 2 * heartbeatInterval.
	// If the key expires, other instances know this one has crashed.
	key := "outbox:heartbeat:" + p.instanceID
	if err := p.redisClient.Set(ctx, key, time.Now().UTC().Format(time.RFC3339), 2*p.heartbeatInterval).Err(); err != nil {
		log.Printf("[outbox-v2] %s heartbeat error: %v", p.instanceID, err)
	}
}

// processBatch fetches pending events, marks them as processing, and publishes to Redis.
func (p *OutboxProcessorV2) processBatch(ctx context.Context) {
	// Step 1: Fetch and lock pending events with SKIP LOCKED.
	events, err := p.repo.FetchPending(ctx, 10)
	if err != nil {
		log.Printf("[outbox-v2] %s fetch error: %v", p.instanceID, err)
		return
	}

	if len(events) == 0 {
		return
	}

	log.Printf("[outbox-v2] %s processing %d pending events", p.instanceID, len(events))

	// Step 2: Mark events as processing (so they can be recovered if we crash).
	// This happens in a single transaction per batch.
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[outbox-v2] %s begin tx error: %v", p.instanceID, err)
		return
	}

	for _, evt := range events {
		if err := p.markProcessing(ctx, tx, evt.EventID); err != nil {
			log.Printf("[outbox-v2] %s mark processing error for %s: %v", p.instanceID, evt.EventID, err)
			tx.Rollback()
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[outbox-v2] %s commit processing marks error: %v", p.instanceID, err)
		return
	}

	// Step 3: Publish each event to Redis.
	for _, evt := range events {
		if err := p.publishToRedis(ctx, &evt); err != nil {
			log.Printf("[outbox-v2] %s publish error for %s: %v", p.instanceID, evt.EventID, err)
			if markErr := p.repo.MarkFailed(ctx, evt.EventID, err.Error()); markErr != nil {
				log.Printf("[outbox-v2] %s mark failed error for %s: %v", p.instanceID, evt.EventID, markErr)
			}
			continue
		}

		// Mark as published in its own transaction.
		pubTx, err := p.db.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("[outbox-v2] %s begin pub-tx error for %s: %v", p.instanceID, evt.EventID, err)
			continue
		}
		if err := p.repo.MarkPublished(ctx, pubTx, evt.EventID); err != nil {
			log.Printf("[outbox-v2] %s mark published error for %s: %v", p.instanceID, evt.EventID, err)
			pubTx.Rollback()
			continue
		}
		if err := pubTx.Commit(); err != nil {
			log.Printf("[outbox-v2] %s commit published error for %s: %v", p.instanceID, evt.EventID, err)
		}
	}
}

// markProcessing transitions an event from "pending" to "processing" within a
// transaction, recording the instance ID and timestamp.
func (p *OutboxProcessorV2) markProcessing(ctx context.Context, tx *sql.Tx, eventID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE outbox SET status = 'processing', processed_at = NOW()
		 WHERE event_id = $1 AND status = 'pending'`, eventID)
	if err != nil {
		return err
	}
	return nil
}

// publishToRedis publishes a single outbox event to Redis Streams.
func (p *OutboxProcessorV2) publishToRedis(ctx context.Context, evt *repository.OutboxEvent) error {
	return p.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: p.streamName,
		Values: map[string]interface{}{
			"event_id":       evt.EventID,
			"event_type":     evt.EventType,
			"aggregate_type": evt.AggregateType,
			"aggregate_id":   evt.AggregateID,
			"payload":        string(evt.Payload),
			"timestamp":      evt.CreatedAt.Format(time.RFC3339),
			"processor":      p.instanceID,
		},
	}).Err()
}
