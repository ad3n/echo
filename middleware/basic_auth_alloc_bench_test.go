package middleware

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ad3n/echo/v5"
)

func BenchmarkBasicAuthDecode(b *testing.B) {
	for _, name := range []string{"short", "long", "invalid", "missing_colon", "parallel"} {
		b.Run(name, func(b *testing.B) {
			user, password := "benchmark-user", "benchmark-password"
			if name == "long" {
				password = strings.Repeat("password", 128)
			}

			header := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
			if name == "invalid" {
				header = "Basic !!invalid!!"
			}

			if name == "missing_colon" {
				header = "Basic " + base64.StdEncoding.EncodeToString([]byte("credentials"))
			}

			e := echo.New()
			h := BasicAuth(func(_ *echo.Context, u, p string) (bool, error) {
				validUser := subtle.ConstantTimeCompare([]byte(u), []byte(user))
				validPassword := subtle.ConstantTimeCompare([]byte(p), []byte(password))
				return validUser&validPassword == 1, nil
			})(func(*echo.Context) error { return nil })
			run := func(loop func() bool) {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set(echo.HeaderAuthorization, header)
				c := e.NewContext(req, &poolBenchWriter{header: make(http.Header)})
				for loop() {
					_ = h(c)
				}
			}
			b.ReportAllocs()
			if name == "parallel" {
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) { run(pb.Next) })
				return
			}

			run(func() bool { return b.Loop() })
		})
	}
}
