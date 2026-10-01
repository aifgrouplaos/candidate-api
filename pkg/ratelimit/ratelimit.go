// Package ratelimit provides a sliding-window rate limit middleware for any route.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/gofiber/fiber/v2"
)

// Store is the port for counting requests. Hit records one request under key and
// reports whether it exceeds limit within window, and how long until it would not.
type Store interface {
	Hit(ctx context.Context, key string, limit int64, window time.Duration) (exceeded bool, retryAfter time.Duration, err error)
}

// Bucket counts requests sharing Key under Name; requests over Limit within Window are rejected.
type Bucket struct {
	Name   string
	Key    string
	Limit  int64
	Window time.Duration
}

// New checks every bucket returned for the request and passes errors to fail, which writes the response.
func New(store Store, buckets func(*fiber.Ctx) []Bucket, fail func(*fiber.Ctx, error) error) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if store == nil {
			return fail(c, errs.Internal("rate limit service is unavailable"))
		}
		for _, bucket := range buckets(c) {
			exceeded, retry, err := store.Hit(c.UserContext(), bucketKey(bucket), bucket.Limit, bucket.Window)
			if err != nil {
				slog.Error("rate limit check failed", "error", err, "bucket", bucket.Name, "path", c.Path())
				return fail(c, errs.Internal("rate limit service is unavailable"))
			}
			if exceeded {
				retryAfter := int64(math.Ceil(retry.Seconds()))
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
