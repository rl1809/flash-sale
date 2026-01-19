package tests

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/rl1809/flash-sale/internal/adapter/storage"
	"github.com/rl1809/flash-sale/internal/core/domain"
	"github.com/rl1809/flash-sale/internal/core/service"
	"github.com/rl1809/flash-sale/internal/port"
)

type testEnv struct {
	redis   *redis.Client
	mysql   *sql.DB
	cache   *storage.RedisAdapter
	db      *storage.MySQLAdapter
	cleanup func()
}

func setupTestEnv(t *testing.T) *testEnv {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		mysqlDSN = "root:root@tcp(localhost:3306)/flashsale?parseTime=true"
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	db, err := sql.Open("mysql", mysqlDSN)
	if err != nil {
		t.Skipf("MySQL not available: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("MySQL not available: %v", err)
	}

	return &testEnv{
		redis: rdb,
		mysql: db,
		cache: storage.NewRedisAdapter(rdb),
		db:    storage.NewMySQLAdapter(db),
		cleanup: func() {
			rdb.Close()
			db.Close()
		},
	}
}

func TestIntegration_FullFlashSaleFlow(t *testing.T) {
	env := setupTestEnv(t)
	defer env.cleanup()

	ctx := context.Background()
	itemID := "integration-test-item"
	initialStock := 10

	// Setup: Clean and initialize
	env.redis.Del(ctx, "stock:"+itemID)
	env.redis.Del(ctx, "idempotency:*")
	env.mysql.ExecContext(ctx, `DELETE FROM orders WHERE item_id = ?`, itemID)
	env.mysql.ExecContext(ctx, `
		INSERT INTO inventory (item_id, stock, version) VALUES (?, ?, 0)
		ON DUPLICATE KEY UPDATE stock = ?, version = 0`, itemID, initialStock, initialStock)

	// Initialize stock in Redis
	env.cache.SetStock(ctx, itemID, initialStock)

	// Create service
	svc := service.NewOrderService(env.cache, 100)

	// Start workers
	var wg sync.WaitGroup
	workerCount := 3
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			workerLoop(id, svc.GetOrderQueue(), env.db, env.cache)
		}(i)
	}

	// Execute purchases
	var successCount atomic.Int32
	var purchaseWg sync.WaitGroup
	totalRequests := 20

	for i := 0; i < totalRequests; i++ {
		purchaseWg.Add(1)
		go func(userID int) {
			defer purchaseWg.Done()
			requestID := uuid.New().String()
			err := svc.Purchase(ctx, requestID, "user", itemID, 1)
			if err == nil {
				successCount.Add(1)
			}
		}(i)
	}

	purchaseWg.Wait()

	// Close service and wait for workers
	svc.Close()
	wg.Wait()

	// Verify results
	if successCount.Load() != int32(initialStock) {
		t.Errorf("expected %d successful purchases, got %d", initialStock, successCount.Load())
	}

	// Verify Redis stock
	redisStock, _ := env.redis.Get(ctx, "stock:"+itemID).Int()
	if redisStock != 0 {
		t.Errorf("expected Redis stock 0, got %d", redisStock)
	}

	// Verify MySQL orders
	var orderCount int
	env.mysql.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE item_id = ?`, itemID).Scan(&orderCount)
	if orderCount != initialStock {
		t.Errorf("expected %d orders in MySQL, got %d", initialStock, orderCount)
	}

	// Verify MySQL inventory
	var mysqlStock int
	env.mysql.QueryRowContext(ctx, `SELECT stock FROM inventory WHERE item_id = ?`, itemID).Scan(&mysqlStock)
	if mysqlStock != 0 {
		t.Errorf("expected MySQL stock 0, got %d", mysqlStock)
	}

	// Cleanup
	env.mysql.ExecContext(ctx, `DELETE FROM orders WHERE item_id = ?`, itemID)
}

func TestIntegration_RollbackOnMySQLFailure(t *testing.T) {
	env := setupTestEnv(t)
	defer env.cleanup()

	ctx := context.Background()
	itemID := "rollback-test-item"
	initialStock := 5

	// Setup: Initialize Redis but DON'T create inventory in MySQL
	env.redis.Del(ctx, "stock:"+itemID)
	env.mysql.ExecContext(ctx, `DELETE FROM inventory WHERE item_id = ?`, itemID)
	env.mysql.ExecContext(ctx, `DELETE FROM orders WHERE item_id = ?`, itemID)

	env.cache.SetStock(ctx, itemID, initialStock)

	// Create service
	svc := service.NewOrderService(env.cache, 100)

	// Start worker that will fail on MySQL
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		workerLoop(0, svc.GetOrderQueue(), env.db, env.cache)
	}()

	// Purchase should succeed (Redis OK)
	requestID := uuid.New().String()
	err := svc.Purchase(ctx, requestID, "user", itemID, 1)
	if err != nil {
		t.Fatalf("purchase failed: %v", err)
	}

	// Give worker time to process and rollback
	time.Sleep(100 * time.Millisecond)

	svc.Close()
	wg.Wait()

	// Verify stock was rolled back
	redisStock, _ := env.redis.Get(ctx, "stock:"+itemID).Int()
	if redisStock != initialStock {
		t.Errorf("expected Redis stock %d after rollback, got %d", initialStock, redisStock)
	}
}

func TestIntegration_IdempotencyPreventsDoubleOrder(t *testing.T) {
	env := setupTestEnv(t)
	defer env.cleanup()

	ctx := context.Background()
	itemID := "idempotency-test-item"
	requestID := "same-request-id-" + uuid.New().String()

	// Setup
	env.redis.Del(ctx, "stock:"+itemID)
	env.redis.Del(ctx, "idempotency:"+requestID)
	env.cache.SetStock(ctx, itemID, 10)

	svc := service.NewOrderService(env.cache, 100)
	defer svc.Close()

	go func() {
		for range svc.GetOrderQueue() {
		}
	}()

	// First call
	err := svc.Purchase(ctx, requestID, "user", itemID, 1)
	if err != nil {
		t.Fatalf("first purchase failed: %v", err)
	}

	// Second call with same requestID
	err = svc.Purchase(ctx, requestID, "user", itemID, 1)
	if err != service.ErrDuplicateRequest {
		t.Errorf("expected ErrDuplicateRequest, got: %v", err)
	}

	// Verify only 1 stock decremented
	stock, _ := env.redis.Get(ctx, "stock:"+itemID).Int()
	if stock != 9 {
		t.Errorf("expected stock 9, got %d", stock)
	}
}

func workerLoop(id int, queue <-chan domain.Order, db port.DatabaseRepository, cache port.CacheRepository) {
	for order := range queue {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		if err := db.CreateOrder(ctx, order); err != nil {
			// Rollback
			cache.IncrementStock(ctx, order.ItemID, order.Quantity)
		}

		cancel()
	}
}
