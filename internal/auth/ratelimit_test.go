package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestClientIP(t *testing.T) {
	for name, tc := range map[string]struct {
		trusted []string
		header  string
		want    string
	}{
		"trusted proxy uses last forwarded IP": {[]string{"0.0.0.0"}, "1.2.3.4, 203.0.113.7", "203.0.113.7"},
		"untrusted peer ignores header":        {nil, "1.2.3.4, 203.0.113.7", "0.0.0.0"},
		"trusted proxy without header":         {[]string{"0.0.0.0"}, "", "0.0.0.0"},
	} {
		t.Run(name, func(t *testing.T) {
			app := fiber.New(fiber.Config{EnableTrustedProxyCheck: true, TrustedProxies: tc.trusted, ProxyHeader: fiber.HeaderXForwardedFor, EnableIPValidation: true})
			app.Get("/", func(c *fiber.Ctx) error { return c.SendString(clientIP(c)) })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				request.Header.Set(fiber.HeaderXForwardedFor, tc.header)
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if string(body) != tc.want {
				t.Fatalf("clientIP = %q, want %q", body, tc.want)
			}
		})
	}
}
