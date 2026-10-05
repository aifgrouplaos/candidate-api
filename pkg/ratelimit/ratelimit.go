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
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/gofiber/fiber/v2"
)

const msgUnavailable = "rate limit service is unavailable"

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

// New rejects the request when any bucket returned for it is over its limit.
func New(store Store, buckets func(*fiber.Ctx) []Bucket) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if store == nil {
			return errs.Internal(msgUnavailable)
		}
		for _, bucket := range buckets(c) {
			exceeded, retry, err := store.Hit(c.UserContext(), bucketKey(bucket), bucket.Limit, bucket.Window)
			if err != nil {
				slog.Error("rate limit check failed", "error", err, "bucket", bucket.Name, "path", c.Path())
				return errs.Internal(msgUnavailable)
			}
			if exceeded {
				retryAfter := int64(math.Ceil(retry.Seconds()))
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Set("Retry-After", strconv.FormatInt(retryAfter, 10))
				return apierror.RateLimited
			}
		}
		return c.Next()
	}
}

// PerUser applies the general authenticated-route limit and rejects requests whose
// userID is empty, so it must run after authentication.
func PerUser(store Store, userID func(*fiber.Ctx) string) fiber.Handler {
	limit := New(store, func(c *fiber.Ctx) []Bucket {
		return []Bucket{{Name: "api-user", Key: userID(c), Limit: 100, Window: time.Minute}}
	})
	return func(c *fiber.Ctx) error {
		if userID(c) == "" {
			return errs.ErrUnauthorized
		}
		return limit(c)
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
