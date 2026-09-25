package middleware

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ad3n/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compressedOwnershipRequest(t *testing.T, value string) *http.Request {
	t.Helper()
	compressed, err := gzipString(value)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
	req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
	return req
}

func TestDecompressBodyOutlivesMiddleware(t *testing.T) {
	for _, limit := range []int64{-1, 32} {
		for _, outcome := range []string{"success", "error", "panic"} {
			t.Run(fmt.Sprintf("%d/%s", limit, outcome), func(t *testing.T) {
				e := echo.New()
				h := DecompressWithConfig(DecompressConfig{MaxDecompressedSize: limit})(func(*echo.Context) error {
					switch outcome {
					case "error":
						return echo.ErrBadRequest
					case "panic":
						panic("handler")
					}

					return nil
				})
				requests := []*http.Request{
					compressedOwnershipRequest(t, "first"),
					compressedOwnershipRequest(t, "second"),
				}
				for _, req := range requests {
					call := func() {
						err := h(e.NewContext(req, httptest.NewRecorder()))
						if outcome == "error" {
							require.ErrorIs(t, err, echo.ErrBadRequest)
							return
						}

						require.NoError(t, err)
					}
					if outcome == "panic" {
						assert.Panics(t, call)
						continue
					}

					call()
				}

				for i, req := range requests {
					data, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					assert.Equal(t, []string{"first", "second"}[i], string(data))
					reader := req.Body.(*limitedGzipReader)
					assert.Nil(t, reader.reader)
					assert.Nil(t, reader.input)
					assert.Nil(t, reader.pool)
					_, err = req.Body.Read(make([]byte, 1))
					require.ErrorIs(t, err, io.EOF)
					require.NoError(t, req.Body.Close())
					require.NoError(t, req.Body.Close())
					_, err = req.Body.Read(make([]byte, 1))
					assert.ErrorIs(t, err, http.ErrBodyReadAfterClose)
				}
			})
		}
	}
}

func TestDecompressExactLimitAndTerminalErrors(t *testing.T) {
	for _, value := range []string{"", "abcd", "abcde"} {
		t.Run(value, func(t *testing.T) {
			req := compressedOwnershipRequest(t, value)
			h := DecompressWithConfig(DecompressConfig{MaxDecompressedSize: 4})(func(*echo.Context) error { return nil })
			require.NoError(t, h(echo.New().NewContext(req, httptest.NewRecorder())))
			var output bytes.Buffer
			one := make([]byte, 1)
			for {
				n, err := req.Body.Read(one)
				output.Write(one[:n])
				if err != nil {
					if len(value) > 4 {
						assert.ErrorIs(t, err, echo.ErrStatusRequestEntityTooLarge)
						break
					}

					assert.ErrorIs(t, err, io.EOF)
					break
				}
			}

			assert.Equal(t, value[:min(4, len(value))], output.String())
			require.NoError(t, req.Body.Close())
		})
	}
}

type blockingGzipSource struct {
	started   chan struct{}
	closed    chan struct{}
	header    *bytes.Reader
	startOnce sync.Once
	closeOnce sync.Once
	closes    atomic.Int32
}

func (r *blockingGzipSource) Read(p []byte) (int, error) {
	if r.header.Len() > 0 {
		return r.header.Read(p)
	}

	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *blockingGzipSource) Close() error {
	r.closeOnce.Do(func() {
		r.closes.Add(1)
		close(r.closed)
	})
	return nil
}

func TestDecompressCloseUnblocksRead(t *testing.T) {
	compressed, err := gzipString("payload")
	require.NoError(t, err)
	source := &blockingGzipSource{
		header:  bytes.NewReader(compressed[:10]),
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	t.Cleanup(func() { _ = source.Close() })
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = source
	req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
	require.NoError(t, Decompress()(func(*echo.Context) error { return nil })(echo.New().NewContext(req, httptest.NewRecorder())))
	readDone := make(chan error, 1)
	go func() {
		_, err := req.Body.Read(make([]byte, 16))
		readDone <- err
	}()
	select {
	case <-source.started:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not reach source")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- req.Body.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Close deadlocked behind Read")
	}

	select {
	case err := <-readDone:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Read was not unblocked")
	}

	require.NoError(t, req.Body.Close())
	assert.Equal(t, int32(1), source.closes.Load())
}

