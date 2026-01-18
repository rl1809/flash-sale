package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rl1809/flash-sale/internal/core/domain"
)

var ErrOptimisticLock = errors.New("optimistic lock conflict")

type MySQLAdapter struct {
	db *sql.DB
}

func NewMySQLAdapter(db *sql.DB) *MySQLAdapter {
	return &MySQLAdapter{db: db}
}

func (m *MySQLAdapter) CreateOrder(ctx context.Context, order domain.Order) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO orders (id, item_id, user_id, quantity, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		order.ID, order.ItemID, order.UserID, order.Quantity, order.Status,
		order.CreatedAt, order.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE inventory 
		SET stock = stock - ?, version = version + 1, updated_at = NOW()
		WHERE item_id = ? AND stock >= ?`,
		order.Quantity, order.ItemID, order.Quantity,
	)
	if err != nil {
		return fmt.Errorf("update inventory: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrOptimisticLock
	}

	return tx.Commit()
}

func (m *MySQLAdapter) GetInventory(ctx context.Context, itemID string) (*domain.Inventory, error) {
	var inv domain.Inventory
	err := m.db.QueryRowContext(ctx, `
		SELECT item_id, stock, version, created_at, updated_at
		FROM inventory WHERE item_id = ?`, itemID,
	).Scan(&inv.ItemID, &inv.Quantity, &inv.Version, &inv.CreatedAt, &inv.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query inventory: %w", err)
	}

	inv.ID = inv.ItemID
	return &inv, nil
}

func (m *MySQLAdapter) UpdateInventory(ctx context.Context, inv domain.Inventory) error {
	result, err := m.db.ExecContext(ctx, `
		UPDATE inventory 
		SET stock = ?, version = version + 1, updated_at = NOW()
		WHERE item_id = ? AND version = ?`,
		inv.Quantity, inv.ItemID, inv.Version,
	)
	if err != nil {
		return fmt.Errorf("update inventory: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrOptimisticLock
	}

	return nil
}
