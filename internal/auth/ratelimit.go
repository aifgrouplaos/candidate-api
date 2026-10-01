package auth

import (
	"encoding/json"
	"strings"
	"time"

	redisadapter "github.com/BounkhongDev/bkgo/adapter/redis"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/pkg/ratelimit"
	"github.com/gofiber/fiber/v2"
)

func LoginRateLimit(cache *redisadapter.Cache) fiber.Handler {
	return ratelimit.New(cache, func(c *fiber.Ctx) []ratelimit.Bucket {
		var input Credentials
		_ = json.Unmarshal(c.Body(), &input)
		email := strings.ToLower(strings.TrimSpace(input.Email))
		buckets := []ratelimit.Bucket{{Name: "login-ip", Key: ratelimit.ClientIP(c), Limit: 100, Window: 15 * time.Minute}}
		if email != "" {
			buckets = append(buckets, ratelimit.Bucket{Name: "login-account", Key: email, Limit: 10, Window: 15 * time.Minute})
		}
		return buckets
	}, writeError)
}

func RefreshRateLimit(cache *redisadapter.Cache) fiber.Handler {
	return ratelimit.New(cache, func(c *fiber.Ctx) []ratelimit.Bucket {
		return []ratelimit.Bucket{{Name: "refresh-ip", Key: ratelimit.ClientIP(c), Limit: 30, Window: time.Minute}}
	}, writeError)
}

func UserRateLimit(cache *redisadapter.Cache) fiber.Handler {
	limit := ratelimit.New(cache, func(c *fiber.Ctx) []ratelimit.Bucket {
		principal, ok := PrincipalFrom(c)
		if !ok {
			return nil
		}
		return []ratelimit.Bucket{{Name: "api-user", Key: principal.UserID, Limit: 100, Window: time.Minute}}
	}, writeError)
	return func(c *fiber.Ctx) error {
		if _, ok := PrincipalFrom(c); !ok {
			return writeError(c, errs.ErrUnauthorized)
		}
		return limit(c)
	}
}
