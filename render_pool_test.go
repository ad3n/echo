// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type poolTestRenderer func(*Context, io.Writer, string, any) error

func (r poolTestRenderer) Render(c *Context, w io.Writer, name string, data any) error {
	return r(c, w, name, data)
}

func TestRenderBufferLifecycle(t *testing.T) {
	renderErr := errors.New("render failed")
	e := New()
	e.Renderer = poolTestRenderer(func(c *Context, w io.Writer, name string, data any) error {
		if _, err := io.WriteString(w, data.(string)); err != nil {
			return err
		}

		switch name {
		case "error":
			return renderErr
		case "panic":
			panic(renderErr)
		}

		return nil
	})

	for worker := range 8 {
		t.Run(string(rune('a'+worker)), func(t *testing.T) {
			t.Parallel()

			for _, name := range []string{"ok", "error", "ok", "panic", "ok", "large", "ok"} {
				body := "body-" + t.Name() + "-" + name
				if name == "large" {
					body = strings.Repeat(body, 8192)
				}

				rec := httptest.NewRecorder()
				rec.Code = 0
				c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)
				var err error
				var recovered any
				func() {
					defer func() { recovered = recover() }()

					err = c.Render(http.StatusCreated, name, body)
				}()

				switch name {
				case "error", "panic":
					if name == "error" && !errors.Is(err, renderErr) {
						t.Fatalf("got error %v, want %v", err, renderErr)
					}

					if name == "panic" && recovered != renderErr {
						t.Fatalf("got panic %v, want %v", recovered, renderErr)
					}

					if rec.Body.Len() != 0 || rec.Code != 0 {
						t.Fatal("failed render committed partial output")
					}
				default:
					if err != nil || recovered != nil {
						t.Fatalf("error=%v panic=%v", err, recovered)
					}

					if rec.Code != http.StatusCreated || rec.Body.String() != body {
						t.Fatal("render returned incorrect status or body")
					}

					if got := rec.Header().Get(HeaderContentType); got != MIMETextHTMLCharsetUTF8 {
						t.Fatalf("unexpected content type %q", got)
					}
				}
			}
		})
	}
}

func BenchmarkRenderBuffer(b *testing.B) {
	for _, size := range []int{128, 4096, 131072} {
		body := strings.Repeat("x", size)
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			e := New()
			e.Renderer = poolTestRenderer(func(c *Context, w io.Writer, name string, data any) error {
				_, err := io.WriteString(w, body)
				return err
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			w := &nopResponseWriter{}
			c := e.NewContext(req, w)
			if err := c.Render(http.StatusOK, "", nil); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				c.Reset(req, w)
				if err := c.Render(http.StatusOK, "", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type renderFailWriter struct {
	nopResponseWriter
	err error
}

func (w *renderFailWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestRenderBufferWriteError(t *testing.T) {
	writeErr := errors.New("write failed")
	e := New()
	e.Renderer = poolTestRenderer(func(c *Context, w io.Writer, name string, data any) error {
		_, err := io.WriteString(w, name)
		return err
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c := e.NewContext(req, &renderFailWriter{err: writeErr})
	if err := c.Render(http.StatusOK, "failed body", nil); !errors.Is(err, writeErr) {
		t.Fatalf("got %v, want %v", err, writeErr)
	}

	rec := httptest.NewRecorder()
	c.Reset(req, rec)
	if err := c.Render(http.StatusOK, "next body", nil); err != nil {
		t.Fatal(err)
	}

	if rec.Body.String() != "next body" {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}
