package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/rl1809/flash-sale/internal/adapter/handler"
	"github.com/rl1809/flash-sale/internal/adapter/handler/pb"
	"github.com/rl1809/flash-sale/internal/adapter/storage"
	"github.com/rl1809/flash-sale/internal/core/domain"
	"github.com/rl1809/flash-sale/internal/core/service"
	"github.com/rl1809/flash-sale/internal/port"
)

const (
	httpPort     = ":8080"
	grpcPort     = ":50051"
	mysqlDSN     = "root:root@tcp(localhost:3306)/flashsale?parseTime=true"
	redisAddr    = "localhost:6379"
	workerCount  = 10
	queueSize    = 10000
	initialStock = 100
	itemID       = "iphone-15"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize MySQL
	db, err := sql.Open("mysql", mysqlDSN)
	if err != nil {
		log.Fatalf("failed to connect mysql: %v", err)
	}
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("failed to ping mysql: %v", err)
	}
	log.Println("connected to mysql")

	// Initialize Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		PoolSize: 100,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect redis: %v", err)
	}
	log.Println("connected to redis")

	// Initialize adapters
	redisAdapter := storage.NewRedisAdapter(rdb)
	mysqlAdapter := storage.NewMySQLAdapter(db)

	// Sync stock to Redis
	if err := redisAdapter.SetStock(ctx, itemID, initialStock); err != nil {
		log.Fatalf("failed to set initial stock: %v", err)
	}
	log.Printf("initialized stock: %s = %d", itemID, initialStock)

	// Initialize service
	orderService := service.NewOrderService(redisAdapter, queueSize)

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			workerLoop(id, orderService.GetOrderQueue(), mysqlAdapter, redisAdapter)
		}(i)
	}
	log.Printf("started %d workers", workerCount)

	// Initialize gRPC server
	grpcServer := grpc.NewServer()
	grpcHandler := handler.NewGRPCHandler(orderService)
	pb.RegisterOrderServiceServer(grpcServer, grpcHandler)

	// Start gRPC server
	lis, err := net.Listen("tcp", grpcPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	go func() {
		log.Printf("gRPC server listening on %s", grpcPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	// Initialize HTTP server
	httpHandler := handler.NewHTTPHandler(orderService)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", httpHandler.HealthCheck)
	mux.HandleFunc("/api/purchase", httpHandler.Purchase)

	httpServer := &http.Server{
		Addr:    httpPort,
		Handler: mux,
	}

	go func() {
		log.Printf("HTTP server listening on %s", httpPort)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")

	// Stop HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)
	log.Println("HTTP server stopped")

	// Stop gRPC server
	grpcServer.GracefulStop()
	log.Println("gRPC server stopped")

	// Close order queue and wait for workers
	orderService.Close()
	wg.Wait()
	log.Println("workers stopped")

	// Close connections
	rdb.Close()
	db.Close()
	log.Println("connections closed")
}

func workerLoop(id int, queue <-chan domain.Order, db port.DatabaseRepository, cache port.CacheRepository) {
	for order := range queue {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		if err := db.CreateOrder(ctx, order); err != nil {
			log.Printf("worker %d: failed to save order %s: %v", id, order.ID, err)

			// Rollback: restore stock in Redis
			if rollbackErr := cache.IncrementStock(ctx, order.ItemID, order.Quantity); rollbackErr != nil {
				log.Printf("worker %d: CRITICAL rollback failed for order %s: %v", id, order.ID, rollbackErr)
			} else {
				log.Printf("worker %d: rolled back stock for order %s", id, order.ID)
			}
		} else {
			log.Printf("worker %d: saved order %s", id, order.ID)
		}

		cancel()
	}
}
