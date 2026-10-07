package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/adapter/jwt"
	minioadapter "github.com/BounkhongDev/bkgo/adapter/minio"
	"github.com/BounkhongDev/bkgo/adapter/redis"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/logger"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/internal/chat"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
	"github.com/aifgrouplaos/candidate-api/internal/project"
	"github.com/aifgrouplaos/candidate-api/internal/tenant"
	"github.com/aifgrouplaos/candidate-api/pkg/apidocs"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/aifgrouplaos/candidate-api/pkg/ratelimit"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	miniogo "github.com/minio/minio-go/v7"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	provision, reset, err := parseFlags()
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}
	if provision != "" || reset != "" {
		return runTenantCommand(ctx, cfg, provision, reset)
	}
	trustedProxies, allowedOrigins, err := prepareServer(cfg)
	if err != nil {
		return err
	}

	// Composition root: construct adapters only when enabled, hold them as ports.
	var (
		db      contract.ORM
		cache   contract.Cache
		store   contract.Storage
		token   contract.Token
		limiter ratelimit.Store
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
		redisCache, err := redis.New(ctx, cfg.Redis)
		if err != nil {
			return fmt.Errorf("redis connect failed: %w", err)
		}
		cache = redisCache
		defer cache.Close()
		limiter = ratelimit.NewRedisStore(redisCache.Client())
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

	app := newApp(cfg, trustedProxies, allowedOrigins, db, limiter, token, store)

	_ = cache

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

func parseFlags() (provision, reset string, err error) {
	flag.StringVar(&provision, "assessment-provision", "", "provision candidate tenants from a local JSON manifest; do not start HTTP")
	flag.StringVar(&reset, "assessment-reset", "", "reset one tenant UUID while all API replicas are stopped; do not start HTTP")
	flag.Parse()
	if flag.NArg() != 0 || (provision != "" && reset != "") {
		return "", "", errors.New("provide one assessment operation or no arguments to start the API")
	}
	return provision, reset, nil
}

// prepareServer reads the HTTP-only settings, installs the logger, and validates the config.
func prepareServer(cfg *config.Config) ([]string, string, error) {
	trustedProxies, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return nil, "", fmt.Errorf("trusted proxy configuration invalid: %w", err)
	}
	allowedOrigins, err := parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS"))
	if err != nil {
		return nil, "", fmt.Errorf("allowed origins configuration invalid: %w", err)
	}

	log := logger.Development()
	if cfg.App.Env == "production" {
		log = logger.Production()
	}
	slog.SetDefault(log)
	if err := validateConfig(cfg, trustedProxies); err != nil {
		return nil, "", err
	}
	return trustedProxies, allowedOrigins, nil
}

