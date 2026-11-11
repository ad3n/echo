package middleware

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ad3n/echo/v5"
	"github.com/stretchr/testify/require"
)

func TestBasicAuthCredentialOwnership(t *testing.T) {
	for _, size := range []int{0, 1, 250, 251, 252, 253, 254, 255, 256, 257, 4096} {
		user := strings.Repeat("u", size)
		password := "password:with:colons\x00\xff"
		var users, passwords []string
		h := BasicAuth(func(_ *echo.Context, u, p string) (bool, error) {
			users = append(users, u)
			passwords = append(passwords, p)
			return true, nil
		})(func(*echo.Context) error { return nil })
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		for range 20 {
			req.SetBasicAuth(user, password)
			require.NoError(t, h(c))
			req.SetBasicAuth("different-user", "different-password")
			require.NoError(t, h(c))
		}

		for i := 0; i < len(users); i += 2 {
			require.Equal(t, user, users[i])
			require.Equal(t, password, passwords[i])
			require.Equal(t, "different-user", users[i+1])
			require.Equal(t, "different-password", passwords[i+1])
		}
	}
}

func FuzzBasicAuthDecodeCompatibility(f *testing.F) {
	for _, input := range []string{"", "!", "dTpw", "Og==", "dTo=", "OnA=", "dXNlcg==", "dTpw\r\n", "dTpw=", "dTpw\xff"} {
		f.Add(input)
	}

	for _, size := range []int{251, 252, 253, 254, 255, 256, 257, 4096} {
		f.Add(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("u", size) + ":p")))
	}

	f.Fuzz(func(t *testing.T, encoded string) {
		if len(encoded) > 65536 {
			t.Skip()
		}

		decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
		user, password, found := bytes.Cut(decoded, []byte{':'})
		called, nextCalled := false, false
		h := BasicAuth(func(_ *echo.Context, u, p string) (bool, error) {
			called = true
			require.Equal(t, string(user), u)
			require.Equal(t, string(password), p)
			return true, nil
		})(func(*echo.Context) error { nextCalled = true; return nil })
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(echo.HeaderAuthorization, "Basic "+encoded)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		err := h(c)
		if encoded != "" && decodeErr != nil {
			require.False(t, called)
			require.False(t, nextCalled)
			require.True(t, errors.Is(err, decodeErr))
			var httpErr *echo.HTTPError
			require.ErrorAs(t, err, &httpErr)
			require.Equal(t, http.StatusBadRequest, httpErr.Code)
			return
		}

		if !found {
			require.Same(t, echo.ErrUnauthorized, err)
			require.False(t, called)
			require.False(t, nextCalled)
			return
		}

		require.NoError(t, err)
		require.True(t, called)
		require.True(t, nextCalled)
	})
}
