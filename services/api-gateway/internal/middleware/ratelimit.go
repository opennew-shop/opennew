package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/ancf-commerce/ancf/services/api-gateway/internal/config"
)

// rateLimitPrefix 是 Redis 中限流计数键的统一前缀；
// rateLimitScript 是在 Redis 端原子执行的令牌桶 Lua 脚本：按经过时间补充令牌，
// 消费一个令牌，超额时返回需等待的 TTL。
const (
	rateLimitPrefix = "ratelimit:"
	rateLimitScript = `
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local window = 1.0

-- Get current bucket state
local data = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(data[1])
local last_refill = tonumber(data[2])

if tokens == nil then
    -- First request: initialize bucket
    tokens = burst
    last_refill = now
end

-- Calculate tokens to add since last refill
local elapsed = now - last_refill
local new_tokens = math.min(burst, tokens + elapsed * rate)
new_tokens = new_tokens - 1

if new_tokens < 0 then
    -- Rate limit exceeded
    local ttl = math.ceil((1 - new_tokens) / rate)
    redis.call('HMSET', key, 'tokens', tokens, 'last_refill', last_refill)
    if ttl > 0 then
        redis.call('EXPIRE', key, ttl + 1)
    end
    return {0, ttl}
end

-- Allow request
redis.call('HMSET', key, 'tokens', new_tokens, 'last_refill', now)
redis.call('EXPIRE', key, math.ceil(burst / rate) + 5)
return {1, burst - math.floor(new_tokens)}
`
)

// RateLimit returns a Gin middleware that implements token bucket rate limiting.
// Uses Redis if available, otherwise falls back to in-memory rate limiting.
// Limits are enforced per client IP or API key.
// Headers set on response: X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset.
func RateLimit(cfg *config.Config, redisClient *redis.Client) gin.HandlerFunc {
	if !cfg.RateLimit.Enabled {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	// Pre-load Lua script if Redis is available
	var scriptSHA string
	if redisClient != nil {
		sha, err := redisClient.ScriptLoad(context.Background(), rateLimitScript).Result()
		if err == nil {
			scriptSHA = sha
		}
	}

	return func(c *gin.Context) {
		// Determine rate limit key: by X-API-Key if present, otherwise by client IP
		keyIdentifier := c.ClientIP()
		if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
			keyIdentifier = apiKey
		}
		rateLimitKey := fmt.Sprintf("%s%s:%s", rateLimitPrefix, c.Request.URL.Path, keyIdentifier)

		var allowed bool
		var remaining int
		var resetAfter int

		if redisClient != nil && scriptSHA != "" {
			// Use Redis token bucket
			now := time.Now().Unix()
			res, err := redisClient.EvalSha(c.Request.Context(), scriptSHA,
				[]string{rateLimitKey},
				cfg.RateLimit.Rate,
				cfg.RateLimit.Burst,
				now,
			).Result()

			if err != nil {
				// Redis error: fall back to allowing the request
				c.Next()
				return
			}

			results, ok := res.([]interface{})
			if !ok || len(results) < 2 {
				c.Next()
				return
			}

			if allowedVal, ok := results[0].(int64); ok {
				allowed = allowedVal == 1
			}
			if remainingVal, ok := results[1].(int64); ok {
				remaining = int(remainingVal)
			}
			if len(results) > 2 {
				if resetVal, ok := results[2].(int64); ok {
					resetAfter = int(resetVal)
				}
			}
		} else {
			// In-memory fallback: use a simple per-path token bucket stored in memory
			allowed, remaining = inMemoryRateLimit(keyIdentifier, cfg.RateLimit.Rate, cfg.RateLimit.Burst)
		}

		// Set rate limit headers
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", cfg.RateLimit.Burst))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		if resetAfter > 0 {
			c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Unix()+int64(resetAfter)))
		}

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"code":        "RATE_LIMIT_EXCEEDED",
					"message":     "Too many requests. Please retry after the rate limit window.",
					"retry_after": resetAfter,
					"request_id":  c.GetString("request_id"),
				},
			})
			return
		}

		c.Next()
	}
}

// --- In-memory token bucket (local fallback) ---

// tokenBucket 表示内存回退模式下单个客户端的令牌桶状态。
type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

// inMemoryBuckets 按客户端标识缓存内存令牌桶，bucketMutex 保护其并发访问。
var (
	inMemoryBuckets = make(map[string]*tokenBucket)
	bucketMutex     sync.Mutex
)

// inMemoryRateLimit 是 Redis 不可用时的内存令牌桶回退实现，
// 按经过时间补充令牌并尝试消费一个，返回是否放行及剩余令牌数。
func inMemoryRateLimit(key string, rate float64, burst int) (allowed bool, remaining int) {
	bucketMutex.Lock()
	defer bucketMutex.Unlock()

	bucket, exists := inMemoryBuckets[key]
	now := time.Now()

	if !exists {
		bucket = &tokenBucket{
			tokens:     float64(burst),
			lastRefill: now,
		}
		inMemoryBuckets[key] = bucket
	}

	// Refill tokens
	elapsed := now.Sub(bucket.lastRefill).Seconds()
	bucket.tokens += elapsed * rate
	if bucket.tokens > float64(burst) {
		bucket.tokens = float64(burst)
	}
	bucket.lastRefill = now

	// Check if a token is available
	if bucket.tokens < 1.0 {
		remaining = int(bucket.tokens)
		return false, remaining
	}

	// Consume a token
	bucket.tokens -= 1.0
	remaining = int(bucket.tokens)
	return true, remaining
}
