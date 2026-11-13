package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ad3n/echo/v5"
)

func BenchmarkURLMiddleware(b *testing.B) {
	for _, tc := range []struct {
		name       string
		middleware echo.MiddlewareFunc
	}{
		{"HTTPSRedirect", HTTPSRedirect()},
		{"AddSlash", AddTrailingSlash()},
		{"RemoveSlash", RemoveTrailingSlash()},
	} {
		b.Run(tc.name, func(b *testing.B) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			h := tc.middleware(func(*echo.Context) error { return nil })
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				req.URL.Path = "/path"
				req.URL.RawPath = ""
				req.RequestURI = "/path"
				if tc.name == "RemoveSlash" {
					req.URL.Path = "/path/"
					req.RequestURI = "/path/"
				}

				c.Reset(req, rec)
				if err := h(c); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRedirectRejectedHost(b *testing.B) {
	for _, size := range []int{1, 65536} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, "/"+strings.Repeat("a", size), nil)
			req.Host = "example.com%2fpath"
			c := echo.New().NewContext(req, httptest.NewRecorder())
			h := HTTPSRedirect()(func(*echo.Context) error {
				b.Fatal("invalid host reached handler")
				return nil
			})
			b.ReportAllocs()
			for b.Loop() {
				if err := h(c); err != echo.ErrBadRequest {
					b.Fatalf("expected bad request, got %v", err)
				}
			}
		})
	}
}
