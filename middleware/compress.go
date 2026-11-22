// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/ad3n/echo/v5"
)

const (
	gzipScheme = "gzip"
)

// GzipConfig defines the config for Gzip middleware.
type GzipConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// Gzip compression level.
	// Optional. Default value -1.
	Level int

	// Length threshold before gzip compression is applied.
	// Optional. Default value 0.
	//
	// Most of the time you will not need to change the default. Compressing
	// a short response might increase the transmitted data because of the
	// gzip format overhead. Compressing the response will also consume CPU
	// and time on the server and the client (for decompressing). Depending on
	// your use case such a threshold might be useful.
	//
	// See also:
	// https://webmasters.stackexchange.com/questions/31750/what-is-recommended-minimum-object-size-for-gzip-performance-benefits
	MinLength int
}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
	buffer            *bytes.Buffer
	minLength         int
	code              int
	wroteHeader       bool
	wroteBody         bool
	minLengthExceeded bool
	finalized         bool
	passthrough       bool
}

// Gzip returns a middleware which compresses HTTP response using gzip compression scheme.
func Gzip() echo.MiddlewareFunc {
	return GzipWithConfig(GzipConfig{})
}

// GzipWithConfig returns a middleware which compresses HTTP response using gzip compression scheme.
func GzipWithConfig(config GzipConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

func (config GzipConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}

	if config.Level < -2 || config.Level > 9 {
		return nil, errors.New("invalid gzip level")
	}

	if config.Level == 0 {
		config.Level = -1
	}

	if config.MinLength < 0 {
		config.MinLength = 0
	}

	pool := gzipCompressPool(config)
	bpool := bufferPool()

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			if config.Skipper(c) {
				return next(c)
			}

			rw := c.Response()
			rw.Header().Add(echo.HeaderVary, echo.HeaderAcceptEncoding)
			if !acceptsGzip(c.Request().Header.Values(echo.HeaderAcceptEncoding)) {
				return next(c)
			}

			encoder, ok := pool.Get().(*gzipEncoder)
			if !ok || encoder == nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "invalid pool object")
			}

			w := encoder.writer
			encoder.output.writer = rw
			w.Reset(&encoder.output)
			var buf *bytes.Buffer
			if config.MinLength > 0 {
				buf = bpool.Get().(*bytes.Buffer)
				buf.Reset()
			}

			grw := &gzipResponseWriter{
				Writer:         w,
				ResponseWriter: rw,
				minLength:      config.MinLength,
				buffer:         buf,
			}
			c.SetResponse(grw)
			completed := false
			defer func() {
				defer func() {
					grw.finalized = true
					grw.Writer = closedGzipWriter{}
					if !grw.minLengthExceeded {
						grw.Writer = rw
						if c.Response() == grw {
							c.SetResponse(rw)
						}
					}

					grw.buffer = nil
					encoder.output.writer = io.Discard
					w.Header = gzip.Header{}
					if buf != nil {
						releaseMiddlewareBuffer(&bpool, buf)
					}

					pool.Put(encoder)
				}()

				if !completed {
					return
				}

				if grw.minLengthExceeded {
					if closeErr := w.Close(); err == nil {
						err = closeErr
					}

					return
				}

				if grw.passthrough || (err == nil && grw.passThroughIfEncoded()) {
					return
				}

				if !grw.wroteBody {
					rw.Header().Del(echo.HeaderContentEncoding)
					if err != nil {
						rw.Header().Del(echo.HeaderContentLength)
					}
				}

				if grw.wroteHeader {
					rw.WriteHeader(grw.code)
				}

				if buf != nil {
					if _, writeErr := buf.WriteTo(rw); err == nil {
						err = writeErr
					}
				}
			}()

			err = next(c)
			completed = true
			return err
		}
	}, nil
}

type closedGzipWriter struct{}

