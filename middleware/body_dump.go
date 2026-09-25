// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/ad3n/echo/v5"
)

// BodyDumpConfig defines the config for BodyDump middleware.
type BodyDumpConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// Handler receives request, response payloads and handler error if there are any.
	// Required.
	Handler BodyDumpHandler

	// MaxRequestBytes limits how much of the request body to dump.
	// If the request body exceeds this limit, only the first MaxRequestBytes
	// are dumped. The handler callback receives truncated data.
	// Default: 5 * MB (5,242,880 bytes)
	// Set to -1 to disable limits (not recommended in production).
	MaxRequestBytes int64

	// MaxResponseBytes limits how much of the response body to dump.
	// If the response body exceeds this limit, only the first MaxResponseBytes
	// are dumped. The handler callback receives truncated data.
	// Default: 5 * MB (5,242,880 bytes)
	// Set to -1 to disable limits (not recommended in production).
	MaxResponseBytes int64
}

// BodyDumpHandler receives the request and response payload.
type BodyDumpHandler func(c *echo.Context, reqBody []byte, resBody []byte, err error)

type bodyDumpResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

// BodyDump returns a BodyDump middleware.
//
// BodyDump middleware captures the request and response payload and calls the
// registered handler.
//
// SECURITY: By default, this limits dumped bodies to 5MB to prevent memory exhaustion
// attacks. To customize limits, use BodyDumpWithConfig. To disable limits (not recommended
// in production), explicitly set MaxRequestBytes and MaxResponseBytes to -1.
func BodyDump(handler BodyDumpHandler) echo.MiddlewareFunc {
	return BodyDumpWithConfig(BodyDumpConfig{Handler: handler})
}

// BodyDumpWithConfig returns a BodyDump middleware with config.
// See: `BodyDump()`.
//
// SECURITY: If MaxRequestBytes and MaxResponseBytes are not set (zero values), they default
// to 5MB each to prevent DoS attacks via large payloads. Set them explicitly to -1 to disable
// limits if needed for your use case.
func BodyDumpWithConfig(config BodyDumpConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

func (config BodyDumpConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Handler == nil {
		return nil, errors.New("echo body-dump middleware requires a handler function")
	}

	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}

	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = 5 * MB
	}

	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = 5 * MB
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}

			reqBuf := bodyDumpBufferPool.Get().(*bytes.Buffer)
			defer releaseMiddlewareBuffer(&bodyDumpBufferPool, reqBuf)

			reqBuf.Reset()
			req := c.Request()
			source := req.Body
			if source == nil {
				source = http.NoBody
			}

			var bodyReader io.Reader = source
			if config.MaxRequestBytes > 0 {
				bodyReader = io.LimitReader(source, config.MaxRequestBytes)
			}

			if _, err := io.Copy(reqBuf, bodyReader); err != nil {
				return err
			}

			reqBody := bytes.Clone(reqBuf.Bytes())
			replay := &replayReadCloser{
				prefix: reqBody,
				source: source,
			}
			req.Body = replay

			resBuf := bodyDumpBufferPool.Get().(*bytes.Buffer)
			defer releaseMiddlewareBuffer(&bodyDumpBufferPool, resBuf)

			resBuf.Reset()
			var respWriter io.Writer
			switch {
			case config.MaxResponseBytes > 0:
				respWriter = &limitedWriter{
					response: c.Response(),
					dumpBuf:  resBuf,
					limit:    config.MaxResponseBytes,
				}
			default:
				respWriter = io.MultiWriter(c.Response(), resBuf)
			}

			writer := &bodyDumpResponseWriter{
				Writer:         respWriter,
				ResponseWriter: c.Response(),
			}
			c.SetResponse(writer)
			defer func() {
				writer.Writer = writer.ResponseWriter
				if c.Response() == writer {
					c.SetResponse(writer.ResponseWriter)
				}
			}()

			err := next(c)
			if !replay.consumed.Load() {
				reqBody = bytes.Clone(reqBody)
			}

			config.Handler(c, reqBody, bytes.Clone(resBuf.Bytes()), err)
			return err
		}
	}, nil
}

type replayReadCloser struct {
	source   io.ReadCloser
	prefix   []byte
	offset   int
	consumed atomic.Bool
}

func (r *replayReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	if r.offset < len(r.prefix) {
		n := copy(p, r.prefix[r.offset:])
		r.offset += n
		if r.offset == len(r.prefix) {
			r.consumed.Store(true)
		}

		return n, nil
	}

	return r.source.Read(p)
}

func (r *replayReadCloser) Close() error {
	return r.source.Close()
}

func (w *bodyDumpResponseWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
}

func (w *bodyDumpResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func (w *bodyDumpResponseWriter) Flush() {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil && errors.Is(err, http.ErrNotSupported) {
		panic(errors.New("response writer flushing is not supported"))
	}
}

func (w *bodyDumpResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *bodyDumpResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

var bodyDumpBufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

type limitedWriter struct {
	response http.ResponseWriter
	dumpBuf  *bytes.Buffer
	dumped   int64
	limit    int64
}

func (w *limitedWriter) Write(b []byte) (n int, err error) {
	// Always write full data to actual response (don't truncate client response)
	n, err = w.response.Write(b)
	if err != nil {
		return n, err
	}

	// Write to dump buffer only up to limit
	if w.dumped < w.limit {
		remaining := w.limit - w.dumped
		toDump := min(int64(n), remaining)
		w.dumpBuf.Write(b[:toDump])
		w.dumped += toDump
	}

	return n, nil
}
