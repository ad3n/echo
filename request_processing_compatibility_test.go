package echo_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	echo "github.com/ad3n/echo/v5"
	"github.com/ad3n/echo/v5/middleware"
)

type (
	retainedPathValues struct {
		values []string
	}

	failingJSONReader struct {
		io.Reader
		err error
	}
)

func (r *retainedPathValues) UnmarshalParams(values []string) error {
	r.values = values
	return nil
}

func (r failingJSONReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, r.err
	}

	return n, err
}

func TestQueryScalarUsesParsedQuerySnapshot(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?n=%ZZ&n=7&n=8&empty=&bad=qux;z=1", nil)
	c := e.NewContext(req, httptest.NewRecorder())
	n, err := echo.QueryParam[int](c, "n")
	if n != 7 || err != nil {
		t.Fatalf("first value: %d, %v", n, err)
	}

	req.URL.RawQuery = "n=12"
	if got := c.QueryParam("n"); got != "7" {
		t.Fatalf("snapshot lost: %q", got)
	}

	values := c.QueryParams()
	values.Set("n", "18")
	n, err = echo.QueryParam[int](c, "n")
	if n != 18 || err != nil {
		t.Fatalf("mutable values: %d, %v", n, err)
	}

	n, err = echo.QueryParamOr(c, "empty", 99)
	if n != 99 || err != nil {
		t.Fatalf("empty default: %d, %v", n, err)
	}

	if _, err = echo.QueryParam[int](c, "bad"); !errors.Is(err, echo.ErrNonExistentKey) {
		t.Fatalf("invalid URL pair: %v", err)
	}

	values.Set("n", "not-a-number")
	_, err = echo.QueryParam[int](c, "n")
	var bindingErr *echo.BindingError
	var numberErr *strconv.NumError
	if !errors.As(err, &bindingErr) || bindingErr.Field != "n" || !errors.As(err, &numberErr) {
		t.Fatalf("conversion error: %v", err)
	}

	c.Reset(httptest.NewRequest(http.MethodGet, "/?n=23", nil), httptest.NewRecorder())
	n, err = echo.QueryParam[int](c, "n")
	if n != 23 || err != nil {
		t.Fatalf("reset query: %d, %v", n, err)
	}
}

func TestQueryScalarRespectsStandardQueryLimit(t *testing.T) {
	t.Setenv("GODEBUG", "urlmaxqueryparams=2")
	raw := "first=42&second=12&third=23"
	_, err := url.ParseQuery(raw)
	if err == nil {
		t.Fatal("standard URL parser accepted query above configured limit")
	}

	req := httptest.NewRequest(http.MethodGet, "/?"+raw, nil)
	ctx := echo.New().NewContext(req, httptest.NewRecorder())
	if _, err := echo.QueryParam[int](ctx, "first"); !errors.Is(err, echo.ErrNonExistentKey) {
		t.Fatalf("query above limit: %v", err)
	}
}

func TestScalarPathBindingPreservesValuesAndErrors(t *testing.T) {
	e := echo.New()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	var scalar struct {
		ID   int `param:"id"`
		Page int `param:"Page"`
	}

	ctx.SetPathValues(echo.PathValues{{Name: "id", Value: "1"}, {Name: "id", Value: "2"}, {Name: "page", Value: "5"}})
	if err := echo.BindPathValues(ctx, &scalar); err != nil || scalar.ID != 2 || scalar.Page != 5 {
		t.Fatalf("duplicate and folded tags: %+v, %v", scalar, err)
	}

	ctx.SetPathValues(echo.PathValues{{Name: "id", Value: "3"}, {Name: "ID", Value: "4"}, {Name: "page", Value: "bad"}})
	err := echo.BindPathValues(ctx, &scalar)
	var numberErr *strconv.NumError
	if scalar.ID != 3 || scalar.Page != 5 || echo.StatusCode(err) != http.StatusBadRequest || !errors.As(err, &numberErr) {
		t.Fatalf("exact tag and field error: %+v, %v", scalar, err)
	}

	ctx.SetPathValues(echo.PathValues{{Name: "id", Value: "bad"}})
	err = echo.BindPathValues(ctx, &scalar)
	if echo.StatusCode(err) != http.StatusBadRequest || !errors.As(err, &numberErr) || !strings.Contains(err.Error(), "id") {
		t.Fatalf("scalar field error: %v", err)
	}

	var custom struct {
		IDs retainedPathValues `param:"id"`
	}
	ctx.SetPathValues(echo.PathValues{{Name: "id", Value: "retained"}})
	if err := echo.BindPathValues(ctx, &custom); err != nil {
		t.Fatal(err)
	}

	ctx.Reset(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	ctx.SetPathValues(echo.PathValues{{Name: "id", Value: "another request"}})
	if len(custom.IDs.values) != 1 || custom.IDs.values[0] != "retained" {
		t.Fatalf("custom unmarshaler retained %v", custom.IDs.values)
	}
}

func TestJSONBindingWithSizeHint(t *testing.T) {
	name := strings.Repeat("x", 128<<10)
	body := `{"name":"` + name + `"}`
	for _, tc := range []struct {
		name   string
		body   io.Reader
		size   int64
		limit  int64
		status int
	}{
		{"known", strings.NewReader(body), int64(len(body)), 256 << 10, 204},
		{"unknown", strings.NewReader(body), -1, 256 << 10, 204},
		{"shorterThanHint", iotest.DataErrReader(strings.NewReader(body)), 256 << 10, 256 << 10, 204},
		{"longerThanHint", strings.NewReader(body), 72 << 10, 256 << 10, 204},
		{"trailingJSON", strings.NewReader(body + "{}"), int64(len(body) + 2), 256 << 10, 400},
		{"smallMalformedBodyWithLargeHint", strings.NewReader("{"), int64(len(body)), 256 << 10, 400},
		{"earlyBodyError", failingJSONReader{strings.NewReader("{"), echo.ErrStatusRequestEntityTooLarge}, int64(len(body)), 256 << 10, 413},
		{"bodyLimitAfterProbe", strings.NewReader(body), 72 << 10, 96 << 10, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			e.Use(middleware.BodyLimit(tc.limit))
			e.POST("/", func(c *echo.Context) error {
				var data struct{ Name string }
				if err := c.Bind(&data); err != nil {
					return err
				}

				if data.Name != name {
					t.Errorf("decoded %d characters, want %d", len(data.Name), len(name))
				}

				return c.NoContent(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/", tc.body)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			req.ContentLength = tc.size
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
		})
	}
}
