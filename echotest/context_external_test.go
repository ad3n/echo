package echotest_test

import (
	"net/http"
	"testing"

	"github.com/ad3n/echo"
	"github.com/ad3n/echo/echotest"
	"github.com/stretchr/testify/assert"
)

func TestToContext_JSONBody(t *testing.T) {
	c := echotest.ContextConfig{
		JSONBody: echotest.LoadBytes(t, "testdata/test.json"),
	}.ToContext(t)

	payload := struct {
		Field string `json:"field"`
	}{}
	if err := c.Bind(&payload); err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "value", payload.Field)
	assert.Equal(t, http.MethodPost, c.Request().Method)
	assert.Equal(t, echo.MIMEApplicationJSON, c.Request().Header.Get(echo.HeaderContentType))
}
