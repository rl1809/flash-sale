package storage

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/rl1809/flash-sale/internal/core/domain"
)

func getMySQLDB(t *testing.T) *sql.DB {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "root:root@tcp(localhost:3306)/flashsale?parseTime=true"
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Skipf("MySQL not available: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}

	return db
}

func TestCreateOrder_Success(t *testing.T) {
	db := getMySQLDB(t)
	defer db.Close()

	ctx := context.Background()
	adapter := NewMySQLAdapter(db)

	// Setup - ensure inventory exists
	_, err := db.ExecContext(ctx, `
		INSERT INTO inventory (item_id, stock, version) VALUES ('test-item', 100, 0)
		ON DUPLICATE KEY UPDATE stock = 100, version = 0`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Cleanup old test orders
	db.ExecContext(ctx, `DELETE FROM orders WHERE id LIKE 'test-order-%'`)

	order := domain.Order{
		ID:        "test-order-" + time.Now().Format("20060102150405"),
		UserID:    "test-user",
		ItemID:    "test-item",
		Quantity:  1,
		Status:    domain.OrderStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err = adapter.CreateOrder(ctx, order)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// Verify order exists
	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE id = ?`, order.ID).Scan(&count)
	if count != 1 {
		t.Error("order not found in database")
	}

	// Verify inventory decremented
	var stock int
	db.QueryRowContext(ctx, `SELECT stock FROM inventory WHERE item_id = 'test-item'`).Scan(&stock)
	if stock != 99 {
		t.Errorf("expected stock 99, got %d", stock)
	}

	// Cleanup
	db.ExecContext(ctx, `DELETE FROM orders WHERE id = ?`, order.ID)
	db.ExecContext(ctx, `UPDATE inventory SET stock = 100, version = 0 WHERE item_id = 'test-item'`)
}

func TestCreateOrder_InsufficientStock(t *testing.T) {
	db := getMySQLDB(t)
	defer db.Close()

	ctx := context.Background()
	adapter := NewMySQLAdapter(db)

	// Setup - inventory with 0 stock
	_, err := db.ExecContext(ctx, `
		INSERT INTO inventory (item_id, stock, version) VALUES ('empty-item', 0, 0)
		ON DUPLICATE KEY UPDATE stock = 0, version = 0`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	order := domain.Order{
		ID:        "test-order-fail-" + time.Now().Format("20060102150405"),
		UserID:    "test-user",
		ItemID:    "empty-item",
		Quantity:  1,
		Status:    domain.OrderStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err = adapter.CreateOrder(ctx, order)
	if err == nil {
		t.Error("expected error for insufficient stock")
		db.ExecContext(ctx, `DELETE FROM orders WHERE id = ?`, order.ID)
	}
}

func TestGetInventory(t *testing.T) {
	db := getMySQLDB(t)
	defer db.Close()

	ctx := context.Background()
	adapter := NewMySQLAdapter(db)

	// Setup
	_, err := db.ExecContext(ctx, `
		INSERT INTO inventory (item_id, stock, version) VALUES ('get-test-item', 50, 5)
		ON DUPLICATE KEY UPDATE stock = 50, version = 5`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	inv, err := adapter.GetInventory(ctx, "get-test-item")
	if err != nil {
		t.Fatalf("GetInventory failed: %v", err)
	}

	if inv == nil {
		t.Fatal("expected inventory, got nil")
	}

	if inv.ItemID != "get-test-item" {
		t.Errorf("expected item_id 'get-test-item', got %s", inv.ItemID)
	}
	if inv.Quantity != 50 {
		t.Errorf("expected quantity 50, got %d", inv.Quantity)
	}
	if inv.Version != 5 {
		t.Errorf("expected version 5, got %d", inv.Version)
	}
}

func TestGetInventory_NotFound(t *testing.T) {
	db := getMySQLDB(t)
	defer db.Close()

	ctx := context.Background()
	adapter := NewMySQLAdapter(db)

	inv, err := adapter.GetInventory(ctx, "nonexistent-item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv != nil {
		t.Error("expected nil for nonexistent item")
	}
}

func TestUpdateInventory_OptimisticLock(t *testing.T) {
	db := getMySQLDB(t)
	defer db.Close()

	ctx := context.Background()
	adapter := NewMySQLAdapter(db)

	// Setup
	_, err := db.ExecContext(ctx, `
		INSERT INTO inventory (item_id, stock, version) VALUES ('lock-test-item', 100, 1)
		ON DUPLICATE KEY UPDATE stock = 100, version = 1`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Update with correct version
	inv := domain.Inventory{
		ItemID:   "lock-test-item",
		Quantity: 90,
		Version:  1,
	}

	err = adapter.UpdateInventory(ctx, inv)
	if err != nil {
		t.Fatalf("UpdateInventory failed: %v", err)
	}

	// Verify version incremented
	var version int
	db.QueryRowContext(ctx, `SELECT version FROM inventory WHERE item_id = 'lock-test-item'`).Scan(&version)
	if version != 2 {
		t.Errorf("expected version 2, got %d", version)
	}

	// Try update with stale version
	inv.Version = 1 // stale
	err = adapter.UpdateInventory(ctx, inv)
	if err != ErrOptimisticLock {
		t.Errorf("expected ErrOptimisticLock, got: %v", err)
	}
}
