package storage

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	stockKeyPrefix      = "stock:"
	idempotencyKeyTTL   = 24 * time.Hour
)

var decrementStockScript = redis.NewScript(`
local key = KEYS[1]
local quantity = tonumber(ARGV[1])

local current = redis.call('GET', key)
if not current then
	return 0
end

current = tonumber(current)
if current >= quantity then
	redis.call('DECRBY', key, quantity)
	return 1
end

return 0
`)

type RedisAdapter struct {
	client *redis.Client
}

func NewRedisAdapter(client *redis.Client) *RedisAdapter {
	return &RedisAdapter{client: client}
}

func (r *RedisAdapter) DecrementStock(ctx context.Context, itemID string, quantity int) (bool, error) {
	key := stockKeyPrefix + itemID

	result, err := decrementStockScript.Run(ctx, r.client, []string{key}, quantity).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}

func (r *RedisAdapter) IncrementStock(ctx context.Context, itemID string, quantity int) error {
	key := stockKeyPrefix + itemID
	return r.client.IncrBy(ctx, key, int64(quantity)).Err()
}

func (r *RedisAdapter) SetIdempotency(ctx context.Context, key string) (bool, error) {
	ok, err := r.client.SetNX(ctx, key, 1, idempotencyKeyTTL).Result()
	if err != nil {
		return false, err
	}

	return ok, nil
}

func (r *RedisAdapter) SetStock(ctx context.Context, itemID string, quantity int) error {
	key := stockKeyPrefix + itemID
	return r.client.Set(ctx, key, quantity, 0).Err()
}
