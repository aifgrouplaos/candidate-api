package main

import (
	"context"
	"log/slog"
	"net"
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
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	trustedProxies, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		slog.Error("trusted proxy configuration invalid", "error", err)
		os.Exit(1)
	}

	log := logger.Development()
	if cfg.App.Env == "production" {
		log = logger.Production()
	}
	slog.SetDefault(log)
	if cfg.App.Env == "production" && (!cfg.PostgresEnabled || !cfg.JWTEnabled || len([]byte(cfg.JWT.Secret)) < 32 || !cfg.RedisEnabled || len(trustedProxies) == 0) {
		slog.Error("production requires PostgreSQL, Redis, trusted proxy addresses, JWT, and a JWT secret of at least 32 bytes")
		os.Exit(1)
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
		gormDB, err := gormadapter.New(cfg.Postgres)
		if err != nil {
			slog.Error("postgres connect failed", "error", err)
			os.Exit(1)
		}
		defer gormDB.Close()
		db = gormDB

		if err := gormDB.Raw().AutoMigrate(&auth.User{}, &auth.AuthSession{}, &auth.RefreshToken{}); err != nil {
			slog.Error("automigrate failed", "error", err)
			os.Exit(1)
		}
	}

	if cfg.RedisEnabled {
		redisCache, err = redis.New(ctx, cfg.Redis)
		if err != nil {
			slog.Error("redis connect failed", "error", err)
			os.Exit(1)
		}
		cache = redisCache
		defer cache.Close()
	}
	if cfg.PostgresEnabled && cfg.JWTEnabled && redisCache == nil {
		slog.Error("Redis is required when authenticated API routes are enabled")
		os.Exit(1)
	}

	if cfg.MinIOEnabled {
		store, err = minioadapter.New(ctx, cfg.MinIO)
		if err != nil {
			slog.Error("minio connect failed", "error", err)
			os.Exit(1)
		}
	}

	if cfg.JWTEnabled {
		token = jwt.New(cfg.JWT)
	}

	app := fiber.New(fiber.Config{
		AppName:                 cfg.App.Name,
		EnableTrustedProxyCheck: true,
		TrustedProxies:          trustedProxies,
		ProxyHeader:             "X-Forwarded-For",
		EnableIPValidation:      true,
	})
	app.Use(cors.New(cors.Config{
		AllowOrigins:  "http://localhost:3000,http://localhost:5173",
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
	protectedAPI := api.Group("", authMiddleware, auth.UserRateLimit(redisCache))
	if db != nil && token != nil && redisCache != nil {
		authHandler := auth.NewAuthHandler(auth.NewAuthUsecase(authRepository, token))
		authHandler.RegisterRoutes(api, protectedAPI, auth.LoginRateLimit(redisCache), auth.RefreshRateLimit(redisCache))
	}

	// TODO: register module routes (repositories need DB_ENABLED=true)
	// userHandler := user.NewUserHandler(user.NewUserUsecase(user.NewUserRepository(db)))
	// userHandler.RegisterRoutes(protectedAPI)
	_ = protectedAPI
	_ = db
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
	if err := app.Listen(":" + cfg.App.Port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
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
