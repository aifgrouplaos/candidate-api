package apidocs

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRegisterServesDocsAndContract(t *testing.T) {
	app := fiber.New()
	Register(app)

	for path, want := range map[string]struct{ contentType, body string }{
		"/docs":         {"text/html", "/openapi.yaml"},
		"/openapi.yaml": {"application/yaml", "openapi: 3.1.0"},
	} {
		response, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusOK {
			t.Errorf("%s status = %d", path, response.StatusCode)
		}
		if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, want.contentType) {
			t.Errorf("%s content type = %q", path, got)
		}
		if !strings.Contains(string(body), want.body) {
			t.Errorf("%s body missing %q", path, want.body)
		}
	}
}
