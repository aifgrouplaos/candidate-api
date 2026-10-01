package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/adapter/jwt"
	minioadapter "github.com/BounkhongDev/bkgo/adapter/minio"
	"github.com/BounkhongDev/bkgo/adapter/redis"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/logger"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/aifgrouplaos/candidate-api/pkg/ratelimit"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}
	trustedProxies, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return fmt.Errorf("trusted proxy configuration invalid: %w", err)
	}
	allowedOrigins, err := parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS"))
	if err != nil {
		return fmt.Errorf("allowed origins configuration invalid: %w", err)
	}

	log := logger.Development()
	if cfg.App.Env == "production" {
		log = logger.Production()
	}
	slog.SetDefault(log)
	if err := validateConfig(cfg, trustedProxies); err != nil {
		return err
	}

	// Composition root: construct adapters only when enabled, hold them as ports.
	var (
		db         contract.ORM
		cache      contract.Cache
		redisCache *redis.Cache
		store      contract.Storage
		token      contract.Token
	)

	if cfg.PostgresEnabled {
		gormDB, err := openPostgres(cfg.Postgres)
		if err != nil {
			return err
		}
		defer gormDB.Close()
		db = gormDB
	}

	if cfg.RedisEnabled {
		redisCache, err = redis.New(ctx, cfg.Redis)
		if err != nil {
			return fmt.Errorf("redis connect failed: %w", err)
		}
		cache = redisCache
		defer cache.Close()
	}

	if cfg.MinIOEnabled {
		store, err = minioadapter.New(ctx, cfg.MinIO)
		if err != nil {
			return fmt.Errorf("minio connect failed: %w", err)
		}
	}

	if cfg.JWTEnabled {
		token = jwt.New(cfg.JWT)
	}

	var limiter ratelimit.Store
	if redisCache != nil {
		limiter = ratelimit.NewRedisStore(redisCache.Client())
	}

	app := newApp(cfg, trustedProxies, allowedOrigins, db, limiter, token)

	_ = cache
	_ = store

	slog.Info("server starting",
		"port", cfg.App.Port,
		"env", cfg.App.Env,
		"postgres", cfg.PostgresEnabled,
		"redis", cfg.RedisEnabled,
		"minio", cfg.MinIOEnabled,
		"jwt", cfg.JWTEnabled,
	)
	return app.Listen(":" + cfg.App.Port)
}

func openPostgres(cfg config.Postgres) (*gormadapter.DB, error) {
	gormDB, err := gormadapter.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres connect failed: %w", err)
	}
	if err := gormDB.Raw().AutoMigrate(&auth.User{}, &auth.AuthSession{}, &auth.RefreshToken{}); err != nil {
		gormDB.Close()
		return nil, fmt.Errorf("automigrate failed: %w", err)
	}
	return gormDB, nil
}

func validateConfig(cfg *config.Config, trustedProxies []string) error {
	if cfg.App.Env == "production" && (!cfg.PostgresEnabled || !cfg.JWTEnabled || len([]byte(cfg.JWT.Secret)) < 32 || !cfg.RedisEnabled || len(trustedProxies) == 0) {
		return errors.New("production requires PostgreSQL, Redis, trusted proxy addresses, JWT, and a JWT secret of at least 32 bytes")
	}
	if cfg.PostgresEnabled && cfg.JWTEnabled && !cfg.RedisEnabled {
		return errors.New("Redis is required when authenticated API routes are enabled")
	}
	return nil
}

func newApp(cfg *config.Config, trustedProxies []string, allowedOrigins string, db contract.ORM, limiter ratelimit.Store, token contract.Token) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:                 cfg.App.Name,
		EnableTrustedProxyCheck: true,
		TrustedProxies:          trustedProxies,
		ProxyHeader:             "X-Forwarded-For",
		EnableIPValidation:      true,
		ErrorHandler:            httpresponse.Error,
	})
	app.Use(cors.New(cors.Config{
		AllowOrigins:  allowedOrigins,
		AllowMethods:  "GET,POST,PATCH,DELETE,OPTIONS",
		AllowHeaders:  "Origin,Content-Type,Authorization,Accept-Language,Idempotency-Key,X-Request-Id",
		ExposeHeaders: "X-Request-Id",
	}))

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "app": cfg.App.Name})
	})

	api := app.Group("/api/v1")
	authRepository := auth.NewAuthRepository(db)
	authMiddleware := auth.Authentication(token, authRepository)
	protected := []fiber.Handler{authMiddleware, ratelimit.PerUser(limiter, auth.UserID)}
	if db != nil && token != nil && limiter != nil {
		authHandler := auth.NewAuthHandler(auth.NewAuthUsecase(authRepository, token))
		authHandler.RegisterRoutes(api, auth.LoginRateLimit(limiter), auth.RefreshRateLimit(limiter), protected...)
	}

	// TODO: register module routes (repositories need DB_ENABLED=true)
	// userHandler := user.NewUserHandler(user.NewUserUsecase(user.NewUserRepository(db)))
	// userHandler.RegisterRoutes(api, protected...)
	return app
}

func parseTrustedProxies(value string) ([]string, error) {
	var proxies []string
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if net.ParseIP(candidate) == nil {
			if _, _, err := net.ParseCIDR(candidate); err != nil {
				return nil, err
			}
		}
		proxies = append(proxies, candidate)
	}
	return proxies, nil
}

// defaultAllowedOrigins are the candidate frontends from API_SPEC; the hosted API
// serves them too, so they are the default in every environment.
const defaultAllowedOrigins = "http://localhost:3000,http://localhost:5173"

// parseAllowedOrigins replaces the default list and accepts only bare http(s) origins.
func parseAllowedOrigins(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return defaultAllowedOrigins, nil
	}
	var origins []string
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		u, err := url.Parse(candidate)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("%q must be an http(s) origin such as https://app.example.com", candidate)
		}
		origins = append(origins, candidate)
	}
	if len(origins) == 0 {
		return "", errors.New("at least one origin is required; an empty Fiber CORS list allows every origin")
	}
	return strings.Join(origins, ","), nil
}
