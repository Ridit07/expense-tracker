package transport_http

import (
	"encoding/json"
	"io"
	"net/http"

	"expense-tracker/common"
	"expense-tracker/model"
	"expense-tracker/services"
)

type AuthHandler struct {
	svc *services.AuthService
}

func NewAuthHandler(svc *services.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

func (h *AuthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
}

type credentialsDTO struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type authResponseDTO struct {
	Token string  `json:"token"`
	User  userDTO `json:"user"`
}

type userDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Register(in.Email, in.Password, in.Name)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	common.WriteSuccess(w, http.StatusCreated, toAuthResponse(res.User, res.Token))
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Login(in.Email, in.Password)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	common.WriteSuccess(w, http.StatusOK, toAuthResponse(res.User, res.Token))
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentialsDTO, bool) {
	var in credentialsDTO
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return in, false
	}
	return in, true
}

func toAuthResponse(u *model.User, token string) authResponseDTO {
	return authResponseDTO{
		Token: token,
		User: userDTO{
			ID:    u.ID.String(),
			Email: u.Email,
			Name:  u.Name,
		},
	}
}
