package middleware

import (
	"strconv"
	"testing"
)

func BenchmarkRandomStringPoolShapes(b *testing.B) {
	for _, length := range []uint8{16, 32, 255} {
		b.Run(strconv.Itoa(int(length)), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = randomString(length)
				}
			})
		})
	}
}
