package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/ad3n/echo/v5"
)

type proxyBufferErrorBody struct {
	closed bool
}

func (*proxyBufferErrorBody) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func (b *proxyBufferErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestProxyBufferConcurrentOwnership(t *testing.T) {
	var wg sync.WaitGroup
	for id := range 16 {
		wg.Go(func() {
			for range 100 {
				buf := proxyCopyBuffers.Get()
				if len(buf) != proxyCopyBufferSize || cap(buf) != proxyCopyBufferSize {
					t.Errorf("unexpected buffer shape: len=%d cap=%d", len(buf), cap(buf))
					return
				}

				for i := range buf {
					buf[i] = byte(id)
				}

				runtime.Gosched()
				for _, value := range buf {
					if value != byte(id) {
						t.Error("buffer shared by active borrowers")
						break
					}
				}

				proxyCopyBuffers.Put(buf)
			}
		})
	}

	wg.Wait()
}

func TestProxyBufferResponseIsolation(t *testing.T) {
	for _, size := range []int{0, 1, proxyCopyBufferSize - 1, proxyCopyBufferSize, proxyCopyBufferSize + 1, 256 << 10} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			payload := bytes.Repeat([]byte{byte(size%251 + 1)}, size)
			e := echo.New()
			e.Use(ProxyWithConfig(ProxyConfig{
				Balancer:  NewRoundRobinBalancer([]*ProxyTarget{{URL: &url.URL{Scheme: "http", Host: "upstream.test"}}}),
				Transport: proxyBenchTransport{payload: payload},
			}))
			for range 20 {
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
				if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), payload) {
					t.Fatalf("response mismatch: status=%d bytes=%d", rec.Code, rec.Body.Len())
				}
			}
		})
	}
}

func TestProxyBufferReadFailureClosesBody(t *testing.T) {
	body := &proxyBufferErrorBody{}
	e := echo.New()
	e.Use(ProxyWithConfig(ProxyConfig{
		Balancer: NewRoundRobinBalancer([]*ProxyTarget{{URL: &url.URL{Scheme: "http", Host: "upstream.test"}}}),
		Transport: proxyIdentityTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, ContentLength: 1}, nil
		}),
	}))
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !body.closed {
		t.Fatal("upstream body was not closed after read failure")
	}

	buf := proxyCopyBuffers.Get()
	defer proxyCopyBuffers.Put(buf)

	if len(buf) != proxyCopyBufferSize {
		t.Fatal("invalid buffer after read failure")
	}
}
