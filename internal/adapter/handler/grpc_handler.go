package handler

import (
	"context"
	"errors"

	"github.com/rl1809/flash-sale/internal/adapter/handler/pb"
	"github.com/rl1809/flash-sale/internal/core/service"
)

type GRPCHandler struct {
	pb.UnimplementedOrderServiceServer
	orderService *service.OrderService
}

func NewGRPCHandler(orderService *service.OrderService) *GRPCHandler {
	return &GRPCHandler{orderService: orderService}
}

func (h *GRPCHandler) Purchase(ctx context.Context, req *pb.PurchaseRequest) (*pb.PurchaseResponse, error) {
	err := h.orderService.Purchase(ctx, req.GetRequestId(), req.GetUserId(), req.GetItemId(), int(req.GetQuantity()))
	if err != nil {
		if errors.Is(err, service.ErrDuplicateRequest) {
			return &pb.PurchaseResponse{
				Success: false,
				Message: "duplicate request",
			}, nil
		}
		if errors.Is(err, service.ErrInsufficientStock) {
			return &pb.PurchaseResponse{
				Success: false,
				Message: "sold out",
			}, nil
		}
		return &pb.PurchaseResponse{
			Success: false,
			Message: "internal error",
		}, nil
	}

	return &pb.PurchaseResponse{
		Success: true,
		Message: "order placed successfully",
	}, nil
}
