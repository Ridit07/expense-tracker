package common

import (
	"encoding/json"
	"net/http"
)

// Envelope is the standard JSON response shape for the HTTP API.
type Envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data,omitempty"`
	Error   *APIError `json:"error,omitempty"`
}

// APIError is the machine-readable error body returned to clients.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// WriteSuccess writes a successful envelope with the given data payload.
func WriteSuccess(w http.ResponseWriter, status int, data any) {
	WriteJSON(w, status, Envelope{Success: true, Data: data})
}

// WriteError writes an error envelope with a machine-readable code.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, Envelope{
		Success: false,
		Error:   &APIError{Code: code, Message: message},
	})
}
