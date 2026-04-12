package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ---------- 全局 Redis 客户端 ----------

var (
	rdb  *redis.Client
	once sync.Once
	ctx  = context.Background()
)

// RedisConfig holds Redis connection parameters read from .env.
type RedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       int
}

// Init initializes the global Redis client. It is safe to call multiple
// times; only the first invocation takes effect.
// If Redis is unreachable, the client is still created (lazy connect)
// but Ping will return an error – callers should degrade gracefully.
func Init(cfg RedisConfig) error {
	var initErr error
	once.Do(func() {
		addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
		rdb = redis.NewClient(&redis.Options{
			Addr:         addr,
			Password:     cfg.Password,
			DB:           cfg.DB,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  3 * time.Second,
			WriteTimeout: 3 * time.Second,
			PoolSize:     20,
			MinIdleConns: 5,
		})
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("[Redis] WARNING: ping failed (%s): %v – will retry on demand", addr, err)
			initErr = err
			return
		}
		log.Printf("[Redis] Connected to %s, DB=%d", addr, cfg.DB)
	})
	return initErr
}

// Close gracefully shuts down the Redis client.
func Close() error {
	if rdb != nil {
		return rdb.Close()
	}
	return nil
}

// Ping checks Redis connectivity. Returns nil when healthy.
func Ping() error {
	if rdb == nil {
		return fmt.Errorf("redis not initialized")
	}
	return rdb.Ping(ctx).Err()
}

// Client returns the raw *redis.Client for advanced usage.
func Client() *redis.Client {
	return rdb
}

// Available returns true when the Redis client has been initialised and
// can reach the server. It is a lightweight check used by callers to
// decide whether to use Redis or fall back to in-memory.
func Available() bool {
	if rdb == nil {
		return false
	}
	return rdb.Ping(ctx).Err() == nil
}

// ---------- Key-Value helpers ----------

// Set stores a key with the given value and TTL.
func Set(key string, value interface{}, ttl time.Duration) error {
	if rdb == nil {
		return fmt.Errorf("redis not initialized")
	}
	return rdb.Set(ctx, key, value, ttl).Err()
}

// Get retrieves a string value by key. Returns redis.Nil when key does
// not exist.
func Get(key string) (string, error) {
	if rdb == nil {
		return "", fmt.Errorf("redis not initialized")
	}
	return rdb.Get(ctx, key).Result()
}

// Del removes one or more keys.
func Del(keys ...string) error {
	if rdb == nil {
		return fmt.Errorf("redis not initialized")
	}
	return rdb.Del(ctx, keys...).Err()
}

// Exists returns true if the key exists.
func Exists(key string) (bool, error) {
	if rdb == nil {
		return false, fmt.Errorf("redis not initialized")
	}
	n, err := rdb.Exists(ctx, key).Result()
	return n > 0, err
}

// Incr atomically increments a key and returns the new value.
func Incr(key string) (int64, error) {
	if rdb == nil {
		return 0, fmt.Errorf("redis not initialized")
	}
	return rdb.Incr(ctx, key).Result()
}

// Expire sets a TTL on an existing key.
func Expire(key string, ttl time.Duration) error {
	if rdb == nil {
		return fmt.Errorf("redis not initialized")
	}
	return rdb.Expire(ctx, key, ttl).Err()
}

// TTL returns the remaining time-to-live of a key.
func TTL(key string) (time.Duration, error) {
	if rdb == nil {
		return 0, fmt.Errorf("redis not initialized")
	}
	return rdb.TTL(ctx, key).Result()
}

// ---------- JSON helpers ----------

// SetJSON marshals value to JSON and stores it with the given TTL.
func SetJSON(key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	return Set(key, string(data), ttl)
}

// GetJSON retrieves a key and unmarshals the JSON value into dest.
// Returns redis.Nil when key does not exist.
func GetJSON(key string, dest interface{}) error {
	s, err := Get(key)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(s), dest)
}

// ---------- Hash helpers (for session-like structures) ----------

// HSet sets fields in a hash.
func HSet(key string, values ...interface{}) error {
	if rdb == nil {
		return fmt.Errorf("redis not initialized")
	}
	return rdb.HSet(ctx, key, values...).Err()
}

// HGetAll returns all fields and values of a hash.
func HGetAll(key string) (map[string]string, error) {
	if rdb == nil {
		return nil, fmt.Errorf("redis not initialized")
	}
	return rdb.HGetAll(ctx, key).Result()
}
