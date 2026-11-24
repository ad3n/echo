package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ad3n/echo/v5"
)

type optimizationBenchWriter struct {
	poolBenchWriter
	code int
}

func (w *optimizationBenchWriter) WriteHeader(code int) {
	w.code = code
}

func BenchmarkRequestLoggerQueryFields(b *testing.B) {
	for _, tc := range []struct {
		name    string
		keys    int
		present int
	}{{"Two", 2, 2}, {"ThirtyTwo", 32, 32}, {"Sparse", 32, 2}} {
		b.Run(tc.name, func(b *testing.B) {
			fields := make([]string, tc.keys)
			query := make([]string, tc.keys)
			for i := range fields {
				fields[i] = "key" + strconv.Itoa(i)
				key := fields[i]
				if i >= tc.present {
					key = "other" + strconv.Itoa(i)
				}

				query[i] = key + "=one&" + key + "=two"
			}

			e := echo.New()
			e.Use(RequestLoggerWithConfig(RequestLoggerConfig{
				LogQueryParams: fields,
				LogValuesFunc: func(_ *echo.Context, v RequestLoggerValues) error {
					if len(v.QueryParams) != tc.present || len(v.QueryParams["key0"]) != 2 {
						return echo.ErrInternalServerError
					}

					return nil
				},
			}))
			e.GET("/", func(*echo.Context) error { return nil })
			req := httptest.NewRequest(http.MethodGet, "/?"+strings.Join(query, "&"), nil)
			w := httptest.NewRecorder()
			b.ReportAllocs()
			for b.Loop() {
				e.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatal(w.Code)
				}
			}
		})
	}
}

func benchmarkRateLimiterRequests(b *testing.B, store RateLimiterStore, clients int, network bool) {
	e := echo.New()
	e.Use(RateLimiterWithConfig(RateLimiterConfig{
		Store:               store,
		IdentifierExtractor: func(c *echo.Context) (string, error) { return c.Request().Header.Get("X-Client-ID"), nil },
	}))
	e.GET("/", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	ids := make([]string, 1024)
	for i := range ids {
		ids[i] = strconv.Itoa(i)
		_, _ = store.Allow(ids[i])
	}

	var client *http.Client
	url := "/"
	if network {
		server := httptest.NewServer(e)
		defer server.Close()

		client = server.Client()
		transport := client.Transport.(*http.Transport)
		transport.MaxIdleConns = 64
		transport.MaxIdleConnsPerHost = 64
		url = server.URL + "/"
	}

	var workers atomic.Uint32
	b.SetParallelism(clients)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		start := int(workers.Add(1)) * 73
		if network {
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				b.Error(err)
				return
			}

			for pb.Next() {
				req.Header.Set("X-Client-ID", ids[start%len(ids)])
				start++
				res, err := client.Do(req)
				if err != nil {
					b.Error(err)
					return
				}

				err = res.Body.Close()
				if err != nil || res.StatusCode != http.StatusNoContent || res.Header.Get(HeaderXRateLimitLimit) == "" {
					b.Errorf("response status=%d, error=%v", res.StatusCode, err)
					return
				}
			}

			return
		}

		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := &optimizationBenchWriter{poolBenchWriter: poolBenchWriter{header: make(http.Header)}}
		for pb.Next() {
			req.Header.Set("X-Client-ID", ids[start%len(ids)])
			start++
			clear(w.header)
			w.code = 0
			e.ServeHTTP(w, req)
			if w.code != http.StatusNoContent || w.header.Get(HeaderXRateLimitLimit) == "" {
				b.Errorf("response status=%d, headers=%v", w.code, w.header)
				return
			}
		}
	})
}

func BenchmarkRateLimiterDefaultRequests(b *testing.B) {
	config := RateLimiterMemoryStoreConfig{Rate: 1_000_000, Burst: 1_000_000, ExpiresIn: time.Hour}
	b.Run("Handler", func(b *testing.B) {
		benchmarkRateLimiterRequests(b, NewRateLimiterMemoryStoreWithConfig(config), 4, false)
	})
	b.Run("HTTP", func(b *testing.B) {
		benchmarkRateLimiterRequests(b, NewRateLimiterMemoryStoreWithConfig(config), 4, true)
	})
}
