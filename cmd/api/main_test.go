package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BounkhongDev/bkgo/config"
	"github.com/BounkhongDev/bkgo/contract"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type stubORM struct{}

func (stubORM) Session(context.Context) *gorm.DB                        { return nil }
func (stubORM) Transaction(context.Context, func(*gorm.DB) error) error { return nil }
func (stubORM) Close() error                                            { return nil }

type stubToken struct{}

func (stubToken) Sign(contract.Claims, time.Duration) (string, error) {
	return "", nil
}
func (stubToken) Verify(string) (contract.Claims, error) { return nil, context.Canceled }

// rejectStore exceeds every bucket, so a mounted limit responds 429 before the handler.
type rejectStore struct{}

func (rejectStore) Hit(context.Context, string, int64, time.Duration) (bool, time.Duration, error) {
	return true, time.Second, nil
}

func TestDevelopmentRegistersAuthRoutesWithoutLimiter(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	cfg := &config.Config{App: config.App{Env: "development"}, PostgresEnabled: true, JWTEnabled: true}
	app := newApp(cfg, nil, defaultAllowedOrigins, stubORM{}, nil, stubToken{}, nil)
	if got := postMalformedJSON(t, app, "/api/v1/auth/login"); got != http.StatusBadRequest {
		t.Fatalf("development without a limiter: got %d, want %d", got, http.StatusBadRequest)
	}

	cfg.App.Env = "staging"
	app = newApp(cfg, nil, defaultAllowedOrigins, stubORM{}, nil, stubToken{}, nil)
	if got := postMalformedJSON(t, app, "/api/v1/auth/login"); got != http.StatusNotFound {
		t.Fatalf("staging without a limiter: got %d, want %d", got, http.StatusNotFound)
	}
}

func TestDevelopmentDoesNotRateLimitAuthRoutes(t *testing.T) {
	for env, want := range map[string]int{"development": http.StatusBadRequest, "production": http.StatusTooManyRequests} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("APP_ENV", env)
			cfg := &config.Config{App: config.App{Env: env}, PostgresEnabled: true, JWTEnabled: true}
			app := newApp(cfg, nil, defaultAllowedOrigins, stubORM{}, rejectStore{}, stubToken{}, nil)
			for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/refresh"} {
				if got := postMalformedJSON(t, app, path); got != want {
					t.Fatalf("%s %s: got %d, want %d", env, path, got, want)
				}
			}
		})
	}
}

// postMalformedJSON returns the status for a POST with an unparseable JSON body.
func postMalformedJSON(t *testing.T, app *fiber.App, path string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestUnsetAppEnvKeepsRateLimits(t *testing.T) {
	t.Setenv("APP_ENV", "")
	cfg := &config.Config{
		App:             config.App{Env: "development"},
		PostgresEnabled: true,
		JWTEnabled:      true,
		JWT:             config.JWT{Secret: strings.Repeat("k", 32)},
	}
	if err := validateConfig(cfg, nil); err == nil {
		t.Fatal("blank APP_ENV was treated as development and allowed to start without Redis")
	}
	app := newApp(cfg, nil, defaultAllowedOrigins, stubORM{}, rejectStore{}, stubToken{}, nil)
	if got := postMalformedJSON(t, app, "/api/v1/auth/login"); got != http.StatusTooManyRequests {
		t.Fatalf("blank APP_ENV login: got %d, want %d", got, http.StatusTooManyRequests)
	}
}

func TestDevelopmentAllowsAuthenticatedRoutesWithoutRedis(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	secret := strings.Repeat("k", 32)
	cfg := &config.Config{
		App:             config.App{Env: "development"},
		PostgresEnabled: true,
		JWTEnabled:      true,
		JWT:             config.JWT{Secret: secret},
	}
	if err := validateConfig(cfg, nil); err != nil {
		t.Fatalf("development without Redis: %v", err)
	}
	for _, env := range []string{"", "dev", "local", "staging", "production"} {
		cfg.App.Env = env
		proxies := []string(nil)
		if env == "production" {
			proxies = []string{"10.0.0.1"}
		}
		if err := validateConfig(cfg, proxies); err == nil {
			t.Fatalf("%q without Redis was accepted", env)
		}
	}
}

func TestProductionRequiresObjectStorage(t *testing.T) {
	cfg := &config.Config{
		App:             config.App{Env: "production"},
		PostgresEnabled: true,
		JWTEnabled:      true,
		JWT:             config.JWT{Secret: strings.Repeat("k", 32)},
		RedisEnabled:    true,
	}
	if err := validateConfig(cfg, []string{"10.0.0.1"}); err == nil {
		t.Fatal("production without object storage was accepted")
	}
	cfg.MinIOEnabled = true
	if err := validateConfig(cfg, []string{"10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
}

func TestCORSPreflightAllowsOnlyConfiguredOrigins(t *testing.T) {
	app := newApp(&config.Config{}, nil, defaultAllowedOrigins, nil, nil, nil, nil)
	for origin, allowed := range map[string]bool{
		"http://localhost:3000": true,
		"http://localhost:5173": true,
		"https://evil.example":  false,
	} {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/projects", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,idempotency-key")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got := res.Header.Get("Access-Control-Allow-Origin"); (got == origin) != allowed {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, allowed = %v", origin, got, allowed)
		}
		if !allowed {
			continue
		}
		headers := strings.ToLower(res.Header.Get("Access-Control-Allow-Headers"))
		for _, h := range []string{"authorization", "content-type", "idempotency-key"} {
			if !strings.Contains(headers, h) {
				t.Errorf("%s: Access-Control-Allow-Headers %q lacks %s", origin, headers, h)
			}
		}
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	for value, want := range map[string]string{
		"":    defaultAllowedOrigins,
		"   ": defaultAllowedOrigins,
		"https://app.example.com, http://localhost:4200": "https://app.example.com,http://localhost:4200",
	} {
		if got, err := parseAllowedOrigins(value); err != nil || got != want {
			t.Errorf("parseAllowedOrigins(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"*", ",", "localhost:3000", "http://localhost:3000/", "ftp://example.com", "https://example.com?x=1", "https://user@example.com"} {
		if _, err := parseAllowedOrigins(value); err == nil {
			t.Errorf("parseAllowedOrigins(%q) accepted an invalid origin", value)
		}
	}
}
