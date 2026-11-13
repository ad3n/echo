// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"errors"
	"io"
	"net/http"

	"github.com/ad3n/echo/v5"
)

type (
	BodyLimitConfig struct {
		Skipper Skipper

		LimitBytes int64
	}

	emptyLimitedReader struct{}

	limitedReader struct {
		reader io.ReadCloser
		err    error
		limit  int64
		read   int64
	}
)

func BodyLimit(limitBytes int64) echo.MiddlewareFunc {
	return BodyLimitWithConfig(BodyLimitConfig{LimitBytes: limitBytes})
}

func BodyLimitWithConfig(config BodyLimitConfig) echo.MiddlewareFunc {
	return toMiddlewareOrPanic(config)
}

func (config BodyLimitConfig) ToMiddleware() (echo.MiddlewareFunc, error) {
	if config.LimitBytes < 0 {
		return nil, errors.New("body limit must be non-negative")
	}

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

			if req.Body == nil || req.Body == http.NoBody {
				req.Body = emptyLimitedReader{}
				return next(c)
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
	if r.err != nil {
		return 0, r.err
	}

	if len(b) == 0 {
		return 0, nil
	}

	remaining := r.limit - r.read
	if int64(len(b))-1 > remaining {
		b = b[:remaining+1]
	}

	n, err = r.reader.Read(b)

	if int64(n) <= remaining {
		r.read += int64(n)
		return n, err
	}

	n = int(remaining)
	r.read = r.limit
	r.err = echo.ErrStatusRequestEntityTooLarge
	return n, r.err
}

func (r *limitedReader) Close() error {
	return r.reader.Close()
}

func (emptyLimitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	return 0, io.EOF
}

func (emptyLimitedReader) Close() error {
	return nil
}
