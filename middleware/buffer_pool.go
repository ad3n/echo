package middleware

import (
	"bytes"
	"sync"
)

const maxPooledMiddlewareBuffer = 64 << 10

func releaseMiddlewareBuffer(pool *sync.Pool, buffer *bytes.Buffer) {
	if buffer.Cap() > maxPooledMiddlewareBuffer {
		return
	}

	buffer.Reset()
	pool.Put(buffer)
}
