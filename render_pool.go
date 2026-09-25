// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"bytes"
	"sync"
)

const maxPooledRenderBuf = 1 << 16

var renderBufPool = sync.Pool{
	New: newRenderBuffer,
}

func newRenderBuffer() any {
	return new(bytes.Buffer)
}

func releaseRenderBuffer(buf *bytes.Buffer) {
	if buf.Cap() > maxPooledRenderBuf {
		return
	}

	buf.Reset()
	renderBufPool.Put(buf)
}
