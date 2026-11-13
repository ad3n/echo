package middleware

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRandomStringScratchOwnershipAndRejection(t *testing.T) {
	input := []byte{208, 255, 0, 51, 52, 207, 1, 2, 3, 4, 10, 11, 12, 13, 14}
	sc := &randStringScratch{reader: bufio.NewReader(bytes.NewReader(input))}
	first := sc.generate(4)
	if first != "AzAz" {
		t.Fatalf("rejection sampling changed: %q", first)
	}

	second := sc.generate(4)
	if second != "KLMN" || first != "AzAz" {
		t.Fatalf("pool scratch changed retained output: %q / %q", first, second)
	}

	clear(sc.b)
	if first != "AzAz" || second != "KLMN" {
		t.Fatal("returned strings alias mutable scratch memory")
	}
}

func TestRandomStringScratchEntropyFailure(t *testing.T) {
	sc := &randStringScratch{reader: bufio.NewReader(strings.NewReader(""))}
	defer func() {
		if recover() == nil {
			t.Fatal("entropy failure did not panic")
		}
	}()

	_ = sc.generate(32)
}

func FuzzRandomStringScratchCompatibility(f *testing.F) {
	f.Add([]byte{0, 51, 52, 207, 208, 255}, uint8(255))
	f.Add([]byte("seed"), uint8(32))
	f.Fuzz(func(t *testing.T, seed []byte, length uint8) {
		if len(seed) > 4096 {
			t.Skip()
		}

		data := append(bytes.Clone(seed), bytes.Repeat([]byte{0, 51, 52, 207}, 256)...)
		sc := &randStringScratch{reader: bufio.NewReader(bytes.NewReader(data))}
		got := sc.generate(length)
		var want strings.Builder
		for _, value := range data {
			if want.Len() == int(length) {
				break
			}

			if value <= randomStringMaxByte {
				want.WriteByte(randomStringCharset[int(value)%len(randomStringCharset)])
			}
		}

		if got != want.String() {
			t.Fatalf("length %d: got %q, want %q", length, got, want.String())
		}

		sc.reader.Reset(io.LimitReader(bytes.NewReader(data), 0))
		clear(sc.b)
		clear(sc.r)
		if got != want.String() {
			t.Fatal("output changed when scratch reused")
		}
	})
}
