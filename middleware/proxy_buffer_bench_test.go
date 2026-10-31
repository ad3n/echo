package middleware

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ad3n/echo/v5"
)

type (
	proxyBenchTransport struct{ payload []byte }

	proxyBenchWriter struct {
		header http.Header
		size   int
	}
)

func (t proxyBenchTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(t.payload)), ContentLength: int64(len(t.payload))}, nil
}

func (w *proxyBenchWriter) Header() http.Header { return w.header }
func (w *proxyBenchWriter) WriteHeader(int)     {}
func (w *proxyBenchWriter) Write(p []byte) (int, error) {
	w.size += len(p)
	return len(p), nil
}

func BenchmarkProxyResponseBuffer(b *testing.B) {
	for _, size := range []int{1024, 256 << 10} {
		for _, parallel := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/parallel=%t", size, parallel), func(b *testing.B) {
				e := echo.New()
				e.Use(ProxyWithConfig(ProxyConfig{
					Balancer:  NewRoundRobinBalancer([]*ProxyTarget{{URL: &url.URL{Scheme: "http", Host: "upstream.test"}}}),
					Transport: proxyBenchTransport{payload: bytes.Repeat([]byte("x"), size)},
				}))
				run := func(pb *testing.PB) {
					req := httptest.NewRequest(http.MethodGet, "/", nil)
					w := &proxyBenchWriter{header: make(http.Header)}
					step := func() {
						clear(w.header)
						w.size = 0
						e.ServeHTTP(w, req)
						if w.size != size {
							b.Fatalf("copied %d bytes, want %d", w.size, size)
						}
					}

					if pb != nil {
						for pb.Next() {
							step()
						}

						return
					}

					for range b.N {
						step()
					}
				}

				e.ServeHTTP(&proxyBenchWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/", nil))
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				if parallel {
					b.RunParallel(run)
					return
				}

				run(nil)
			})
		}
	}
}

func BenchmarkProxyResponseNetwork(b *testing.B) {
	for _, size := range []int{1024, 256 << 10} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			payload := bytes.Repeat([]byte("x"), size)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(payload)
			}))
			defer upstream.Close()

			target, err := url.Parse(upstream.URL)
			if err != nil {
				b.Fatal(err)
			}

			transport := http.DefaultTransport.(*http.Transport).Clone()
			defer transport.CloseIdleConnections()

			e := echo.New()
			e.Use(ProxyWithConfig(ProxyConfig{Balancer: NewRoundRobinBalancer([]*ProxyTarget{{URL: target}}), Transport: transport}))
			frontend := httptest.NewServer(e)
			defer frontend.Close()

			client := frontend.Client()
			request := func() {
				response, err := client.Get(frontend.URL)
				if err != nil {
					b.Fatal(err)
				}

				n, err := io.Copy(io.Discard, response.Body)
				closeErr := response.Body.Close()
				if err != nil || closeErr != nil || n != int64(size) || response.StatusCode != http.StatusOK {
					b.Fatalf("response status=%d bytes=%d read=%v close=%v", response.StatusCode, n, err, closeErr)
				}
			}

			request()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				request()
			}
		})
	}
}
