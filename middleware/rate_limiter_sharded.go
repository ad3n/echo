package middleware

import (
	"errors"
	"hash/maphash"
	"sync/atomic"
	"time"

	"github.com/ad3n/echo/v5"
)

type ShardedRateLimiterMemoryStore struct {
	start       time.Time
	shards      []*RateLimiterMemoryStore
	seed        maphash.Seed
	nextCleanup atomic.Int64
	expiresIn   time.Duration
}

func NewShardedRateLimiterMemoryStore(config RateLimiterMemoryStoreConfig, shardCount int) (*ShardedRateLimiterMemoryStore, error) {
	if shardCount <= 0 {
		return nil, errors.New("rate limiter shard count must be positive")
	}

	shards := make([]*RateLimiterMemoryStore, shardCount)
	for i := range shards {
		shards[i] = NewRateLimiterMemoryStoreWithConfig(config)
	}

	store := &ShardedRateLimiterMemoryStore{
		start:     shards[0].lastCleanup,
		shards:    shards,
		seed:      maphash.MakeSeed(),
		expiresIn: shards[0].expiresIn,
	}
	store.nextCleanup.Store(int64(store.expiresIn))
	return store, nil
}

func (store *ShardedRateLimiterMemoryStore) Allow(identifier string) (bool, error) {
	shard := store.shardFor(identifier)
	allowed, err := shard.Allow(identifier)
	store.cleanup(shard.timeNow())
	return allowed, err
}

func (store *ShardedRateLimiterMemoryStore) AllowContext(c *echo.Context, identifier string) (bool, error) {
	shard := store.shardFor(identifier)
	allowed, err := shard.AllowContext(c, identifier)
	store.cleanup(shard.timeNow())
	return allowed, err
}

func (store *ShardedRateLimiterMemoryStore) shardFor(identifier string) *RateLimiterMemoryStore {
	return store.shards[maphash.String(store.seed, identifier)%uint64(len(store.shards))]
}

func (store *ShardedRateLimiterMemoryStore) cleanup(now time.Time) {
	elapsed := int64(now.Sub(store.start))
	next := store.nextCleanup.Load()
	if elapsed <= next || !store.nextCleanup.CompareAndSwap(next, int64(now.Add(store.expiresIn).Sub(store.start))) {
		return
	}

	for _, shard := range store.shards {
		shard.mutex.Lock()
		if now.Sub(shard.lastCleanup) > shard.expiresIn {
			shard.cleanupStaleVisitors(now)
		}

		shard.mutex.Unlock()
	}
}
