package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/rl1809/flash-sale/internal/adapter/storage"
	"github.com/rl1809/flash-sale/internal/core/service"
)

const (
	redisAddr     = "localhost:6379"
	itemID        = "flash-sale-item"
	initialStock  = 20
	totalRequests = 50
	queueSize     = 100
)

func main() {
	ctx := context.Background()

	// Initialize Redis
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect redis: %v", err)
	}
	defer rdb.Close()

	// Clear previous test data
	rdb.Del(ctx, "stock:"+itemID)
	keys, _ := rdb.Keys(ctx, "order:user-*:"+itemID).Result()
	for _, k := range keys {
		rdb.Del(ctx, k)
	}

	// Initialize adapter and service
	redisAdapter := storage.NewRedisAdapter(rdb)
	if err := redisAdapter.SetStock(ctx, itemID, initialStock); err != nil {
		log.Fatalf("failed to set stock: %v", err)
	}

	orderService := service.NewOrderService(redisAdapter, queueSize)
	defer orderService.Close()

	// Drain the order queue in background
	go func() {
		for range orderService.GetOrderQueue() {
		}
	}()

	// Counters
	var successCount atomic.Int32
	var failCount atomic.Int32

	// Spawn concurrent requests
	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			err := orderService.Purchase(ctx, fmt.Sprintf("user-%d", userID), itemID, 1)
			if err == nil {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Results
	success := successCount.Load()
	fail := failCount.Load()

	fmt.Println("========== STRESS TEST RESULTS ==========")
	fmt.Printf("Initial Stock:    %d\n", initialStock)
	fmt.Printf("Total Requests:   %d\n", totalRequests)
	fmt.Printf("Successful:       %d\n", success)
	fmt.Printf("Failed:           %d\n", fail)
	fmt.Printf("Duration:         %v\n", elapsed)
	fmt.Println("==========================================")

	// Assertions
	if success == int32(initialStock) && fail == int32(totalRequests-initialStock) {
		fmt.Println("PASS: Exactly 20 orders succeeded, 30 failed")
	} else {
		fmt.Printf("FAIL: Expected %d success/%d fail, got %d/%d\n",
			initialStock, totalRequests-initialStock, success, fail)
	}

	// Verify final stock in Redis
	finalStock, _ := rdb.Get(ctx, "stock:"+itemID).Int()
	fmt.Printf("Final Redis Stock: %d\n", finalStock)

	if finalStock == 0 {
		fmt.Println("PASS: Stock depleted to 0")
	} else {
		fmt.Printf("FAIL: Expected stock 0, got %d\n", finalStock)
	}
}
