package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ad3n/echo/v5"
)

func TestShardedRateLimiterMiddleware(t *testing.T) {
	config := RateLimiterMemoryStoreConfig{Rate: 0.1, Burst: 1}
	store, err := NewShardedRateLimiterMemoryStore(config, 32)
	if err != nil {
		t.Fatal(err)
	}

	e := echo.New()
	e.Use(RateLimiterWithConfig(RateLimiterConfig{
		Store:               store,
		IdentifierExtractor: func(c *echo.Context) (string, error) { return c.Request().Header.Get("X-Client-ID"), nil },
	}))
	e.GET("/", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	for _, tc := range []struct {
		id     string
		status int
	}{{"A", 204}, {"A", 429}, {"B", 204}, {"B", 429}} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Client-ID", tc.id)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != tc.status || rec.Header().Get(HeaderXRateLimitLimit) != "1" || rec.Header().Get(HeaderXRateLimitRemaining) != "0" {
			t.Fatalf("id %s: status=%d, headers=%v", tc.id, rec.Code, rec.Header())
		}

		if (rec.Header().Get(echo.HeaderRetryAfter) != "") != (tc.status == http.StatusTooManyRequests) {
			t.Fatalf("id %s: unexpected retry header %v", tc.id, rec.Header())
		}
	}
}

func TestShardedRateLimiterCleanupInactiveShard(t *testing.T) {
	config := RateLimiterMemoryStoreConfig{Rate: 0, Burst: 1, ExpiresIn: time.Second}
	store, err := NewShardedRateLimiterMemoryStore(config, 4)
	if err != nil {
		t.Fatal(err)
	}

	now := store.start
	for _, shard := range store.shards {
		shard.lastCleanup = now
		shard.timeNow = func() time.Time { return now }
	}

	inactive := "inactive"
	active := ""
	for i := range 1024 {
		key := strconv.Itoa(i)
		if store.shardFor(key) != store.shardFor(inactive) {
			active = key
			break
		}
	}

	if active == "" {
		t.Fatal("unable to select keys from different shards")
	}

	if allowed, err := store.Allow(inactive); !allowed || err != nil {
		t.Fatalf("initial request: %v, %v", allowed, err)
	}

	now = now.Add(2 * time.Second)
	if allowed, err := store.Allow(active); !allowed || err != nil {
		t.Fatalf("active shard request: %v, %v", allowed, err)
	}

	if allowed, err := store.Allow(inactive); !allowed || err != nil {
		t.Fatalf("expired visitor reused: %v, %v", allowed, err)
	}
}

func TestShardedRateLimiterRejectsInvalidShardCount(t *testing.T) {
	if _, err := NewShardedRateLimiterMemoryStore(RateLimiterMemoryStoreConfig{}, 0); err == nil {
		t.Fatal("expected error for zero shards")
	}
}

func TestShardedRateLimiterConcurrentBurst(t *testing.T) {
	store, err := NewShardedRateLimiterMemoryStore(RateLimiterMemoryStoreConfig{Burst: 3}, 32)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var accepted [8]atomic.Int32
	for i := range 64 {
		wg.Go(func() {
			key := i % len(accepted)
			allowed, err := store.Allow(strconv.Itoa(key))
			if err != nil {
				t.Error(err)
			}

			if allowed {
				accepted[key].Add(1)
			}
		})
	}

	wg.Wait()
	for i := range accepted {
		if count := accepted[i].Load(); count != 3 {
			t.Fatalf("identifier %d allowed %d requests", i, count)
		}
	}
}

func BenchmarkRateLimiterShardedRequests(b *testing.B) {
	config := RateLimiterMemoryStoreConfig{Rate: 1_000_000, Burst: 1_000_000, ExpiresIn: time.Hour}
	b.Run("Handler", func(b *testing.B) {
		store, err := NewShardedRateLimiterMemoryStore(config, 32)
		if err != nil {
			b.Fatal(err)
		}

		benchmarkRateLimiterRequests(b, store, 4, false)
	})
	b.Run("HTTP", func(b *testing.B) {
		store, err := NewShardedRateLimiterMemoryStore(config, 32)
		if err != nil {
			b.Fatal(err)
		}

		benchmarkRateLimiterRequests(b, store, 4, true)
	})
}
