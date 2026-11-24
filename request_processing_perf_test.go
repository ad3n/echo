package echo

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type (
	checkedResponseWriter struct {
		nopResponseWriter
		code int
	}

	requestPathTarget struct {
		User int64 `param:"user"`
		Book int64 `param:"book"`
	}

	requestJSONTarget struct {
		Name   string `json:"name"`
		ID     int    `json:"id"`
		Active bool   `json:"active"`
	}
)

func BenchmarkRequestProcessing(b *testing.B) {
	b.Run("RouteStatic", func(b *testing.B) {
		e := New()
		e.GET("/healthz", func(c *Context) error { return c.NoContent(http.StatusNoContent) })
		benchCheckedRequest(b, e, httptest.NewRequest(http.MethodGet, "/healthz", nil), nil, "")
	})
	b.Run("PathStruct", func(b *testing.B) {
		e := New()
		e.GET("/users/:user/books/:book", func(c *Context) error {
			var target requestPathTarget
			if err := c.Bind(&target); err != nil {
				return err
			}

			if target.User != 42 || target.Book != 7 {
				return ErrInternalServerError
			}

			return c.NoContent(http.StatusNoContent)
		})
		benchCheckedRequest(b, e, httptest.NewRequest(http.MethodGet, "/users/42/books/7", nil), nil, "")
	})
	for _, workload := range []struct {
		name string
		keys int
	}{
		{"Query1Of5", 1},
		{"Query20Of20", 20},
	} {
		b.Run(workload.name, func(b *testing.B) {
			e := New()
			query := make([]string, max(5, workload.keys))
			keys := make([]string, len(query))
			for i := range query {
				keys[i] = "key" + strconv.Itoa(i)
				query[i] = keys[i] + "=" + strconv.Itoa(i+1)
			}

			e.GET("/search", func(c *Context) error {
				for i := range workload.keys {
					value, err := QueryParam[int](c, keys[i])
					if err != nil {
						return err
					}

					if value != i+1 {
						return ErrInternalServerError
					}
				}

				return c.NoContent(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/search?"+strings.Join(query, "&"), nil)
			benchCheckedRequest(b, e, req, nil, "")
		})
	}

	for _, workload := range []struct {
		name          string
		size          int
		unknownLength bool
	}{
		{"JSONSmall", 64, false},
		{"JSON4KiB", 4 << 10, false},
		{"JSON128KiB", 128 << 10, false},
		{"JSON128KiBUnknownLength", 128 << 10, true},
	} {
		b.Run(workload.name, func(b *testing.B) {
			e := New()
			body := `{"id":42,"active":true,"name":"` + strings.Repeat("a", workload.size) + `"}`
			e.POST("/users", func(c *Context) error {
				var target requestJSONTarget
				if err := c.Bind(&target); err != nil {
					return err
				}

				if target.ID != 42 || len(target.Name) != workload.size || !target.Active {
					return ErrInternalServerError
				}

				return c.NoContent(http.StatusNoContent)
			})
			reader := strings.NewReader(body)
			req := httptest.NewRequest(http.MethodPost, "/users", reader)
			req.Header.Set(HeaderContentType, MIMEApplicationJSON)
			req.Body = nopReadCloser{reader}
			if workload.unknownLength {
				req.ContentLength = -1
			}

			b.SetBytes(int64(len(body)))
			benchCheckedRequest(b, e, req, reader, body)
		})
	}
}

func (w *checkedResponseWriter) WriteHeader(code int) {
	w.code = code
}

func benchCheckedRequest(b *testing.B, e *Echo, req *http.Request, reader *strings.Reader, body string) {
	b.Helper()
	w := &checkedResponseWriter{}
	b.ReportAllocs()
	for b.Loop() {
		if reader != nil {
			reader.Reset(body)
		}

		w.code = 0
		e.ServeHTTP(w, req)
		if w.code != http.StatusNoContent {
			b.Fatalf("status %d", w.code)
		}
	}
}

func BenchmarkRequestProcessingHTTP(b *testing.B) {
	e := New()
	const size = 128 << 10
	body := `{"name":"` + strings.Repeat("a", size) + `"}`
	e.POST("/users/:user/books/:book", func(c *Context) error {
		var target struct {
			Name string `json:"name"`
			User int64  `param:"user"`
			Book int64  `param:"book"`
		}
		if err := c.Bind(&target); err != nil {
			return err
		}

		limit, err := QueryParam[int](c, "limit")
		if err != nil {
			return err
		}

		if target.User != 42 || target.Book != 7 || limit != 5 || len(target.Name) != size {
			return ErrInternalServerError
		}

		return c.NoContent(http.StatusNoContent)
	})
	server := httptest.NewServer(e)
	defer server.Close()

	client := server.Client()
	endpoint := server.URL + "/users/42/books/7?limit=5&offset=10&sort=asc"
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		res, err := client.Post(endpoint, MIMEApplicationJSON, strings.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}

		if err := res.Body.Close(); err != nil {
			b.Fatal(err)
		}

		if res.StatusCode != http.StatusNoContent {
			b.Fatalf("status %d", res.StatusCode)
		}
	}
}
