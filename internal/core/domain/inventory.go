package domain

import "time"

type Inventory struct {
	ID        string
	ItemID    string
	Quantity  int
	Version   int // optimistic locking
	CreatedAt time.Time
	UpdatedAt time.Time
}