func TestBodyDumpOwnsSnapshotsAndReplaysBody(t *testing.T) {
	e := echo.New()
	var snapshots [][]byte
	var captured http.ResponseWriter
	mw := BodyDumpWithConfig(BodyDumpConfig{
		MaxRequestBytes:  3,
		MaxResponseBytes: 3,
		Handler: func(_ *echo.Context, request, response []byte, _ error) {
			snapshots = append(snapshots, request, response)
		},
	})
	h := mw(func(c *echo.Context) error {
		captured = c.Response()
		return c.String(http.StatusOK, "response")
	})
	first := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abcdef"))
	firstRecorder := httptest.NewRecorder()
	c := e.NewContext(first, firstRecorder)
	require.NoError(t, h(c))
	firstWriter := captured
	snapshots[0][0] = 'X'
	data, err := io.ReadAll(first.Body)
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(data))
	require.NoError(t, first.Body.Close())
	_, err = firstWriter.Write([]byte("tail"))
	require.NoError(t, err)
	assert.Equal(t, "responsetail", firstRecorder.Body.String())
	assert.Equal(t, "res", string(snapshots[1]))
	assert.Same(t, c.Response(), firstWriter.(*bodyDumpResponseWriter).ResponseWriter)

	for range 16 {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("zzz"))
		require.NoError(t, h(e.NewContext(req, httptest.NewRecorder())))
		require.NoError(t, req.Body.Close())
	}

	assert.Equal(t, "Xbc", string(snapshots[0]))
	assert.Equal(t, "res", string(snapshots[1]))
}

func TestBodyDumpDetachesOnPanic(t *testing.T) {
	for _, callbackPanic := range []bool{false, true} {
		t.Run(fmt.Sprint(callbackPanic), func(t *testing.T) {
			c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
			original := c.Response()
			var captured *bodyDumpResponseWriter
			h := BodyDump(func(*echo.Context, []byte, []byte, error) {
				panic("callback")
			})(func(c *echo.Context) error {
				captured = c.Response().(*bodyDumpResponseWriter)
				if !callbackPanic {
					panic("handler")
				}

				return nil
			})
			assert.Panics(t, func() { _ = h(c) })
			assert.Same(t, original, c.Response())
			assert.Same(t, original, captured.Writer)
		})
	}
}

func TestGzipDetachesAndCountsOnlyCurrentWrite(t *testing.T) {
	e := echo.New()
	var saved *gzipResponseWriter
	mw := GzipWithConfig(GzipConfig{MinLength: 8})
	h := mw(func(c *echo.Context) error {
		saved = c.Response().(*gzipResponseWriter)
		for _, chunk := range []string{"first", "second"} {
			n, err := c.Response().Write([]byte(chunk))
			require.NoError(t, err)
			assert.Equal(t, len(chunk), n)
		}

		return nil
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
	rec := httptest.NewRecorder()
	require.NoError(t, h(e.NewContext(req, rec)))
	first := saved
	assert.Nil(t, first.buffer)
	assert.IsType(t, closedGzipWriter{}, first.Writer)
	gr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	require.NoError(t, err)
	decoded, err := io.ReadAll(gr)
	require.NoError(t, err)
	require.NoError(t, gr.Close())
	assert.Equal(t, "firstsecond", string(decoded))

	for range 16 {
		require.NoError(t, h(e.NewContext(req, httptest.NewRecorder())))
		n, err := first.Write([]byte("late"))
		assert.Zero(t, n)
		assert.ErrorIs(t, err, io.ErrClosedPipe)
	}
}

func TestGzipEmptyFlushAndLargeWrite(t *testing.T) {
	for _, payload := range []string{"", strings.Repeat("x", 1<<20)} {
		t.Run(fmt.Sprint(len(payload)), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
			rec := httptest.NewRecorder()
			h := Gzip()(func(c *echo.Context) error {
				writer := c.Response().(*gzipResponseWriter)
				if payload == "" {
					writer.Flush()
					return nil
				}

				n, err := writer.Write([]byte(payload))
				assert.Equal(t, len(payload), n)
				assert.Nil(t, writer.buffer)
				return err
			})
			require.NoError(t, h(echo.New().NewContext(req, rec)))
			gr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
			require.NoError(t, err)
			decoded, err := io.ReadAll(gr)
			require.NoError(t, err)
			require.NoError(t, gr.Close())
			assert.Equal(t, payload, string(decoded))
		})
	}
}

type failingPoolResponse struct {
	header http.Header
	err    error
}

func (w *failingPoolResponse) Header() http.Header       { return w.header }
func (*failingPoolResponse) WriteHeader(int)             {}
func (w *failingPoolResponse) Write([]byte) (int, error) { return 0, w.err }

func TestGzipFinalizationErrorAndPanicCleanup(t *testing.T) {
	for _, minLength := range []int{0, 100} {
		t.Run(fmt.Sprint(minLength), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
			failure := errors.New("transport failure")
			response := &failingPoolResponse{header: make(http.Header), err: failure}
			var saved *gzipResponseWriter
			h := GzipWithConfig(GzipConfig{MinLength: minLength})(func(c *echo.Context) error {
				saved = c.Response().(*gzipResponseWriter)
				_, _ = saved.Write([]byte("test"))
				return nil
			})
			assert.ErrorIs(t, h(echo.New().NewContext(req, response)), failure)
			assert.Nil(t, saved.buffer)
			h = GzipWithConfig(GzipConfig{MinLength: minLength})(func(c *echo.Context) error {
				saved = c.Response().(*gzipResponseWriter)
				panic("handler")
			})
			assert.Panics(t, func() { _ = h(echo.New().NewContext(req, httptest.NewRecorder())) })
			assert.Nil(t, saved.buffer)
			assert.True(t, saved.finalized)
		})
	}
}

