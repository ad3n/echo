// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"strings"
	"sync"
	"testing"
)

func TestRandomStringConcurrent(t *testing.T) {
	const goroutines, iterations = 100, 300
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()

			for range iterations {
				s := randomString(32)
				if len(s) != 32 {
					t.Errorf("expected length 32, got %d (%q)", len(s), s)
					return
				}

				for _, r := range s {
					if !strings.ContainsRune(randomStringCharset, r) {
						t.Errorf("char %q not in charset (%q)", r, s)
						return
					}
				}
			}
		}()
	}

	wg.Wait()
}
