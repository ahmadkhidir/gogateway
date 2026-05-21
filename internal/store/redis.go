package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient wraps a go-redis client and provides high-level operations
// for rate limiting and other gateway features.
type RedisClient struct {
	client *redis.Client
}

// NewRedisClient creates a new Redis client from the given configuration.
// Returns nil if no address is configured (Redis is optional).
func NewRedisClient(addr, password string, db, poolSize int) (*RedisClient, error) {
	if addr == "" {
		return nil, nil
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
		PoolSize: poolSize,
	})

	// Verify connectivity.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}

	return &RedisClient{client: client}, nil
}

// Close shuts down the Redis connection pool.
func (rc *RedisClient) Close() error {
	if rc == nil || rc.client == nil {
		return nil
	}
	return rc.client.Close()
}

// CheckRateLimit implements a fixed-window rate limit using INCR + EXPIRE.
//
// The key "rate:{routeID}:{clientID}" is incremented on each request.
// On first request (INCR → 1) the TTL is set to the window duration.
// If the count exceeds the limit the request is denied.
//
// Returns (allowed, remaining, resetDuration, error).
func (rc *RedisClient) CheckRateLimit(ctx context.Context, routeID, clientID string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	key := fmt.Sprintf("rate:%s:%s", routeID, clientID)
	windowMs := window.Milliseconds()

	// Lua script for atomic increment + expire + TTL query.
	// KEYS[1] = key, ARGV[1] = limit, ARGV[2] = window in ms
	script := redis.NewScript(`
		local key = KEYS[1]
		local limit = tonumber(ARGV[1])
		local window = tonumber(ARGV[2])

		local count = redis.call('INCR', key)
		if count == 1 then
			redis.call('PEXPIRE', key, window)
		end

		local remaining = limit - count
		if remaining < 0 then remaining = 0 end

		local ttl = redis.call('PTTL', key)
		if ttl < 0 then ttl = 0 end

		if count <= limit then
			return {1, remaining, ttl}
		else
			return {0, remaining, ttl}
		end
	`)

	raw, err := script.Run(ctx, rc.client, []string{key}, limit, windowMs).Result()
	if err != nil {
		return false, 0, 0, fmt.Errorf("rate limit: %w", err)
	}

	// Result is []any{int64, int64, int64}
	items, ok := raw.([]any)
	if !ok || len(items) < 3 {
		return false, 0, 0, fmt.Errorf("rate limit: unexpected result type %T", raw)
	}

	allowed := toInt(items[0]) == 1
	remaining := toInt(items[1])
	reset := time.Duration(toInt(items[2])) * time.Millisecond

	return allowed, remaining, reset, nil
}

// toInt converts a Redis integer response (typically int64) to int.
func toInt(v any) int {
	switch n := v.(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

// Ping checks connectivity to Redis.
func (rc *RedisClient) Ping(ctx context.Context) error {
	return rc.client.Ping(ctx).Err()
}
