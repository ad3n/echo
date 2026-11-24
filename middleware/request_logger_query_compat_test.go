package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ad3n/echo/v5"
	"github.com/ad3n/echo/v5/middleware"
)

func TestRequestLoggerRetainsSelectedQueryValues(t *testing.T) {
	fields := make([]string, 24)
	pairs := make([]string, 24)
	for i := range fields {
		fields[i] = "key" + strconv.Itoa(i)
		pairs[i] = fields[i] + "=first&" + fields[i] + "=second"
	}

	var retained []map[string][]string
	e := echo.New()
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogQueryParams: fields,
		LogValuesFunc: func(_ *echo.Context, values middleware.RequestLoggerValues) error {
			retained = append(retained, values.QueryParams)
			return nil
		},
	}))
	e.GET("/", func(c *echo.Context) error {
		if values := c.QueryParams(); values.Has("key0") {
			values.Add("key0", "from handler")
		}

		return c.NoContent(http.StatusNoContent)
	})
	for _, path := range []string{"/?" + strings.Join(pairs, "&"), "/?other=value", "/?key0=new"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d", rec.Code)
		}
	}

	if len(retained) != 3 || len(retained[0]) != len(fields) || retained[1] == nil || len(retained[1]) != 0 {
		t.Fatalf("retained maps: %v", retained)
	}

	if !reflect.DeepEqual(retained[0]["key0"], []string{"first", "second", "from handler"}) ||
		!reflect.DeepEqual(retained[2]["key0"], []string{"new", "from handler"}) {
		t.Fatalf("retained values: %v, %v", retained[0]["key0"], retained[2]["key0"])
	}

	retained[1]["key0"] = []string{"added later"}
	if retained[0]["key0"][0] != "first" || retained[2]["key0"][0] != "new" {
		t.Fatal("log callbacks share a mutable map")
	}
}
