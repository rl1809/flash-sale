package main

import (
	"context"
	"database/sql"
	"log"
	"net"
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
)

const (
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
			workerLoop(id, orderService.GetOrderQueue(), mysqlAdapter)
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
		log.Printf("grpc server listening on %s", grpcPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("grpc server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")

	// Stop gRPC server
	grpcServer.GracefulStop()
	log.Println("grpc server stopped")

	// Close order queue and wait for workers
	orderService.Close()
	wg.Wait()
	log.Println("workers stopped")

	// Close connections
	rdb.Close()
	db.Close()
	log.Println("connections closed")
}

func workerLoop(id int, queue <-chan domain.Order, db *storage.MySQLAdapter) {
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
