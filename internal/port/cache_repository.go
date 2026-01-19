package port

import "context"

type CacheRepository interface {
	// DecrementStock atomically decreases stock in cache, returns false if insufficient
	DecrementStock(ctx context.Context, itemID string, quantity int) (bool, error)

	// IncrementStock restores stock (for rollback on failure)
	IncrementStock(ctx context.Context, itemID string, quantity int) error

	// SetIdempotency sets a key for idempotency check, returns false if already exists
	SetIdempotency(ctx context.Context, key string) (bool, error)
}
