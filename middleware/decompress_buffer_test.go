package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ad3n/echo/v5"
	"github.com/stretchr/testify/require"
)

func TestGzipInputReleaseDetachesSource(t *testing.T) {
	compressed, err := gzipString("private request body")
	require.NoError(t, err)
	for _, valid := range []bool{true, false} {
		data := compressed
		if !valid {
			data = []byte("invalid gzip header")
		}

		source := bytes.NewReader(data)
		input := newGzipInput(struct{ io.Reader }{source})
		buffer, ok := input.Reader.(*gzipInputBuffer)
		require.True(t, ok)
		require.NotNil(t, buffer)
		reader := new(gzip.Reader)
		err := reader.Reset(input)
		if valid {
			require.NoError(t, err)
		}

		if !valid {
			require.Error(t, err)
		}

		releaseGzipReader(&sync.Pool{}, reader, input)
		require.IsType(t, gzipEOFReader{}, input.Reader)
		_, err = input.Read(make([]byte, 1))
		require.ErrorIs(t, err, io.EOF)
		_, err = input.ReadByte()
		require.ErrorIs(t, err, io.EOF)
	}
}

func TestDecompressBufferedOwnership(t *testing.T) {
	for _, mode := range []string{"eof", "early_close", "limit", "multistream"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			payload := strings.Repeat(mode, 1000)
			compressed, err := gzipString(payload)
			require.NoError(t, err)
			if mode == "multistream" {
				compressed = append(compressed, compressed...)
				payload += payload
			}

			config := DecompressConfig{}
			if mode == "limit" {
				config.MaxDecompressedSize = 1
			}

			h := DecompressWithConfig(config)(func(*echo.Context) error { return nil })
			e := echo.New()
			var retained []io.ReadCloser
			for range 20 {
				body := io.NopCloser(struct{ io.Reader }{bytes.NewReader(compressed)})
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set(echo.HeaderContentEncoding, GZIPEncoding)
				c := e.NewContext(req, httptest.NewRecorder())
				require.NoError(t, h(c))
				retained = append(retained, req.Body)
			}

			for _, body := range retained {
				if mode == "early_close" {
					require.NoError(t, body.Close())
					_, err := body.Read(make([]byte, 1))
					require.ErrorIs(t, err, http.ErrBodyReadAfterClose)
					continue
				}

				data, err := io.ReadAll(body)
				if mode == "limit" {
					require.ErrorIs(t, err, echo.ErrStatusRequestEntityTooLarge)
					require.Equal(t, payload[:1], string(data))
				}

				if mode != "limit" {
					require.NoError(t, err)
					require.Equal(t, payload, string(data))
				}

				require.NoError(t, body.Close())
				require.NoError(t, body.Close())
			}
		})
	}
}

func TestGzipInputPreservesCallerBuffer(t *testing.T) {
	source := bufio.NewReader(strings.NewReader("caller-owned unread data"))
	input := newGzipInput(source)
	require.Same(t, source, input.Reader)
	require.Same(t, source, input.ByteReader)
	releaseGzipReader(&sync.Pool{}, new(gzip.Reader), input)
	data, err := io.ReadAll(source)
	require.NoError(t, err)
	require.Equal(t, "caller-owned unread data", string(data))
}
