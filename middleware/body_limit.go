// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"io"
	"net/http"

	"github.com/ad3n/echo/v5"
)

// BodyLimitConfig defines the config for BodyLimitWithConfig middleware.
type BodyLimitConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper Skipper

	// LimitBytes is maximum allowed size in bytes for a request body
	LimitBytes int64
}

type limitedReader struct {
	reader io.ReadCloser
	limit  int64
	read   int64
}

// BodyLimit returns a BodyLimit middleware.
//
// BodyLimit middleware sets the maximum allowed size for a request body, if the size exceeds the configured limit, it
// sends "413 - Request Entity Too Large" response. The BodyLimit is determined based on both `Content-Length` request
// header and actual content read, which makes it super secure.
func BodyLimit(limitBytes int64) echo.MiddlewareFunc {
	return BodyLimitWithConfig(BodyLimitConfig{LimitBytes: limitBytes})
}

// BodyLimitWithConfig returns a BodyLimitWithConfig middleware. Middleware sets the maximum allowed size in bytes for
// a request body, if the  size exceeds the configured limit, it sends "413 - Request Entity Too Large" response.
// The BodyLimitWithConfig is determined based on both `Content-Length` request header and actual content read, which
// makes it super secure.
func BodyLimitWithConfig(config BodyLimitConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

func (config BodyLimitConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.Skipper == nil {
		config.Skipper = DefaultSkipper
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if config.Skipper(c) {
				return next(c)
			}

			req := c.Request()
			if req.ContentLength > config.LimitBytes {
				return echo.ErrStatusRequestEntityTooLarge
			}

			if req.Body == nil {
				req.Body = http.NoBody
			}

			req.Body = &limitedReader{
				reader: req.Body,
				limit:  config.LimitBytes,
			}

			return next(c)
		}
	}, nil
}

func (r *limitedReader) Read(b []byte) (n int, err error) {
	n, err = r.reader.Read(b)
	r.read += int64(n)
	if r.read > r.limit {
		return n, echo.ErrStatusRequestEntityTooLarge
	}

	return
}

func (r *limitedReader) Close() error {
	return r.reader.Close()
}

func (r *limitedReader) Reset(reader io.ReadCloser) {
	r.reader = reader
	r.read = 0
}
