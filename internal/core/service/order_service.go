package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rl1809/flash-sale/internal/core/domain"
	"github.com/rl1809/flash-sale/internal/port"
)

var (
	ErrDuplicateRequest  = errors.New("duplicate request")
	ErrInsufficientStock = errors.New("insufficient stock")
)

type OrderService struct {
	cache      port.CacheRepository
	orderQueue chan domain.Order
}

func NewOrderService(cache port.CacheRepository, queueSize int) *OrderService {
	return &OrderService{
		cache:      cache,
		orderQueue: make(chan domain.Order, queueSize),
	}
}

func (s *OrderService) Purchase(ctx context.Context, requestID, userID, itemID string, quantity int) error {
	idempotencyKey := fmt.Sprintf("idempotency:%s", requestID)

	ok, err := s.cache.SetIdempotency(ctx, idempotencyKey)
	if err != nil {
		return fmt.Errorf("idempotency check failed: %w", err)
	}
	if !ok {
		return ErrDuplicateRequest
	}

	ok, err = s.cache.DecrementStock(ctx, itemID, quantity)
	if err != nil {
		return fmt.Errorf("stock decrement failed: %w", err)
	}
	if !ok {
		return ErrInsufficientStock
	}

	order := domain.Order{
		ID:        uuid.New().String(),
		UserID:    userID,
		ItemID:    itemID,
		Quantity:  quantity,
		Status:    domain.OrderStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	s.orderQueue <- order

	return nil
}

func (s *OrderService) GetOrderQueue() <-chan domain.Order {
	return s.orderQueue
}

func (s *OrderService) Close() {
	close(s.orderQueue)
}
