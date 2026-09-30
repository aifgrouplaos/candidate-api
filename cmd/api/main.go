package main

import (
	"context"
	"log/slog"
	"os"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/adapter/jwt"
	minioadapter "github.com/BounkhongDev/bkgo/adapter/minio"
	"github.com/BounkhongDev/bkgo/adapter/redis"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/logger"
	"github.com/BounkhongDev/bkgo/middleware"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/gofiber/fiber/v2"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	log := logger.Development()
	if cfg.App.Env == "production" {
		log = logger.Production()
	}
	slog.SetDefault(log)

	// Composition root: construct adapters only when enabled, hold them as ports.
	var (
		db    contract.ORM
		cache contract.Cache
		store contract.Storage
		token contract.Token
	)

	if cfg.PostgresEnabled {
		gormDB, err := gormadapter.New(cfg.Postgres)
		if err != nil {
			slog.Error("postgres connect failed", "error", err)
			os.Exit(1)
		}
		defer gormDB.Close()
		db = gormDB

		if err := gormDB.Raw().AutoMigrate(&auth.User{}, &auth.RefreshToken{}); err != nil {
			slog.Error("automigrate failed", "error", err)
			os.Exit(1)
		}
	}

	if cfg.RedisEnabled {
		cache, err = redis.New(ctx, cfg.Redis)
		if err != nil {
			slog.Error("redis connect failed", "error", err)
			os.Exit(1)
		}
		defer cache.Close()
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

	app := fiber.New(fiber.Config{AppName: cfg.App.Name})
	app.Use(middleware.CORS())

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "app": cfg.App.Name})
	})

	api := app.Group("/api/v1")
	authMiddleware := auth.Authentication(token)
	if db != nil && token != nil {
		authHandler := auth.NewAuthHandler(auth.NewAuthUsecase(auth.NewAuthRepository(db), token))
		authHandler.RegisterRoutes(api, authMiddleware)
	}
	api = api.Group("", authMiddleware)

	// TODO: register module routes (repositories need DB_ENABLED=true)
	// userHandler := user.NewUserHandler(user.NewUserUsecase(user.NewUserRepository(db)))
	// userHandler.RegisterRoutes(api)
	_ = api
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
