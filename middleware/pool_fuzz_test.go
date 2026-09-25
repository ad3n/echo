package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ad3n/echo/v5"
)

func FuzzDecompressLifecycle(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("invalid gzip"))
	compressed, err := gzipString("seed")
	if err != nil {
		f.Fatal(err)
	}

	f.Add(compressed)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}

		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
		req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
		h := DecompressWithConfig(DecompressConfig{MaxDecompressedSize: 1024})(func(c *echo.Context) error {
			_, err := io.Copy(io.Discard, c.Request().Body)
			return err
		})
		_ = h(echo.New().NewContext(req, httptest.NewRecorder()))
		_ = req.Body.Close()
		_ = req.Body.Close()
	})
}

func FuzzPoolMiddlewareRoundTrip(f *testing.F) {
	f.Add([]byte("seed"), uint8(3), false)
	f.Add([]byte{}, uint8(0), true)
	f.Fuzz(func(t *testing.T, payload []byte, threshold uint8, reverse bool) {
		if len(payload) > 64<<10 {
			t.Skip()
		}

		var compressed bytes.Buffer
		gw := gzip.NewWriter(&compressed)
		if _, err := gw.Write(payload); err != nil {
			t.Fatal(err)
		}

		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/", &compressed)
		req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
		req.Header.Set(echo.HeaderAcceptEncoding, gzipScheme)
		dump := BodyDumpWithConfig(BodyDumpConfig{
			MaxRequestBytes:  int64(threshold) + 1,
			MaxResponseBytes: int64(threshold) + 1,
			Handler:          func(*echo.Context, []byte, []byte, error) {},
		})
		h := GzipWithConfig(GzipConfig{MinLength: int(threshold)})(func(c *echo.Context) error {
			data, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return err
			}

			return c.Blob(http.StatusOK, echo.MIMEOctetStream, data)
		})
		if reverse {
			h = dump(Decompress()(h))
		}

		if !reverse {
			h = Decompress()(dump(h))
		}

		rec := httptest.NewRecorder()
		if err := h(echo.New().NewContext(req, rec)); err != nil {
			t.Fatal(err)
		}

		_ = req.Body.Close()
		var result io.Reader = rec.Body
		if rec.Header().Get(echo.HeaderContentEncoding) == gzipScheme {
			gr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer gr.Close()

			result = gr
		}

		decoded, err := io.ReadAll(result)
		if err != nil || !bytes.Equal(payload, decoded) {
			t.Fatalf("round-trip mismatch: input %d bytes, output %d bytes, error %v", len(payload), len(decoded), err)
		}
	})
}