func (closedGzipWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.finalized {
		if !w.minLengthExceeded {
			w.ResponseWriter.WriteHeader(code)
		}

		return
	}

	if (code >= 100 && code < 200 && code != http.StatusSwitchingProtocols) || w.passthrough {
		w.ResponseWriter.WriteHeader(code)
		return
	}

	w.wroteHeader = true
	w.code = code
	w.passThroughIfEncoded()
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.finalized {
		return w.Writer.Write(b)
	}

	if w.passThroughIfEncoded() {
		return w.ResponseWriter.Write(b)
	}

	if w.Header().Get(echo.HeaderContentType) == "" {
		w.Header().Set(echo.HeaderContentType, http.DetectContentType(b))
	}

	w.wroteBody = true
	if w.minLengthExceeded {
		return w.Writer.Write(b)
	}

	if w.minLength == 0 {
		w.startCompression()
		return w.Writer.Write(b)
	}

	if len(b) < w.minLength-w.buffer.Len() {
		return w.buffer.Write(b)
	}

	w.startCompression()
	if w.buffer.Len() > 0 {
		if _, err := w.buffer.WriteTo(w.Writer); err != nil {
			return 0, err
		}
	}

	return w.Writer.Write(b)
}

func (w *gzipResponseWriter) startCompression() {
	w.minLengthExceeded = true
	w.Header().Del(echo.HeaderContentLength)
	w.Header().Set(echo.HeaderContentEncoding, gzipScheme)
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(w.code)
	}
}

func (w *gzipResponseWriter) Flush() {
	if w.finalized || w.passThroughIfEncoded() {
		http.NewResponseController(w.ResponseWriter).Flush()
		return
	}

	if !w.minLengthExceeded {
		w.startCompression()
		if w.buffer != nil {
			_, _ = w.buffer.WriteTo(w.Writer)
		}
	}

	if gw, ok := w.Writer.(*gzip.Writer); ok {
		_ = gw.Flush()
	}

	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *gzipResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

func gzipCompressPool(config GzipConfig) sync.Pool {
	return sync.Pool{
		New: func() any {
			encoder := &gzipEncoder{output: gzipOutput{writer: io.Discard}}
			w, err := gzip.NewWriterLevel(&encoder.output, config.Level)
			if err != nil {
				return err
			}

			encoder.writer = w
			return encoder
		},
	}
}

type gzipEncoder struct {
	writer *gzip.Writer
	output gzipOutput
}

type gzipOutput struct {
	writer io.Writer
}

func (w *gzipOutput) Write(p []byte) (int, error) {
	return w.writer.Write(p)
}

func bufferPool() sync.Pool {
	return sync.Pool{
		New: func() any {
			b := &bytes.Buffer{}
			return b
		},
	}
}

func (w *gzipResponseWriter) passThroughIfEncoded() bool {
	if w.passthrough {
		return true
	}
	if w.wroteBody || w.minLengthExceeded {
		return false
	}
	if !hasContentCoding(w.Header().Values(echo.HeaderContentEncoding)) {
		return false
	}
	w.passthrough = true
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(w.code)
	}
	return true
}

func hasContentCoding(headers []string) bool {
	for _, header := range headers {
		for coding := range strings.SplitSeq(header, ",") {
			coding = strings.TrimSpace(coding)
			if coding != "" && !strings.EqualFold(coding, "identity") {
				return true
			}
		}
	}
	return false
}

func acceptsGzip(headers []string) bool {
	var gzipAllowed, wildcardAllowed, wildcardRefused bool
	for _, header := range headers {
		for entry := range strings.SplitSeq(header, ",") {
			coding, weight, hasWeight := strings.Cut(entry, ";")
			coding = strings.TrimSpace(coding)
			switch {
			case strings.EqualFold(coding, gzipScheme), strings.EqualFold(coding, "x-gzip"):
				if hasWeight && !positiveQualityValue(weight) {
					return false
				}
				gzipAllowed = true
			case coding == "*":
				if hasWeight && !positiveQualityValue(weight) {
					wildcardRefused = true
				} else {
					wildcardAllowed = true
				}
			}
		}
	}
	return gzipAllowed || (wildcardAllowed && !wildcardRefused)
}

func positiveQualityValue(weight string) bool {
	name, value, ok := strings.Cut(strings.TrimSpace(weight), "=")
	if !ok || !strings.EqualFold(name, "q") {
		return false
	}
	whole, fraction, _ := strings.Cut(value, ".")
	if (whole != "0" && whole != "1") || len(fraction) > 3 {
		return false
	}
	positive := whole == "1"
	for i := range len(fraction) {
		digit := fraction[i]
		if digit < '0' || digit > '9' || (whole == "1" && digit != '0') {
			return false
		}
		positive = positive || digit != '0'
	}
	return positive
}
