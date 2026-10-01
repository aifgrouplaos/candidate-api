// Package ratelimit provides a Redis sliding-window rate limit middleware for any route.
package ratelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"strconv"
	"time"

	redisadapter "github.com/BounkhongDev/bkgo/adapter/redis"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Bucket counts requests sharing Key under Name; requests over Limit within Window are rejected.
type Bucket struct {
	Name   string
	Key    string
	Limit  int64
	Window time.Duration
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

// New checks every bucket returned for the request and passes errors to fail, which writes the response.
func New(cache *redisadapter.Cache, buckets func(*fiber.Ctx) []Bucket, fail func(*fiber.Ctx, error) error) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if cache == nil {
			return fail(c, errs.Internal("rate limit service is unavailable"))
		}
		for _, bucket := range buckets(c) {
			now := time.Now().UnixMilli()
			result, err := slidingWindow.Run(c.UserContext(), cache.Client(), []string{bucketKey(bucket)},
				now, bucket.Window.Milliseconds(), uuid.NewString(), bucket.Limit).Slice()
			if err != nil || len(result) != 2 {
				slog.Error("rate limit check failed", "error", err, "bucket", bucket.Name, "path", c.Path())
				return fail(c, errs.Internal("rate limit service is unavailable"))
			}
			count, countOK := result[0].(int64)
			retryMS, retryOK := result[1].(int64)
			if !countOK || !retryOK {
				return fail(c, errs.Internal("rate limit service returned an invalid response"))
			}
			if count > bucket.Limit {
				retryAfter := int64(math.Ceil(float64(retryMS) / float64(time.Second.Milliseconds())))
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Set("Retry-After", strconv.FormatInt(retryAfter, 10))
				return fail(c, errs.New(fiber.StatusTooManyRequests, "RATE_LIMITED", "Rate limit exceeded."))
			}
		}
		return c.Next()
	}
}

// ClientIP takes the last X-Forwarded-For entry because a trusted proxy appends the
// address it saw; earlier entries are client-controlled.
func ClientIP(c *fiber.Ctx) string {
	if ips := c.IPs(); c.IsProxyTrusted() && len(ips) > 0 {
		return ips[len(ips)-1]
	}
	return c.Context().RemoteIP().String()
}

func bucketKey(bucket Bucket) string {
	sum := sha256.Sum256([]byte(bucket.Key))
	return "candidate-api:rate:" + bucket.Name + ":" + hex.EncodeToString(sum[:])
}
