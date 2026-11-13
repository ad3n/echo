package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ad3n/echo/v5"
)

type httpWorkloadFilter struct {
	Tenant   string `param:"tenant" json:"tenant"`
	Category string `query:"category" json:"category"`
	Search   string `query:"search" json:"search"`
	Sort     string `query:"sort" json:"sort"`
	Cursor   string `query:"cursor" json:"cursor"`
	Region   string `query:"region" json:"region"`
	Language string `query:"language" json:"language"`
	Status   string `query:"status" json:"status"`
	Limit    int    `query:"limit" json:"limit"`
	Page     int    `query:"page" json:"page"`
	Active   bool   `query:"active" json:"active"`
}

const httpWorkloadQuery = "/api/acme/items?category=books&search=distributed%20systems&sort=price&cursor=next&region=id&language=en&status=available&limit=25&page=2&active=true"
const httpWorkloadBody = `{"category":"books","search":"distributed systems","sort":"price","region":"id","language":"en","status":"available","limit":25,"page":2,"active":true}`

func newHTTPWorkload() *echo.Echo {
	e := echo.New()
	e.Use(Recover(), RequestID(), BodyLimit(1<<20))
	e.IPExtractor = echo.ExtractIPFromXFFHeader()
	handle := func(c *echo.Context) error {
		var filter httpWorkloadFilter
		if err := c.Bind(&filter); err != nil {
			return err
		}

		c.Response().Header().Set("X-Client-IP", c.RealIP())
		return c.JSON(http.StatusOK, &filter)
	}
	e.GET("/api/:tenant/items", handle)
	e.POST("/api/:tenant/items", handle)
	return e
}

func BenchmarkHTTPWorkload(b *testing.B) {
	for _, mode := range []string{"Handler", "KeepAlive", "KeepAlive32"} {
		b.Run(mode, func(b *testing.B) {
			e := newHTTPWorkload()
			var endpoint string
			client := &http.Client{}
			if mode != "Handler" {
				server := httptest.NewServer(e)
				defer server.Close()

				transport := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128, DisableCompression: true}
				defer transport.CloseIdleConnections()

				client.Transport = transport
				endpoint = server.URL
			}

			if mode == "KeepAlive32" {
				b.SetParallelism(8)
			}

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					method := http.MethodGet
					var body io.Reader
					if i%5 == 0 {
						method = http.MethodPost
						body = strings.NewReader(httpWorkloadBody)
					}

					i++
					req, err := http.NewRequest(method, endpoint+httpWorkloadQuery, body)
					if err != nil {
						b.Error(err)
						return
					}

					req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
					req.Header.Set(echo.HeaderXForwardedFor, "203.0.113.5, 10.0.0.2")
					if mode == "Handler" {
						req.RemoteAddr = "10.0.0.1:43210"
						rec := httptest.NewRecorder()
						e.ServeHTTP(rec, req)
						if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"tenant":"acme"`)) {
							b.Error("unexpected handler response")
							return
						}

						continue
					}

					res, err := client.Do(req)
					if err != nil {
						b.Error(err)
						return
					}

					data, err := io.ReadAll(res.Body)
					res.Body.Close()
					if err != nil || res.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"tenant":"acme"`)) {
						b.Error(fmt.Errorf("unexpected HTTP response: status=%d error=%v", res.StatusCode, err))
						return
					}
				}
			})
		})
	}
}

func TestHTTPWorkloadBehavior(t *testing.T) {
	server := httptest.NewServer(newHTTPWorkload())
	defer server.Close()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			var body io.Reader
			if method == http.MethodPost {
				body = strings.NewReader(httpWorkloadBody)
			}

			req, err := http.NewRequest(method, server.URL+httpWorkloadQuery, body)
			if err != nil {
				t.Fatal(err)
			}

			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			req.Header.Set(echo.HeaderXForwardedFor, "203.0.113.5, 10.0.0.2")
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()

			var got httpWorkloadFilter
			if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}

			want := httpWorkloadFilter{
				Tenant: "acme", Category: "books", Search: "distributed systems", Sort: "price",
				Region: "id", Language: "en", Status: "available", Limit: 25, Page: 2, Active: true,
			}
			if method == http.MethodGet {
				want.Cursor = "next"
			}

			if got != want || res.StatusCode != http.StatusOK || res.Header.Get("X-Client-IP") != "203.0.113.5" || len(res.Header.Get(echo.HeaderXRequestID)) != 32 {
				t.Fatalf("unexpected response: status=%d body=%+v headers=%v", res.StatusCode, got, res.Header)
			}
		})
	}
}