func TestPoolMiddlewareConcurrentRequests(t *testing.T) {
	e := echo.New()
	dump := BodyDumpWithConfig(BodyDumpConfig{
		MaxRequestBytes:  4,
		MaxResponseBytes: 4,
		Handler:          func(*echo.Context, []byte, []byte, error) {},
	})
	e.Use(Decompress(), dump, Gzip())
	e.POST("/", func(c *echo.Context) error {
		payload, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}

		return c.Blob(http.StatusOK, echo.MIMETextPlain, payload)
	})
	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Go(func() {
			for iteration := range 16 {
				value := fmt.Sprintf("worker-%d-request-%d", worker, iteration)
				req := compressedOwnershipRequest(t, value)
				req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("status = %d, body = %q", rec.Code, rec.Body.String())
					return
				}

				gr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
				if err != nil {
					t.Error(err)
					return
				}

				payload, err := io.ReadAll(gr)
				_ = gr.Close()
				_ = req.Body.Close()
				if err != nil || string(payload) != value {
					t.Errorf("payload %q, want %q, error %v", payload, value, err)
					return
				}
			}
		})
	}

	wg.Wait()
}

func TestRandomStringZeroLength(t *testing.T) {
	assert.Empty(t, randomString(0))
}

func TestMiddlewareNilBodies(t *testing.T) {
	for _, mw := range []echo.MiddlewareFunc{
		BodyLimit(8),
		Decompress(),
		BodyDump(func(*echo.Context, []byte, []byte, error) {}),
	} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Body = nil
		req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		assert.NotPanics(t, func() {
			require.NoError(t, mw(func(c *echo.Context) error {
				data, err := io.ReadAll(c.Request().Body)
				assert.Empty(t, data)
				return err
			})(c))
		})
	}
}

func TestReleaseMiddlewareBufferDiscardsOversized(t *testing.T) {
	fresh := new(bytes.Buffer)
	pool := &sync.Pool{New: func() any { return fresh }}
	oversized := bytes.NewBuffer(make([]byte, maxPooledMiddlewareBuffer+1))
	releaseMiddlewareBuffer(pool, oversized)
	assert.Same(t, fresh, pool.Get())
	buffer := bytes.NewBufferString("data")
	releaseMiddlewareBuffer(pool, buffer)
	assert.Zero(t, buffer.Len())
}

type panickingPoolResponse struct {
	header http.Header
}

func (w *panickingPoolResponse) Header() http.Header     { return w.header }
func (*panickingPoolResponse) WriteHeader(int)           {}
func (*panickingPoolResponse) Write([]byte) (int, error) { panic("transport panic") }

func TestGzipPreservesTransportPanic(t *testing.T) {
	for _, minLength := range []int{0, 100} {
		t.Run(fmt.Sprint(minLength), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
			var saved *gzipResponseWriter
			h := GzipWithConfig(GzipConfig{MinLength: minLength})(func(c *echo.Context) error {
				saved = c.Response().(*gzipResponseWriter)
				_, err := saved.Write([]byte("test"))
				return err
			})
			response := &panickingPoolResponse{header: make(http.Header)}
			assert.PanicsWithValue(t, "transport panic", func() {
				_ = h(echo.New().NewContext(req, response))
			})
			assert.Nil(t, saved.buffer)
			assert.True(t, saved.finalized)
			rec := httptest.NewRecorder()
			require.NoError(t, h(echo.New().NewContext(req, rec)))
			assert.NotEmpty(t, rec.Body.Bytes())
		})
	}
}

func TestBodyDumpSnapshotDuringBodyRead(t *testing.T) {
	payload := strings.Repeat("payload", 2048)
	for range 32 {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
		data := make(chan []byte, 1)
		readErrors := make(chan error, 1)
		h := BodyDumpWithConfig(BodyDumpConfig{
			MaxRequestBytes:  4096,
			MaxResponseBytes: 16,
			Handler: func(_ *echo.Context, request, response []byte, _ error) {
				for i := range request {
					request[i] = 'x'
				}

				assert.Empty(t, response)
			},
		})(func(c *echo.Context) error {
			body := c.Request().Body
			go func() {
				result, err := io.ReadAll(body)
				data <- result
				readErrors <- err
			}()
			return nil
		})
		require.NoError(t, h(echo.New().NewContext(req, httptest.NewRecorder())))
		select {
		case result := <-data:
			assert.Equal(t, payload, string(result))
		case <-time.After(3 * time.Second):
			t.Fatal("request replay stalled")
		}

		require.NoError(t, <-readErrors)
		require.NoError(t, req.Body.Close())
	}
}
