package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/rl1809/flash-sale/internal/core/domain"
	"github.com/rl1809/flash-sale/internal/port"
)

var (
	ErrDuplicateRequest  = errors.New("duplicate request")
	ErrInsufficientStock = errors.New("insufficient stock")
)

type OrderService struct {
	cache       port.CacheRepository
	orderQueue  chan domain.Order
}

func NewOrderService(cache port.CacheRepository, queueSize int) *OrderService {
	return &OrderService{
		cache:      cache,
		orderQueue: make(chan domain.Order, queueSize),
	}
}

func (s *OrderService) Purchase(ctx context.Context, userID, itemID string, quantity int) error {
	idempotencyKey := fmt.Sprintf("order:%s:%s", userID, itemID)

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
		ID:        fmt.Sprintf("%s-%s-%d", userID, itemID, time.Now().UnixNano()),
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

func StartWorkers(count int, queue <-chan domain.Order, db port.DatabaseRepository) {
	for i := 0; i < count; i++ {
		go worker(i, queue, db)
	}
}

func worker(id int, queue <-chan domain.Order, db port.DatabaseRepository) {
	for order := range queue {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		if err := db.CreateOrder(ctx, order); err != nil {
			log.Printf("worker %d: failed to save order %s: %v", id, order.ID, err)
		} else {
			log.Printf("worker %d: saved order %s", id, order.ID)
		}

		cancel()
	}
}