// Operator commands share the composition root, never the HTTP route tree.
func runTenantCommand(ctx context.Context, cfg *config.Config, manifest, tenantID string) error {
	if !cfg.PostgresEnabled {
		return errors.New("assessment operations require DB_ENABLED=true")
	}
	if tenantID != "" && !cfg.MinIOEnabled {
		return errors.New("reset requires MINIO_ENABLED=true to remove all tenant avatars")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	db, err := openPostgres(cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()
	var cleanup tenant.CleanupAvatars
	if tenantID != "" {
		storage, err := minioadapter.New(ctx, cfg.MinIO)
		if err != nil {
			return fmt.Errorf("private storage unavailable: %w", err)
		}
		cleanup = removeTenantAvatars(storage.Client(), cfg.MinIO.Bucket)
	}
	uc := tenant.NewTenantUsecase(tenant.NewTenantRepository(db), cleanup)
	if tenantID != "" {
		if err := uc.Reset(ctx, tenantID); err != nil {
			return err
		}
		fmt.Printf("Reset candidate tenant %s; Admin retained, all sessions revoked.\n", tenantID)
		return nil
	}
	tenants, err := readManifest(manifest)
	if err != nil {
		return err
	}
	if err := uc.SetupTenants(ctx, tenants, os.Getenv); err != nil {
		return err
	}
	fmt.Printf("Provisioned %d candidate tenants; existing account credentials unchanged.\n", len(tenants))
	return nil
}

func removeTenantAvatars(client *miniogo.Client, bucket string) tenant.CleanupAvatars {
	return func(ctx context.Context, id string) error {
		// List the full prefix, including objects no longer referenced by an Employee.
		listCtx, stop := context.WithCancel(ctx)
		defer stop()
		prefix := "avatars/" + id + "/"
		for object := range client.ListObjects(listCtx, bucket, miniogo.ListObjectsOptions{Prefix: prefix, Recursive: true, WithVersions: true}) {
			if object.Err != nil {
				return object.Err
			}
			if !strings.HasPrefix(object.Key, prefix) {
				return errors.New("storage returned an object outside the tenant prefix")
			}
			if err := client.RemoveObject(ctx, bucket, object.Key, miniogo.RemoveObjectOptions{VersionID: object.VersionID}); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
}

func readManifest(path string) ([]tenant.Tenant, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open assessment manifest")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
	decoder.DisallowUnknownFields()
	var tenants []tenant.Tenant
	if err := decoder.Decode(&tenants); err != nil {
		return nil, errors.New("invalid assessment JSON manifest")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("assessment manifest must contain one JSON array")
	}
	return tenants, nil
}

func openPostgres(cfg config.Postgres) (*gormadapter.DB, error) {
	gormDB, err := gormadapter.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres connect failed: %w", err)
	}
	if err := gormDB.Raw().AutoMigrate(&auth.User{}, &auth.AuthSession{}, &auth.RefreshToken{}, &employee.Department{}, &employee.Employee{},
		&project.Project{}, &project.Phase{}, &project.Task{}, &chat.Conversation{}, &chat.Message{}); err != nil {
		gormDB.Close()
		return nil, fmt.Errorf("automigrate failed: %w", err)
	}
	if err := employee.SeedDepartments(context.Background(), gormDB); err != nil {
		gormDB.Close()
		return nil, fmt.Errorf("department seed failed: %w", err)
	}
	return gormDB, nil
}

// skipRateLimits is only an explicit APP_ENV=development. bkgo turns a blank
// APP_ENV into "development", and that default must still enforce the buckets.
func skipRateLimits(env string) bool {
	return env == "development" && os.Getenv("APP_ENV") == "development"
}

func allowRequest(c *fiber.Ctx) error { return c.Next() }

func validateConfig(cfg *config.Config, trustedProxies []string) error {
	if cfg.App.Env == "production" && (!cfg.PostgresEnabled || !cfg.JWTEnabled || len([]byte(cfg.JWT.Secret)) < 32 || !cfg.RedisEnabled || len(trustedProxies) == 0 || !cfg.MinIOEnabled) {
		return errors.New("production requires PostgreSQL, Redis, trusted proxy addresses, JWT, a JWT secret of at least 32 bytes, and private object storage")
	}
	if !skipRateLimits(cfg.App.Env) && cfg.PostgresEnabled && cfg.JWTEnabled && !cfg.RedisEnabled {
		return errors.New("Redis is required when authenticated API routes are enabled")
	}
	return nil
}

func newApp(cfg *config.Config, trustedProxies []string, allowedOrigins string, db contract.ORM, limiter ratelimit.Store, token contract.Token, store contract.Storage) *fiber.App {
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
	apidocs.Register(app)

	api := app.Group("/api/v1")
	authRepository := auth.NewAuthRepository(db)
	authMiddleware := auth.Authentication(token, authRepository)
	if db != nil && token != nil && (skipRateLimits(cfg.App.Env) || limiter != nil) {
		protected := []fiber.Handler{authMiddleware}
		loginLimit := allowRequest
		refreshLimit := allowRequest
		if !skipRateLimits(cfg.App.Env) {
			loginLimit = auth.LoginRateLimit(limiter)
			refreshLimit = auth.RefreshRateLimit(limiter)
			protected = append(protected, ratelimit.PerUser(limiter, auth.UserID))
		}
		authHandler := auth.NewAuthHandler(auth.NewAuthUsecase(authRepository, token))
		authHandler.RegisterRoutes(api, loginLimit, refreshLimit, protected...)
		employees := employee.NewEmployeeUsecase(employee.NewEmployeeRepository(db, chat.ProvisionConversation), store, cfg.MinIO.Bucket)
		employee.NewEmployeeHandler(employees).RegisterRoutes(api, protected...)
		project.NewProjectHandler(project.NewProjectUsecase(project.NewProjectRepository(db))).RegisterRoutes(api, protected...)
		chatHandler := chat.NewChatHandler(chat.NewChatUsecase(chat.NewChatRepository(db), employees.AvatarURL, authRepository))
		chatHandler.RegisterRoutes(api, protected...)
		chatHandler.RegisterWebSocket(app, strings.Split(allowedOrigins, ","))
	}
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
