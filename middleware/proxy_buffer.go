package middleware

import "sync"

const proxyCopyBufferSize = 32 << 10

var proxyCopyBuffers = proxyBufferPool{
	pool: sync.Pool{New: func() any { return new([proxyCopyBufferSize]byte) }},
}

type proxyBufferPool struct {
	pool sync.Pool
}

func (p *proxyBufferPool) Get() []byte {
	return p.pool.Get().(*[proxyCopyBufferSize]byte)[:]
}

func (p *proxyBufferPool) Put(buf []byte) {
	p.pool.Put((*[proxyCopyBufferSize]byte)(buf))
}
