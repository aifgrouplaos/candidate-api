package ratelimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/gofiber/fiber/v2"
)

type fakeStore struct {
	exceeded bool
	retry    time.Duration
	err      error
}

func (s fakeStore) Hit(context.Context, string, int64, time.Duration) (bool, time.Duration, error) {
	return s.exceeded, s.retry, s.err
}

func TestNew(t *testing.T) {
	for name, tc := range map[string]struct {
		store      Store
		wantStatus int
		wantRetry  string
	}{
		"under limit passes":         {fakeStore{}, fiber.StatusOK, ""},
		"over limit rejects":         {fakeStore{exceeded: true, retry: 1500 * time.Millisecond}, fiber.StatusTooManyRequests, "2"},
		"retry rounds up to 1s":      {fakeStore{exceeded: true}, fiber.StatusTooManyRequests, "1"},
		"store error fails closed":   {fakeStore{err: errors.New("down")}, fiber.StatusInternalServerError, ""},
		"missing store fails closed": {nil, fiber.StatusInternalServerError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			fail := func(c *fiber.Ctx, err error) error {
				appErr, _ := errs.IsAppError(err)
				return c.SendStatus(appErr.Status)
			}
			buckets := func(*fiber.Ctx) []Bucket { return []Bucket{{Name: "test", Key: "k", Limit: 1, Window: time.Minute}} }
			app := fiber.New(fiber.Config{ErrorHandler: fail})
			app.Get("/", New(tc.store, buckets), func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
			response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.wantStatus || response.Header.Get("Retry-After") != tc.wantRetry {
				t.Fatalf("got status %d Retry-After %q, want %d %q", response.StatusCode, response.Header.Get("Retry-After"), tc.wantStatus, tc.wantRetry)
			}
		})
	}
}

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
			app.Get("/", func(c *fiber.Ctx) error { return c.SendString(ClientIP(c)) })
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
				t.Fatalf("ClientIP = %q, want %q", body, tc.want)
			}
		})
	}
}
