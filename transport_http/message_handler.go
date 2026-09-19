package transport_http

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"expense-tracker/common"
	apperrors "expense-tracker/errors"
	"expense-tracker/model"
	"expense-tracker/services"
)

// MessageHandler exposes the raw-message ingestion API.
type MessageHandler struct {
	svc *services.MessageService
}

func NewMessageHandler(svc *services.MessageService) *MessageHandler {
	return &MessageHandler{svc: svc}
}

// Register wires the handler's routes onto the mux. Go 1.22+ method+path
// patterns keep routing dependency-free.
func (h *MessageHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/messages", h.ingest)
	mux.HandleFunc("GET /api/v1/messages", h.list)
	mux.HandleFunc("GET /api/v1/messages/{id}", h.get)
}

// ---- request/response DTOs ----

type ingestMessageDTO struct {
	Sender     string `json:"sender"`
	Body       string `json:"body"`
	Source     string `json:"source"`
	ReceivedAt string `json:"received_at"` // RFC3339; optional
}

// ingestRequest accepts either a single message or a batch. If Messages is set
// it takes precedence; otherwise the top-level fields are treated as one message.
type ingestRequest struct {
	Messages []ingestMessageDTO `json:"messages"`
	ingestMessageDTO
}

type ingestResultDTO struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Duplicate bool   `json:"duplicate"`
}

type ingestResponseDTO struct {
	Ingested  int               `json:"ingested"`
	Duplicate int               `json:"duplicate"`
	Results   []ingestResultDTO `json:"results"`
}

// ---- handlers ----

func (h *MessageHandler) ingest(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB cap
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid_body", "could not read request body")
		return
	}

	var req ingestRequest
	if err := json.Unmarshal(body, &req); err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}

	dtos := req.Messages
	if len(dtos) == 0 {
		// Single-message form.
		dtos = []ingestMessageDTO{req.ingestMessageDTO}
	}

	inputs := make([]services.IngestInput, 0, len(dtos))
	for _, d := range dtos {
		receivedAt, perr := parseReceivedAt(d.ReceivedAt)
		if perr != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid_received_at", "received_at must be RFC3339")
			return
		}
		inputs = append(inputs, services.IngestInput{
			Sender:     d.Sender,
			Body:       d.Body,
			Source:     model.MessageSource(d.Source),
			ReceivedAt: receivedAt,
		})
	}

	results, err := h.svc.IngestBatch(userID, inputs)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := ingestResponseDTO{Results: make([]ingestResultDTO, 0, len(results))}
	for _, res := range results {
		if res.Duplicate {
			resp.Duplicate++
		} else {
			resp.Ingested++
		}
		resp.Results = append(resp.Results, ingestResultDTO{
			ID:        res.Message.ID.String(),
			Status:    string(res.Message.Status),
			Duplicate: res.Duplicate,
		})
	}

	common.WriteSuccess(w, http.StatusCreated, resp)
}

func (h *MessageHandler) list(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())

	status := model.MessageStatus(r.URL.Query().Get("status"))
	limit := parseIntDefault(r.URL.Query().Get("limit"), 50)

	msgs, err := h.svc.List(userID, status, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	common.WriteSuccess(w, http.StatusOK, msgs)
}

func (h *MessageHandler) get(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id := r.PathValue("id")

	msg, err := h.svc.GetByID(userID, id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	common.WriteSuccess(w, http.StatusOK, msg)
}

// ---- helpers ----

func parseReceivedAt(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// writeServiceError maps domain errors to HTTP responses.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case apperrors.Is(err, apperrors.ErrInvalidInput):
		common.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case apperrors.Is(err, apperrors.ErrUnauthorized):
		common.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
	case apperrors.Is(err, apperrors.ErrConflict):
		common.WriteError(w, http.StatusConflict, "conflict", err.Error())
	case apperrors.Is(err, apperrors.ErrNotFound):
		common.WriteError(w, http.StatusNotFound, "not_found", "resource not found")
	default:
		common.WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
	}
}
