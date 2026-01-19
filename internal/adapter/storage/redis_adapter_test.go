package storage

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
)

func getRedisClient(t *testing.T) *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	return client
}

func TestDecrementStock_Success(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	adapter := NewRedisAdapter(client)

	// Setup
	client.Del(ctx, "stock:test-item")
	adapter.SetStock(ctx, "test-item", 10)

	// Test
	ok, err := adapter.DecrementStock(ctx, "test-item", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected success")
	}

	// Verify
	stock, _ := client.Get(ctx, "stock:test-item").Int()
	if stock != 7 {
		t.Errorf("expected stock 7, got %d", stock)
	}
}

func TestDecrementStock_InsufficientStock(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	adapter := NewRedisAdapter(client)

	// Setup
	client.Del(ctx, "stock:test-item")
	adapter.SetStock(ctx, "test-item", 5)

	// Test - try to decrement more than available
	ok, err := adapter.DecrementStock(ctx, "test-item", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected failure due to insufficient stock")
	}

	// Verify stock unchanged
	stock, _ := client.Get(ctx, "stock:test-item").Int()
	if stock != 5 {
		t.Errorf("expected stock 5, got %d", stock)
	}
}

func TestDecrementStock_KeyNotExists(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	adapter := NewRedisAdapter(client)

	// Setup - ensure key doesn't exist
	client.Del(ctx, "stock:nonexistent")

	// Test
	ok, err := adapter.DecrementStock(ctx, "nonexistent", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected failure for nonexistent key")
	}
}

func TestDecrementStock_Concurrent(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	adapter := NewRedisAdapter(client)

	initialStock := 20
	totalRequests := 50

	// Setup
	client.Del(ctx, "stock:concurrent-test")
	adapter.SetStock(ctx, "concurrent-test", initialStock)

	var successCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := adapter.DecrementStock(ctx, "concurrent-test", 1)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if ok {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if successCount.Load() != int32(initialStock) {
		t.Errorf("expected %d successes, got %d", initialStock, successCount.Load())
	}

	stock, _ := client.Get(ctx, "stock:concurrent-test").Int()
	if stock != 0 {
		t.Errorf("expected stock 0, got %d", stock)
	}
}

func TestIncrementStock(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	adapter := NewRedisAdapter(client)

	// Setup
	client.Del(ctx, "stock:test-item")
	adapter.SetStock(ctx, "test-item", 5)

	// Test
	err := adapter.IncrementStock(ctx, "test-item", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify
	stock, _ := client.Get(ctx, "stock:test-item").Int()
	if stock != 8 {
		t.Errorf("expected stock 8, got %d", stock)
	}
}

func TestSetIdempotency_Success(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	adapter := NewRedisAdapter(client)

	// Setup
	client.Del(ctx, "test-idem-key")

	// First call should succeed
	ok, err := adapter.SetIdempotency(ctx, "test-idem-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected first call to succeed")
	}

	// Second call should fail (key exists)
	ok, err = adapter.SetIdempotency(ctx, "test-idem-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected second call to fail")
	}
}

func TestSetIdempotency_Concurrent(t *testing.T) {
	client := getRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	adapter := NewRedisAdapter(client)

	// Setup
	client.Del(ctx, "concurrent-idem-key")

	var successCount atomic.Int32
	var wg sync.WaitGroup
	concurrency := 100

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := adapter.SetIdempotency(ctx, "concurrent-idem-key")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if ok {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// Only one should succeed
	if successCount.Load() != 1 {
		t.Errorf("expected exactly 1 success, got %d", successCount.Load())
	}
}
