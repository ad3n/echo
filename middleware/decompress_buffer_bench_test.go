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

func BenchmarkDecompressBuffered(b *testing.B) {
	compressed, err := gzipString(strings.Repeat("benchmark data ", 1000))
	if err != nil {
		b.Fatal(err)
	}

	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "parallel"
		}

		b.Run(name, func(b *testing.B) {
			e := echo.New()
			h := Decompress()(func(c *echo.Context) error {
				_, err := io.Copy(io.Discard, c.Request().Body)
				return err
			})
			run := func(loop func() bool) {
				source := bytes.NewReader(compressed)
				body := io.NopCloser(struct{ io.Reader }{source})
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
				writer := &poolBenchWriter{header: make(http.Header)}
				c := e.NewContext(req, writer)
				for loop() {
					source.Reset(compressed)
					req.Body = body
					c.Reset(req, writer)
					if err := h(c); err != nil {
						b.Fatal(err)
					}

					if err := req.Body.Close(); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			if parallel {
				b.RunParallel(func(pb *testing.PB) { run(pb.Next) })
				return
			}

			run(func() bool { return b.Loop() })
		})
	}
}
