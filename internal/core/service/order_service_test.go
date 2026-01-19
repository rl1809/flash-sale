package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rl1809/flash-sale/internal/core/domain"
)

// Mock CacheRepository
type mockCacheRepo struct {
	stock          int
	idempotencySet map[string]bool
	mu             sync.Mutex
}

func newMockCacheRepo(initialStock int) *mockCacheRepo {
	return &mockCacheRepo{
		stock:          initialStock,
		idempotencySet: make(map[string]bool),
	}
}

func (m *mockCacheRepo) DecrementStock(ctx context.Context, itemID string, quantity int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stock >= quantity {
		m.stock -= quantity
		return true, nil
	}
	return false, nil
}

func (m *mockCacheRepo) IncrementStock(ctx context.Context, itemID string, quantity int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stock += quantity
	return nil
}

func (m *mockCacheRepo) SetIdempotency(ctx context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.idempotencySet[key] {
		return false, nil
	}
	m.idempotencySet[key] = true
	return true, nil
}

func TestPurchase_Success(t *testing.T) {
	cache := newMockCacheRepo(10)
	svc := NewOrderService(cache, 100)
	defer svc.Close()

	// Drain queue
	go func() {
		for range svc.GetOrderQueue() {
		}
	}()

	err := svc.Purchase(context.Background(), "req-1", "user-1", "item-1", 1)
	if err != nil {
		t.Errorf("expected success, got error: %v", err)
	}

	if cache.stock != 9 {
		t.Errorf("expected stock 9, got %d", cache.stock)
	}
}

func TestPurchase_InsufficientStock(t *testing.T) {
	cache := newMockCacheRepo(0)
	svc := NewOrderService(cache, 100)
	defer svc.Close()

	go func() {
		for range svc.GetOrderQueue() {
		}
	}()

	err := svc.Purchase(context.Background(), "req-1", "user-1", "item-1", 1)
	if !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock, got: %v", err)
	}
}

func TestPurchase_DuplicateRequest(t *testing.T) {
	cache := newMockCacheRepo(10)
	svc := NewOrderService(cache, 100)
	defer svc.Close()

	go func() {
		for range svc.GetOrderQueue() {
		}
	}()

	// First request
	err := svc.Purchase(context.Background(), "req-1", "user-1", "item-1", 1)
	if err != nil {
		t.Fatalf("first purchase failed: %v", err)
	}

	// Duplicate request with same requestID
	err = svc.Purchase(context.Background(), "req-1", "user-1", "item-1", 1)
	if !errors.Is(err, ErrDuplicateRequest) {
		t.Errorf("expected ErrDuplicateRequest, got: %v", err)
	}

	// Stock should only be decremented once
	if cache.stock != 9 {
		t.Errorf("expected stock 9, got %d", cache.stock)
	}
}

func TestPurchase_Concurrent(t *testing.T) {
	initialStock := 20
	totalRequests := 50

	cache := newMockCacheRepo(initialStock)
	svc := NewOrderService(cache, 100)
	defer svc.Close()

	go func() {
		for range svc.GetOrderQueue() {
		}
	}()

	var successCount atomic.Int32
	var failCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			requestID := "req-" + string(rune(id+'0')) + "-" + string(rune(i+'0'))
			err := svc.Purchase(context.Background(), requestID, "user", "item", 1)
			if err == nil {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if successCount.Load() != int32(initialStock) {
		t.Errorf("expected %d successes, got %d", initialStock, successCount.Load())
	}

	if cache.stock != 0 {
		t.Errorf("expected stock 0, got %d", cache.stock)
	}
}

func TestPurchase_OrderQueued(t *testing.T) {
	cache := newMockCacheRepo(10)
	svc := NewOrderService(cache, 100)

	err := svc.Purchase(context.Background(), "req-1", "user-1", "item-1", 2)
	if err != nil {
		t.Fatalf("purchase failed: %v", err)
	}

	// Read from queue
	order := <-svc.GetOrderQueue()

	if order.UserID != "user-1" {
		t.Errorf("expected user-1, got %s", order.UserID)
	}
	if order.ItemID != "item-1" {
		t.Errorf("expected item-1, got %s", order.ItemID)
	}
	if order.Quantity != 2 {
		t.Errorf("expected quantity 2, got %d", order.Quantity)
	}
	if order.Status != domain.OrderStatusPending {
		t.Errorf("expected pending status, got %s", order.Status)
	}
	if order.ID == "" {
		t.Error("expected non-empty order ID")
	}

	svc.Close()
}
