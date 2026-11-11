// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"bufio"
	"compress/gzip"
	"io"
	"sync"
)

var gzipInputBufferPool = sync.Pool{
	New: func() any {
		return &gzipInputBuffer{Reader: *bufio.NewReader(gzipEOFReader{})}
	},
}

type (
	gzipEOFReader struct{}

	gzipInputBuffer struct {
		bufio.Reader
	}

	gzipInput struct {
		io.Reader
		io.ByteReader
	}
)

func (gzipEOFReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (gzipEOFReader) ReadByte() (byte, error) {
	return 0, io.EOF
}

func newGzipInput(source io.Reader) *gzipInput {
	if reader, ok := source.(io.ByteReader); ok {
		return &gzipInput{Reader: source, ByteReader: reader}
	}

	buffer := gzipInputBufferPool.Get().(*gzipInputBuffer)
	buffer.Reset(source)
	return &gzipInput{Reader: buffer, ByteReader: buffer}
}

func releaseGzipReader(pool *sync.Pool, reader *gzip.Reader, input *gzipInput) {
	buffer, _ := input.Reader.(*gzipInputBuffer)
	input.Reader = gzipEOFReader{}
	input.ByteReader = gzipEOFReader{}
	_ = reader.Reset(gzipEOFReader{})
	if buffer != nil {
		buffer.Reset(gzipEOFReader{})
		gzipInputBufferPool.Put(buffer)
	}

	pool.Put(reader)
}
