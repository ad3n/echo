package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ad3n/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackedLimitBody struct {
	*strings.Reader
	closed bool
}

func (b *trackedLimitBody) Close() error {
	b.closed = true
	return nil
}

func TestBodyLimitRequestOwnsReader(t *testing.T) {
	for _, outcome := range []string{"success", "error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			e := echo.New()
			h := BodyLimit(16)(func(c *echo.Context) error {
				switch c.Request().Header.Get("X-Outcome") {
				case "error":
					return errors.New("handler error")
				case "panic":
					panic("handler panic")
				}

				return nil
			})
			firstBody := &trackedLimitBody{Reader: strings.NewReader("first")}
			first := httptest.NewRequest(http.MethodPost, "/", nil)
			first.Body = firstBody
			first.ContentLength = -1
			first.Header.Set("X-Outcome", outcome)
			call := func() {
				err := h(e.NewContext(first, httptest.NewRecorder()))
				if outcome == "error" {
					require.Error(t, err)
					return
				}

				require.NoError(t, err)
			}
			if outcome == "panic" {
				assert.Panics(t, call)
			}

			if outcome != "panic" {
				call()
			}

			secondBody := &trackedLimitBody{Reader: strings.NewReader("second")}
			second := httptest.NewRequest(http.MethodPost, "/", nil)
			second.Body = secondBody
			second.ContentLength = -1
			require.NoError(t, h(e.NewContext(second, httptest.NewRecorder())))
			assert.NotSame(t, first.Body, second.Body)
			data, err := io.ReadAll(first.Body)
			require.NoError(t, err)
			assert.Equal(t, "first", string(data))
			require.NoError(t, first.Body.Close())
			assert.True(t, firstBody.closed)
			assert.False(t, secondBody.closed)
			data, err = io.ReadAll(second.Body)
			require.NoError(t, err)
			assert.Equal(t, "second", string(data))
		})
	}
}

func TestBodyLimitConcurrentRetainedReaders(t *testing.T) {
	e := echo.New()
	h := BodyLimit(16)(func(*echo.Context) error { return nil })
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 32 {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("payload"))
				c := e.NewContext(req, httptest.NewRecorder())
				if err := h(c); err != nil {
					t.Error(err)
					return
				}

				data, err := io.ReadAll(req.Body)
				if err != nil || string(data) != "payload" {
					t.Errorf("body = %q, error = %v", data, err)
					return
				}
			}
		})
	}

	wg.Wait()
}

func BenchmarkBodyLimitReaderOwnership(b *testing.B) {
	e := echo.New()
	h := BodyLimit(16)(func(*echo.Context) error { return nil })
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	c := e.NewContext(req, httptest.NewRecorder())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		req.Body = http.NoBody
		if err := h(c); err != nil {
			b.Fatal(err)
		}
	}
}
