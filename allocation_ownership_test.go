package echo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextReleaseDropsRequestReferences(t *testing.T) {
	e := New()
	req := httptest.NewRequest(http.MethodGet, "/?tag=one&tag=two", nil)
	req.Header.Set("X-Test", "original")
	c := e.NewContext(req, httptest.NewRecorder())
	c.Set("payload", req)
	c.SetLogger(e.Logger.With("request", req))
	c.SetPathValues(PathValues{{Name: "first", Value: "secret"}, {Name: "second", Value: "tail"}})
	c.SetPathValues(PathValues{{Name: "first", Value: "short"}})
	c.orgResponse.Before(func() { _ = req.URL })
	c.orgResponse.After(func() { _ = req.Header })
	c.dsw.ResponseWriter = c.Response()
	c.route = &RouteInfo{Path: "/:first"}
	c.handler = func(*Context) error { return nil }
	c.path = "/:first"
	query := c.QueryParams()
	var bound map[string][]string
	require.NoError(t, BindQueryParams(c, &bound))

	e.ReleaseContext(c)

	assert.Nil(t, c.request)
	assert.Nil(t, c.query)
	assert.Empty(t, c.store)
	assert.Nil(t, c.route)
	assert.Nil(t, c.handler)
	assert.Empty(t, c.path)
	assert.Same(t, e.Logger, c.logger)
	assert.Nil(t, c.orgResponse.ResponseWriter)
	assert.Same(t, c.orgResponse, c.response)
	assert.Nil(t, c.dsw.ResponseWriter)
	assert.Empty(t, c.PathValues())
	for _, value := range (*c.pathValues)[:cap(*c.pathValues)] {
		assert.Equal(t, PathValue{}, value)
	}

	for _, hooks := range [][]func(){c.orgResponse.beforeFuncs, c.orgResponse.afterFuncs} {
		assert.Empty(t, hooks)
		for _, hook := range hooks[:cap(hooks)] {
			assert.Nil(t, hook)
		}
	}

	assert.Equal(t, url.Values{"tag": {"one", "two"}}, query)
	assert.Equal(t, []string{"one", "two"}, bound["tag"])
	assert.Equal(t, "original", req.Header.Get("X-Test"))
}

func TestContextResetDropsOversizedStore(t *testing.T) {
	c := New().NewContext(nil, nil)
	for i := 0; i <= maxPooledContextStoreEntries; i++ {
		c.Set(fmt.Sprint(i), i)
	}

	c.Reset(nil, nil)
	require.Nil(t, c.store)
	c.Set("next", "value")
	assert.Equal(t, "value", c.Get("next"))
}

func TestContextResetWithoutEcho(t *testing.T) {
	c := NewContext(nil, nil)
	c.Set("previous", "value")
	assert.NotPanics(t, func() { c.Reset(nil, nil) })
	assert.Empty(t, c.store)
	assert.NotNil(t, c.Logger())
}

func TestResponseHooksReuseAndRelease(t *testing.T) {
	r := NewResponse(httptest.NewRecorder(), New().Logger)
	var before, after int
	r.Before(func() { before++ })
	r.After(func() { after++ })
	beforeSlot, afterSlot := &r.beforeFuncs[0], &r.afterFuncs[0]
	_, err := r.Write([]byte("one"))
	require.NoError(t, err)
	r.reset(httptest.NewRecorder())
	require.Nil(t, *beforeSlot)
	require.Nil(t, *afterSlot)
	r.Before(func() { before += 10 })
	r.After(func() { after += 10 })
	assert.Equal(t, beforeSlot, &r.beforeFuncs[0])
	assert.Equal(t, afterSlot, &r.afterFuncs[0])
	_, err = r.Write([]byte("two"))
	require.NoError(t, err)
	assert.Equal(t, 11, before)
	assert.Equal(t, 11, after)

	for range maxPooledResponseHooks + 1 {
		r.Before(func() {})
		r.After(func() {})
	}

	r.reset(nil)
	assert.Nil(t, r.beforeFuncs)
	assert.Nil(t, r.afterFuncs)
}

func TestServeHTTPReleasesContextOnEveryExit(t *testing.T) {
	for _, outcome := range []string{"success", "error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			e := New()
			var captured *Context
			e.GET("/", func(c *Context) error {
				captured = c
				c.Set("request", c.Request())
				switch outcome {
				case "error":
					return ErrBadRequest
				case "panic":
					panic("test")
				}

				return c.NoContent(http.StatusNoContent)
			})
			serve := func() {
				e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			}
			if outcome == "panic" {
				assert.Panics(t, serve)
			}

			if outcome != "panic" {
				assert.NotPanics(t, serve)
			}

			require.NotNil(t, captured)
			assert.Nil(t, captured.request)
			assert.Empty(t, captured.store)
			assert.Nil(t, captured.orgResponse.ResponseWriter)
		})
	}
}

func BenchmarkServeHTTP_ResponseHooks(b *testing.B) {
	e := New()
	hook := func() {}
	e.GET("/", func(c *Context) error {
		c.orgResponse.Before(hook)
		c.orgResponse.After(hook)
		return c.NoContent(http.StatusNoContent)
	})
	benchServe(b, e, httptest.NewRequest(http.MethodGet, "/", nil))
}
