package port

import (
	"context"

	"github.com/rl1809/flash-sale/internal/core/domain"
)

type DatabaseRepository interface {
	// CreateOrder persists a new order with optimistic locking on inventory
	CreateOrder(ctx context.Context, order domain.Order) error

	// GetInventory retrieves inventory by item ID
	GetInventory(ctx context.Context, itemID string) (*domain.Inventory, error)

	// UpdateInventory updates inventory with version check for optimistic locking
	UpdateInventory(ctx context.Context, inventory domain.Inventory) error
}
