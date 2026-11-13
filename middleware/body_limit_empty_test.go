package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ad3n/echo/v5"
)

func TestBodyLimitEmptyBodyCompatibility(t *testing.T) {
	for _, body := range []io.ReadCloser{nil, http.NoBody} {
		for _, limit := range []int64{0, 16} {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Body = body
			c := echo.New().NewContext(req, httptest.NewRecorder())
			h := BodyLimit(limit)(func(*echo.Context) error { return nil })
			if err := h(c); err != nil {
				t.Fatal(err)
			}

			reference := &limitedReader{reader: http.NoBody, limit: limit}
			for _, size := range []int{0, 1, 0, 16} {
				gotN, gotErr := req.Body.Read(make([]byte, size))
				wantN, wantErr := reference.Read(make([]byte, size))
				if gotN != wantN || gotErr != wantErr {
					t.Fatalf("size %d: got %d/%v, want %d/%v", size, gotN, gotErr, wantN, wantErr)
				}
			}

			if err := req.Body.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestBodyLimitDoesNotTrustZeroContentLength(t *testing.T) {
	for _, length := range []int64{0, -1} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("oversized"))
		req.ContentLength = length
		c := echo.New().NewContext(req, httptest.NewRecorder())
		err := BodyLimit(3)(func(c *echo.Context) error {
			data, readErr := io.ReadAll(c.Request().Body)
			if string(data) != "ove" {
				t.Fatalf("limit bypass: %q", data)
			}

			return readErr
		})(c)
		if err != echo.ErrStatusRequestEntityTooLarge {
			t.Fatalf("expected size rejection, got %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.ContentLength = 4
	err := BodyLimit(3)(func(*echo.Context) error {
		t.Fatal("handler called for excessive declared size")
		return nil
	})(echo.New().NewContext(req, httptest.NewRecorder()))
	if err != echo.ErrStatusRequestEntityTooLarge {
		t.Fatalf("expected declared size rejection, got %v", err)
	}
}

func TestBodyLimitEmptyBodyZeroAllocations(t *testing.T) {
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	h := BodyLimit(16)(func(*echo.Context) error { return nil })
	allocations := testing.AllocsPerRun(100, func() {
		c.Request().Body = http.NoBody
		if err := h(c); err != nil {
			t.Fatal(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("empty-body middleware allocated %g objects", allocations)
	}
}

func FuzzBodyLimitFastPath(f *testing.F) {
	f.Add([]byte("payload"), uint16(3), uint8(0))
	f.Add([]byte("payload"), uint16(3), uint8(2))
	f.Add([]byte("payload"), uint16(3), uint8(8))
	f.Add([]byte{}, uint16(0), uint8(1))
	f.Fuzz(func(t *testing.T, data []byte, limit uint16, mode uint8) {
		if len(data) > 16384 {
			t.Skip()
		}

		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
		expected := data
		switch mode % 3 {
		case 0:
			req.Body = nil
			expected = nil
		case 1:
			req.Body = http.NoBody
			expected = nil
		default:
			req.Body = io.NopCloser(bytes.NewReader(data))
		}

		switch (mode / 3) % 3 {
		case 0:
			req.ContentLength = 0
		case 1:
			req.ContentLength = -1
		default:
			req.ContentLength = int64(len(data))
		}

		called := false
		var got []byte
		err := BodyLimit(int64(limit))(func(c *echo.Context) error {
			called = true
			n, readErr := c.Request().Body.Read(nil)
			if n != 0 || readErr != nil {
				t.Fatalf("empty read: %d, %v", n, readErr)
			}

			got, readErr = io.ReadAll(c.Request().Body)
			return readErr
		})(echo.New().NewContext(req, httptest.NewRecorder()))
		if req.ContentLength > int64(limit) {
			if called || err != echo.ErrStatusRequestEntityTooLarge {
				t.Fatalf("declared size limit bypass: called=%v error=%v", called, err)
			}

			return
		}

		overflow := len(expected) > int(limit)
		if overflow {
			expected = expected[:limit]
			if err != echo.ErrStatusRequestEntityTooLarge {
				t.Fatalf("body limit bypass: %v", err)
			}
		}

		if !overflow && err != nil {
			t.Fatal(err)
		}

		if !called || !bytes.Equal(got, expected) {
			t.Fatalf("body mismatch: called=%v got=%q want=%q", called, got, expected)
		}

		if err := req.Body.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
