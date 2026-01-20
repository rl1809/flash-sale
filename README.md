# Flash Sale System

A high-performance flash sale backend system built in Go, designed to handle massive concurrent purchase requests while preventing overselling through atomic stock operations.

## Features

- **Atomic Stock Decrement**: Uses Redis Lua scripts for atomic stock checks and decrements, ensuring no overselling under high concurrency
- **Idempotency**: Prevents duplicate purchases using Redis-based idempotency keys with TTL
- **Async Order Processing**: Worker pool pattern for asynchronous order persistence to MySQL
- **Dual API Support**: Both HTTP REST and gRPC endpoints
- **Graceful Shutdown**: Properly handles SIGINT/SIGTERM signals with connection draining
- **Hexagonal Architecture**: Clean separation of concerns using ports and adapters pattern

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Clients                              │
│                   (HTTP / gRPC)                             │
└──────────────────────┬──────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────┐
│                     Handlers                                │
│              (HTTP Handler / gRPC Handler)                  │
└──────────────────────┬──────────────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────────────┐
│                   Order Service                             │
│     ┌─────────────────────────────────────────────┐         │
│     │ 1. Check Idempotency (Redis SETNX)          │         │
│     │ 2. Decrement Stock (Redis Lua Script)       │         │
│     │ 3. Queue Order for Async Processing         │         │
│     └─────────────────────────────────────────────┘         │
└──────────────────────┬──────────────────────────────────────┘
                       │
         ┌─────────────┴─────────────┐
         ▼                           ▼
┌─────────────────┐         ┌─────────────────┐
│  Redis Adapter  │         │  MySQL Adapter  │
│   (Cache Port)  │         │ (Database Port) │
└────────┬────────┘         └────────┬────────┘
         │                           │
         ▼                           ▼
┌─────────────────┐         ┌─────────────────┐
│     Redis       │         │     MySQL       │
│  (Stock Cache)  │         │ (Order Storage) │
└─────────────────┘         └─────────────────┘
```

## Tech Stack

- **Go** 1.25+
- **Redis** 7 - Stock caching and idempotency
- **MySQL** 8 - Order persistence
- **gRPC** - High-performance RPC
- **Docker Compose** - Local development environment

## Prerequisites

- Go 1.25 or higher
- Docker and Docker Compose
- (Optional) Protocol Buffer compiler for regenerating gRPC code

## Getting Started

### 1. Start Infrastructure

```bash
docker-compose up -d
```

This starts:
- MySQL on port `3306` (credentials: root/root, database: flashsale)
- Redis on port `6379`

### 2. Run the Server

```bash
go run cmd/server/main.go
```

The server starts:
- HTTP server on `:8080`
- gRPC server on `:50051`

### 3. Test the API

**Health Check:**
```bash
curl http://localhost:8080/health
```

**Purchase Request (HTTP):**
```bash
curl -X POST http://localhost:8080/api/purchase \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "unique-request-123",
    "user_id": "user-456",
    "item_id": "iphone-15",
    "quantity": 1
  }'
```

## API Documentation

### HTTP Endpoints

#### POST /api/purchase

Place a purchase order.

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| request_id | string | Yes | Unique request ID for idempotency |
| user_id | string | Yes | User identifier |
| item_id | string | Yes | Item identifier |
| quantity | int | Yes | Purchase quantity (must be > 0) |

**Response:**
```json
{
  "success": true,
  "message": "order placed successfully"
}
```

**Error Responses:**
| Status | Message | Description |
|--------|---------|-------------|
| 400 | invalid request body | Malformed JSON |
| 400 | missing required fields | Required fields not provided |
| 409 | duplicate request | Same request_id was already processed |
| 410 | sold out | Insufficient stock |
| 500 | internal error | Server error |

#### GET /health

Health check endpoint.

### gRPC Service

```protobuf
service OrderService {
  rpc Purchase(PurchaseRequest) returns (PurchaseResponse);
}
```

## Project Structure

```
.
├── cmd/
│   ├── server/          # Main application entry point
│   │   └── main.go
│   └── stress_test/     # Stress testing tool
│       └── main.go
├── internal/
│   ├── adapter/
│   │   ├── handler/     # HTTP and gRPC handlers
│   │   │   ├── http_handler.go
│   │   │   ├── grpc_handler.go
│   │   │   └── pb/      # Generated protobuf code
│   │   └── storage/     # Database and cache adapters
│   │       ├── mysql_adapter.go
│   │       └── redis_adapter.go
│   ├── core/
│   │   ├── domain/      # Domain models
│   │   │   ├── order.go
│   │   │   └── inventory.go
│   │   └── service/     # Business logic
│   │       └── order_service.go
│   └── port/            # Interface definitions
│       ├── cache_repository.go
│       └── database_repository.go
├── migrations/
│   └── init.sql         # Database schema
├── proto/
│   └── order.proto      # gRPC service definition
├── tests/
│   └── integration_test.go
├── docker-compose.yml
├── go.mod
└── go.sum
```

## How It Works

### Purchase Flow

1. **Idempotency Check**: The service uses Redis `SETNX` to ensure each `request_id` is processed only once (24-hour TTL)

2. **Atomic Stock Decrement**: A Lua script runs atomically in Redis:
   ```lua
   local current = redis.call('GET', key)
   if current >= quantity then
       redis.call('DECRBY', key, quantity)
       return 1  -- success
   end
   return 0  -- insufficient stock
   ```

3. **Async Order Processing**: Successfully reserved orders are pushed to an in-memory channel and processed by a worker pool

4. **Persistence with Rollback**: Workers persist orders to MySQL. On failure, stock is rolled back in Redis

### Configuration

Default values (can be modified in `cmd/server/main.go`):

| Parameter | Default | Description |
|-----------|---------|-------------|
| HTTP Port | 8080 | HTTP server port |
| gRPC Port | 50051 | gRPC server port |
| Worker Count | 10 | Number of order processing workers |
| Queue Size | 10000 | Order queue buffer size |
| Initial Stock | 100 | Initial inventory stock |

## Testing

### Run Stress Test

The stress test simulates concurrent purchase requests:

```bash
go run cmd/stress_test/main.go
```

Example output:
```
========== STRESS TEST RESULTS ==========
Initial Stock:    20
Total Requests:   50
Successful:       20
Failed:           30
Duration:         15.234ms
==========================================
PASS: Exactly 20 orders succeeded, 30 failed
Final Redis Stock: 0
PASS: Stock depleted to 0
```

### Run Integration Tests

```bash
go test ./tests/... -v
```

### Run Unit Tests

```bash
go test ./internal/... -v
```

## Regenerating gRPC Code

If you modify `proto/order.proto`, regenerate the Go code:

```bash
protoc --go_out=. --go-grpc_out=. proto/order.proto
```

## License

MIT
