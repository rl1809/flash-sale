package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rl1809/flash-sale/internal/core/service"
)

type HTTPHandler struct {
	orderService *service.OrderService
}

type PurchaseHTTPRequest struct {
	RequestID string `json:"request_id"`
	UserID    string `json:"user_id"`
	ItemID    string `json:"item_id"`
	Quantity  int    `json:"quantity"`
}

type PurchaseHTTPResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func NewHTTPHandler(orderService *service.OrderService) *HTTPHandler {
	return &HTTPHandler{orderService: orderService}
}

func (h *HTTPHandler) Purchase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PurchaseHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, PurchaseHTTPResponse{
			Success: false,
			Message: "invalid request body",
		})
		return
	}

	if req.RequestID == "" || req.UserID == "" || req.ItemID == "" || req.Quantity <= 0 {
		writeJSON(w, http.StatusBadRequest, PurchaseHTTPResponse{
			Success: false,
			Message: "missing required fields",
		})
		return
	}

	err := h.orderService.Purchase(r.Context(), req.RequestID, req.UserID, req.ItemID, req.Quantity)
	if err != nil {
		status := http.StatusInternalServerError
		message := "internal error"

		if errors.Is(err, service.ErrDuplicateRequest) {
			status = http.StatusConflict
			message = "duplicate request"
		} else if errors.Is(err, service.ErrInsufficientStock) {
			status = http.StatusGone
			message = "sold out"
		}

		writeJSON(w, status, PurchaseHTTPResponse{
			Success: false,
			Message: message,
		})
		return
	}

	writeJSON(w, http.StatusOK, PurchaseHTTPResponse{
		Success: true,
		Message: "order placed successfully",
	})
}

func (h *HTTPHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
