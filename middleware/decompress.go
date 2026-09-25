// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"bufio"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/ad3n/echo/v5"
)

// DecompressConfig defines the config for Decompress middleware.
type DecompressConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// GzipDecompressPool defines an interface to provide the sync.Pool used to create/store Gzip readers
	GzipDecompressPool Decompressor

	// MaxDecompressedSize limits the maximum size of decompressed request body in bytes.
	// If the decompressed body exceeds this limit, the middleware returns HTTP 413 error.
	// This prevents zip bomb attacks where small compressed payloads decompress to huge sizes.
	// Default: 100 * MB (104,857,600 bytes)
	// Set to -1 to disable limits (not recommended in production).
	MaxDecompressedSize int64
}

// GZIPEncoding content-encoding header if set to "gzip", decompress body contents.
const GZIPEncoding string = "gzip"

// Decompressor is used to get the sync.Pool used by the middleware to get Gzip readers
type Decompressor interface {
	gzipDecompressPool() sync.Pool
}

// DefaultGzipDecompressPool is the default implementation of Decompressor interface
type DefaultGzipDecompressPool struct {
}

func (d *DefaultGzipDecompressPool) gzipDecompressPool() sync.Pool {
	return sync.Pool{New: func() any { return new(gzip.Reader) }}
}

// Decompress decompresses request body based if content encoding type is set to "gzip" with default config
//
// SECURITY: By default, this limits decompressed data to 100MB to prevent zip bomb attacks.
// To customize the limit, use DecompressWithConfig. To disable limits (not recommended in production),
// set MaxDecompressedSize to -1.
func Decompress() echo.MiddlewareFunc {
	return DecompressWithConfig(DecompressConfig{})
}

// DecompressWithConfig returns a decompress middleware with config or panics on invalid configuration.
//
// SECURITY: If MaxDecompressedSize is not set (zero value), it defaults to 100MB to prevent
// DoS attacks via zip bombs. Set to -1 to explicitly disable limits if needed for your use case.
func DecompressWithConfig(config DecompressConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

func (config DecompressConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}

	if config.GzipDecompressPool == nil {
		config.GzipDecompressPool = &DefaultGzipDecompressPool{}
	}

	if config.MaxDecompressedSize == 0 {
		config.MaxDecompressedSize = 100 * MB
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		pool := config.GzipDecompressPool.gzipDecompressPool()

		return func(c *echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}

			req := c.Request()
			if req.Body == nil {
				req.Body = http.NoBody
				return next(c)
			}

			if !isGzipContentEncoding(req.Header.Get(echo.HeaderContentEncoding)) {
				return next(c)
			}

			i := pool.Get()
			gr, ok := i.(*gzip.Reader)
			if !ok || gr == nil {
				if err, isErr := i.(error); isErr {
					return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
				}

				return echo.NewHTTPError(http.StatusInternalServerError, "unexpected type from gzip decompression pool")
			}

			input := newGzipInput(req.Body)
			if err := gr.Reset(input); err != nil {
				releaseGzipReader(&pool, gr, input)
				if err == io.EOF {
					return next(c)
				}

				return err
			}

			req.Body = &limitedGzipReader{
				reader:    gr,
				input:     input,
				source:    req.Body,
				pool:      &pool,
				remaining: config.MaxDecompressedSize,
				limited:   config.MaxDecompressedSize > 0,
			}
			req.ContentLength = -1

			return next(c)
		}
	}, nil
}

func isGzipContentEncoding(v string) bool {
	return strings.EqualFold(v, GZIPEncoding)
}

type limitedGzipReader struct {
	reader    *gzip.Reader
	input     *gzipInput
	source    io.ReadCloser
	pool      *sync.Pool
	terminal  error
	closeErr  error
	remaining int64
	mu        sync.Mutex
	closeOnce sync.Once
	limited   bool
}

func (r *limitedGzipReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(p) == 0 {
		return 0, nil
	}

	if r.terminal != nil {
		return 0, r.terminal
	}

	if r.limited && r.remaining == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			err = echo.ErrStatusRequestEntityTooLarge
		}

		if err != nil {
			r.finish(err)
		}

		return 0, err
	}

	if r.limited && int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}

	n, err := r.reader.Read(p)
	if r.limited {
		r.remaining -= int64(n)
	}

	if err != nil {
		r.finish(err)
	}

	return n, err
}

func (r *limitedGzipReader) finish(err error) {
	r.terminal = err
	if r.reader == nil {
		return
	}

	_ = r.reader.Close()
	releaseGzipReader(r.pool, r.reader, r.input)
	r.reader = nil
	r.input = nil
	r.pool = nil
}

func (r *limitedGzipReader) Close() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.source.Close()
	})
	r.mu.Lock()
	defer r.mu.Unlock()

	r.finish(http.ErrBodyReadAfterClose)
	return r.closeErr
}

type gzipEOFReader struct{}

func (gzipEOFReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (gzipEOFReader) ReadByte() (byte, error) {
	return 0, io.EOF
}

type gzipInput struct {
	io.Reader
	io.ByteReader
}

func newGzipInput(source io.Reader) *gzipInput {
	if reader, ok := source.(io.ByteReader); ok {
		return &gzipInput{Reader: source, ByteReader: reader}
	}

	buffer := bufio.NewReader(source)
	return &gzipInput{Reader: buffer, ByteReader: buffer}
}

func releaseGzipReader(pool *sync.Pool, reader *gzip.Reader, input *gzipInput) {
	input.Reader = gzipEOFReader{}
	input.ByteReader = gzipEOFReader{}
	_ = reader.Reset(gzipEOFReader{})
	pool.Put(reader)
}
