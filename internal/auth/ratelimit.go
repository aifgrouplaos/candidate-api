package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	redisadapter "github.com/BounkhongDev/bkgo/adapter/redis"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type rateBucket struct {
	name   string
	key    string
	limit  int64
	window time.Duration
}

var slidingWindow = goredis.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
redis.call('ZADD', KEYS[1], now, ARGV[3])
redis.call('PEXPIRE', KEYS[1], window)
local count = redis.call('ZCARD', KEYS[1])
local first = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
local retry = 0
if count > tonumber(ARGV[4]) then
  retry = tonumber(first[2]) + window - now
end
return {count, retry}
`)

func LoginRateLimit(cache *redisadapter.Cache) fiber.Handler {
	return redisRateLimit(cache, func(c *fiber.Ctx) []rateBucket {
		var input Credentials
		_ = json.Unmarshal(c.Body(), &input)
		email := strings.ToLower(strings.TrimSpace(input.Email))
		buckets := []rateBucket{{name: "login-ip", key: clientIP(c), limit: 100, window: 15 * time.Minute}}
		if email != "" {
			buckets = append(buckets, rateBucket{name: "login-account", key: email, limit: 10, window: 15 * time.Minute})
		}
		return buckets
	})
}

func RefreshRateLimit(cache *redisadapter.Cache) fiber.Handler {
	return redisRateLimit(cache, func(c *fiber.Ctx) []rateBucket {
		return []rateBucket{{name: "refresh-ip", key: clientIP(c), limit: 30, window: time.Minute}}
	})
}

// clientIP takes the last X-Forwarded-For entry because a trusted proxy appends the
// address it saw; earlier entries are client-controlled.
func clientIP(c *fiber.Ctx) string {
	if ips := c.IPs(); c.IsProxyTrusted() && len(ips) > 0 {
		return ips[len(ips)-1]
	}
	return c.Context().RemoteIP().String()
}

func UserRateLimit(cache *redisadapter.Cache) fiber.Handler {
	limit := redisRateLimit(cache, func(c *fiber.Ctx) []rateBucket {
		principal, ok := PrincipalFrom(c)
		if !ok {
			return nil
		}
		return []rateBucket{{name: "api-user", key: principal.UserID, limit: 100, window: time.Minute}}
	})
	return func(c *fiber.Ctx) error {
		if _, ok := PrincipalFrom(c); !ok {
			return writeError(c, errs.ErrUnauthorized)
		}
		return limit(c)
	}
}

func redisRateLimit(cache *redisadapter.Cache, buckets func(*fiber.Ctx) []rateBucket) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if cache == nil {
			return writeError(c, errs.Internal("rate limit service is unavailable"))
		}
		for _, bucket := range buckets(c) {
			now := time.Now().UnixMilli()
			result, err := slidingWindow.Run(c.UserContext(), cache.Client(), []string{bucketKey(bucket)},
				now, bucket.window.Milliseconds(), uuid.NewString(), bucket.limit).Slice()
			if err != nil || len(result) != 2 {
				slog.Error("rate limit check failed", "error", err, "bucket", bucket.name, "path", c.Path())
				return writeError(c, errs.Internal("rate limit service is unavailable"))
			}
			count, countOK := result[0].(int64)
			retryMS, retryOK := result[1].(int64)
			if !countOK || !retryOK {
				return writeError(c, errs.Internal("rate limit service returned an invalid response"))
			}
			if count > bucket.limit {
				retryAfter := int64(math.Ceil(float64(retryMS) / float64(time.Second.Milliseconds())))
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Set("Retry-After", strconv.FormatInt(retryAfter, 10))
				return writeError(c, errs.New(fiber.StatusTooManyRequests, "RATE_LIMITED", "Rate limit exceeded."))
			}
		}
		return c.Next()
	}
}

func bucketKey(bucket rateBucket) string {
	sum := sha256.Sum256([]byte(bucket.key))
	return "candidate-api:rate:" + bucket.name + ":" + hex.EncodeToString(sum[:])
}
