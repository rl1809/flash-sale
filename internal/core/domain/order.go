package domain

import "time"

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusConfirmed OrderStatus = "confirmed"
	OrderStatusCancelled OrderStatus = "cancelled"
)

type Order struct {
	ID        string
	UserID    string
	ItemID    string
	Quantity  int
	Status    OrderStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}
