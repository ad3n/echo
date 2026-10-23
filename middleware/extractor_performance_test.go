package middleware

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/ad3n/echo/v5"
)

func BenchmarkCookieExtractor(b *testing.B) {
	for _, name := range []string{"session", "missing", ""} {
		b.Run(name, func(b *testing.B) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Cookie", "a=1; b=2; c=3; d=4; session=token; e=5; f=6; g=7")
			c := echo.New().NewContext(req, nil)
			extract := valuesFromCookie(name, 1)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _, _ = extract(c)
			}
		})
	}
}

func BenchmarkCreateExtractors(b *testing.B) {
	for range b.N {
		_, err := CreateExtractors("header:Authorization:Bearer ,query:token,cookie:session,param:key", 1)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func FuzzCookieExtractorCompatibility(f *testing.F) {
	for _, seed := range [][2]string{{"a=1; session=abc; session=def", "session"}, {`session="quoted"; bad=invalid value`, "session"}, {"a=1", "missing"}, {"=empty; a=1", ""}} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, header, name string) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Cookie", header)
		c := echo.New().NewContext(req, nil)
		for _, limit := range []uint{0, 1, 2, 20} {
			effective := max(limit, 1)
			var expected []string
			for _, cookie := range c.Cookies() {
				if cookie.Name != name {
					continue
				}

				expected = append(expected, cookie.Value)
				if uint(len(expected)) >= effective {
					break
				}
			}

			actual, source, err := valuesFromCookie(name, limit)(c)
			if !reflect.DeepEqual(actual, expected) || source != ExtractorSourceCookie || (err != nil) != (len(expected) == 0) {
				t.Fatalf("got %#v, %v, %v; want %#v", actual, source, err, expected)
			}
		}
	})
}
