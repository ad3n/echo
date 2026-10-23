// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/ad3n/echo/v5"
	"github.com/stretchr/testify/assert"
)

type ctxAwareStore struct {
	allowCalled    bool
	ctxAllowCalled bool
	allow          bool
}

func (s *ctxAwareStore) Allow(identifier string) (bool, error) {
	s.allowCalled = true
	return s.allow, nil
}

func (s *ctxAwareStore) AllowContext(c *echo.Context, identifier string) (bool, error) {
	s.ctxAllowCalled = true
	c.Response().Header().Set("Retry-After", "42")
	return s.allow, nil
}

func TestRateLimiter_storeAllowContextIsPreferred(t *testing.T) {
	e := echo.New()
	store := &ctxAwareStore{allow: true}
	mw := RateLimiterWithConfig(RateLimiterConfig{
		Store:               store,
		IdentifierExtractor: func(c *echo.Context) (string, error) { return "id", nil },
	})
	handler := mw(func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	assert.NoError(t, handler(c))
	assert.True(t, store.ctxAllowCalled, "AllowContext should be called when implemented")
	assert.False(t, store.allowCalled, "Allow should not be called when AllowContext is implemented")
	assert.Equal(t, "42", rec.Header().Get("Retry-After"), "store should be able to set headers via the context")
}

func TestRateLimiterMemoryStore_AllowContextSetsHeaders(t *testing.T) {
	store := NewRateLimiterMemoryStoreWithConfig(RateLimiterMemoryStoreConfig{Rate: 1, Burst: 3})
	e := echo.New()
	e.GET("/", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") },
		RateLimiterWithConfig(RateLimiterConfig{
			Store:               store,
			IdentifierExtractor: func(c *echo.Context) (string, error) { return "id", nil },
		}))

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	for i := range 3 {
		rec := do()
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "3", rec.Header().Get(HeaderXRateLimitLimit))
		assert.Equal(t, strconv.Itoa(2-i), rec.Header().Get(HeaderXRateLimitRemaining))
		assert.Empty(t, rec.Header().Get(echo.HeaderRetryAfter))
	}

	rec := do()
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "0", rec.Header().Get(HeaderXRateLimitRemaining))
	assert.NotEmpty(t, rec.Header().Get(echo.HeaderRetryAfter))
}
