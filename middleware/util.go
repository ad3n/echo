// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
)

const (
	_ = int64(1 << (10 * iota)) // ignore first value by assigning to blank identifier
	// KB is 1 KiloByte = 1024 bytes
	KB
	// MB is 1 Megabyte = 1_048_576 bytes
	MB
	// GB is 1 Gigabyte = 1_073_741_824 bytes
	GB
	// TB is 1 Terabyte = 1_099_511_627_776 bytes
	TB
	// PB is 1 Petabyte = 1_125_899_906_842_624 bytes
	PB
	// EB is 1 Exabyte = 1_152_921_504_606_847_000 bytes
	EB
)

const (
	randomStringCharset    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	randomStringCharsetLen = 52
	randomStringMaxByte    = 255 - (256 % randomStringCharsetLen)
)

type randStringScratch struct {
	reader *bufio.Reader
	b      []byte
	r      []byte
}

var randStringScratchPool = sync.Pool{New: func() any {
	return &randStringScratch{reader: bufio.NewReader(rand.Reader)}
}}

func matchScheme(domain, pattern string) bool {
	domainScheme, _, domainHasScheme := strings.Cut(domain, ":")
	patternScheme, _, patternHasScheme := strings.Cut(pattern, ":")
	return domainHasScheme && patternHasScheme && domainScheme == patternScheme
}

func createRandomStringGenerator(length uint8) func() string {
	return func() string {
		return randomString(length)
	}
}

func randomString(length uint8) string {
	if length == 0 {
		return ""
	}

	sc := randStringScratchPool.Get().(*randStringScratch)
	defer randStringScratchPool.Put(sc)

	return sc.generate(length)
}

func (sc *randStringScratch) generate(length uint8) string {
	if length == 0 {
		return ""
	}

	n := int(length)
	if cap(sc.b) < n {
		sc.b = make([]byte, n)
	}

	sc.b = sc.b[:n]
	rlen := n + n/4
	if cap(sc.r) < rlen {
		sc.r = make([]byte, rlen)
	}

	sc.r = sc.r[:rlen]
	b, r := sc.b, sc.r
	var i uint8 = 0

	for {
		_, err := io.ReadFull(sc.reader, r)
		if err != nil {
			panic("unexpected error happened when reading from bufio.NewReader(crypto/rand.Reader)")
		}

		for _, rb := range r {
			if rb > randomStringMaxByte {
				continue
			}

			b[i] = randomStringCharset[rb%randomStringCharsetLen]
			i++
			if i == length {
				return string(b[:length])
			}
		}
	}
}

func validateOrigins(origins []string, what string) error {
	for _, o := range origins {
		if err := validateOrigin(o, what); err != nil {
			return err
		}
	}
	return nil
}

func validateOrigin(origin string, what string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("can not parse %s: %w", what, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s is missing scheme or host: %s", what, origin)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s can not have path, query, and fragments: %s", what, origin)
	}
	return nil
}
