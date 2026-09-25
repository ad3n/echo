package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ad3n/echo/v5"
)

type poolBenchWriter struct {
	header http.Header
}

func (w *poolBenchWriter) Header() http.Header       { return w.header }
func (*poolBenchWriter) WriteHeader(int)             {}
func (*poolBenchWriter) Write(b []byte) (int, error) { return len(b), nil }

type poolBenchBody struct {
	*bytes.Reader
}

func (*poolBenchBody) Close() error { return nil }

func BenchmarkPoolSafety(b *testing.B) {
	for _, size := range []int{1024, 65536} {
		name := "gzip_1KiB"
		if size == 65536 {
			name = "gzip_64KiB"
		}

		b.Run(name, func(b *testing.B) {
			payload := bytes.Repeat([]byte("data"), size/4)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
			writer := &poolBenchWriter{header: make(http.Header)}
			c := echo.New().NewContext(req, writer)
			h := Gzip()(func(c *echo.Context) error {
				_, err := c.Response().Write(payload)
				return err
			})
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				clear(writer.header)
				c.Reset(req, writer)
				if err := h(c); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	b.Run("decompress_15KiB", func(b *testing.B) {
		compressed, err := gzipString(strings.Repeat("benchmark data ", 1000))
		if err != nil {
			b.Fatal(err)
		}

		source := &poolBenchBody{Reader: bytes.NewReader(compressed)}
		req := httptest.NewRequest(http.MethodPost, "/", source)
		req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
		writer := &poolBenchWriter{header: make(http.Header)}
		c := echo.New().NewContext(req, writer)
		h := Decompress()(func(c *echo.Context) error {
			_, err := io.Copy(io.Discard, c.Request().Body)
			return err
		})
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			source.Reset(compressed)
			req.Body = source
			c.Reset(req, writer)
			if err := h(c); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("body_dump_1KiB", func(b *testing.B) {
		payload := strings.Repeat("data", 256)
		input := []byte(payload)
		source := &poolBenchBody{Reader: bytes.NewReader(input)}
		req := httptest.NewRequest(http.MethodPost, "/", source)
		writer := &poolBenchWriter{header: make(http.Header)}
		c := echo.New().NewContext(req, writer)
		h := BodyDump(func(*echo.Context, []byte, []byte, error) {})(func(c *echo.Context) error {
			if _, err := io.Copy(io.Discard, c.Request().Body); err != nil {
				return err
			}

			return c.String(http.StatusOK, payload)
		})
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			clear(writer.header)
			source.Reset(input)
			req.Body = source
			c.Reset(req, writer)
			if err := h(c); err != nil {
				b.Fatal(err)
			}
		}
	})
}
