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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("development without a limiter: got %d, want %d", res.StatusCode, http.StatusBadRequest)
	}

	cfg.App.Env = "staging"
	app = newApp(cfg, nil, defaultAllowedOrigins, stubORM{}, nil, stubToken{}, nil)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	res, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("staging without a limiter: got %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestDevelopmentDoesNotRateLimitAuthRoutes(t *testing.T) {
	for _, env := range []string{"development", "production"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("APP_ENV", env)
			cfg := &config.Config{App: config.App{Env: env}, PostgresEnabled: true, JWTEnabled: true}
			app := newApp(cfg, nil, defaultAllowedOrigins, stubORM{}, rejectStore{}, stubToken{}, nil)
			for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/refresh"} {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{"))
				req.Header.Set("Content-Type", "application/json")
				res, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				want := http.StatusBadRequest
				if env != "development" {
					want = http.StatusTooManyRequests
				}
				if res.StatusCode != want {
					t.Fatalf("%s %s: got %d, want %d", env, path, res.StatusCode, want)
				}
			}
		})
	}
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("blank APP_ENV login: got %d, want %d", res.StatusCode, http.StatusTooManyRequests)
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
