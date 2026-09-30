package transport_http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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
	// Registered alongside /{id}: a literal segment outranks a wildcard in
	// Go's pattern matcher, so "sync-state" is never read as an id.
	mux.HandleFunc("GET /api/v1/messages/sync-state", h.syncState)
	mux.HandleFunc("GET /api/v1/messages/{id}", h.get)
}

// maxIngestBytes bounds the request body. The macOS backfill posts hundreds of
// messages per call, which 1MB no longer covers.
const maxIngestBytes = 5 << 20

// ---- request/response DTOs ----

type ingestMessageDTO struct {
	Sender     string `json:"sender"`
	Body       string `json:"body"`
	Source     string `json:"source"`
	ReceivedAt string `json:"received_at"` // RFC3339; optional
	ExternalID string `json:"external_id"` // source's own id, e.g. chat.db guid; optional
}

// ingestRequest accepts either a single message or a batch. If Messages is set
// it takes precedence; otherwise the top-level fields are treated as one message.
type ingestRequest struct {
	Messages []ingestMessageDTO `json:"messages"`
	ingestMessageDTO
}

type ingestResultDTO struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Duplicate  bool   `json:"duplicate"`
	ExternalID string `json:"external_id,omitempty"`
}

type ingestResponseDTO struct {
	Ingested  int               `json:"ingested"`
	Duplicate int               `json:"duplicate"`
	Rejected  int               `json:"rejected"` // stored, but classified as a non-transaction
	Results   []ingestResultDTO `json:"results"`
}

// ---- handlers ----

func (h *MessageHandler) ingest(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())

	// MaxBytesReader rather than LimitReader: a limit reader truncates
	// silently, and the client then gets "not valid JSON" for what is really an
	// oversized batch — an error that sends them debugging the wrong thing.
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			common.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				fmt.Sprintf("request body exceeds the %d MB limit; send fewer messages per batch", maxIngestBytes>>20))
			return
		}
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

	if len(dtos) > services.MaxBatchSize {
		common.WriteError(w, http.StatusBadRequest, "batch_too_large",
			fmt.Sprintf("batch contains %d messages; the maximum is %d per request",
				len(dtos), services.MaxBatchSize))
		return
	}

	inputs := make([]services.IngestInput, 0, len(dtos))
	for _, d := range dtos {
		receivedAt, perr := parseReceivedAt(d.ReceivedAt)
		if perr != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid_received_at", "received_at must be RFC3339")
			return
		}
		source, serr := parseSource(d.Source)
		if serr != nil {
			common.WriteError(w, http.StatusBadRequest, "invalid_input", serr.Error())
			return
		}
		inputs = append(inputs, services.IngestInput{
			Sender:     d.Sender,
			Body:       d.Body,
			Source:     source,
			ReceivedAt: receivedAt,
			ExternalID: d.ExternalID,
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
		if res.Message.Status == model.StatusRejected {
			resp.Rejected++
		}
		resp.Results = append(resp.Results, ingestResultDTO{
			ID:         res.Message.ID.String(),
			Status:     string(res.Message.Status),
			Duplicate:  res.Duplicate,
			ExternalID: res.Message.ExternalID,
		})
	}

	common.WriteSuccess(w, http.StatusCreated, resp)
}

// syncState answers "what did you last receive from me on this source?" so a
// pull-based sync tool can resume from a watermark instead of replaying its
// whole history after a reinstall.
func (h *MessageHandler) syncState(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())

	source, err := parseSource(r.URL.Query().Get("source"))
	if err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}

	state, err := h.svc.SyncState(userID, source)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	common.WriteSuccess(w, http.StatusOK, state)
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

// parseSource validates against the known source set. An empty value is left
// empty for the caller to interpret (ingest defaults it, sync-state reads it as
// "all sources"); anything unrecognised is rejected rather than stored, since a
// typo'd source silently becomes a value no later stage can act on.
func parseSource(s string) (model.MessageSource, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	src := model.MessageSource(s)
	if !src.Valid() {
		sources := model.MessageSources()
		known := make([]string, 0, len(sources))
		for _, v := range sources {
			known = append(known, string(v))
		}
		return "", fmt.Errorf("unknown source %q; expected one of: %s", s, strings.Join(known, ", "))
	}
	return src, nil
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
