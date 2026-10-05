package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

var errInvalidResponse = errors.New("rate limit script returned an invalid response")

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

// RedisStore is the Redis adapter for Store, using an atomic sorted-set sliding window.
type RedisStore struct {
	client *goredis.Client
}

func NewRedisStore(client *goredis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Hit(ctx context.Context, key string, limit int64, window time.Duration) (bool, time.Duration, error) {
	result, err := slidingWindow.Run(ctx, s.client, []string{key},
		time.Now().UnixMilli(), window.Milliseconds(), uuid.NewString(), limit).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(result) != 2 {
		return false, 0, errInvalidResponse
	}
	count, countOK := result[0].(int64)
	retryMS, retryOK := result[1].(int64)
	if !countOK || !retryOK {
		return false, 0, errInvalidResponse
	}
	return count > limit, time.Duration(retryMS) * time.Millisecond, nil
}
