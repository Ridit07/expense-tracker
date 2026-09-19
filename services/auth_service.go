package services

import (
	"fmt"
	"strings"
	"time"

	"expense-tracker/db"
	apperrors "expense-tracker/errors"
	"expense-tracker/model"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthService struct {
	jwtSecret []byte
	tokenTTL  time.Duration
}

func NewAuthService(jwtSecret string) *AuthService {
	return &AuthService{
		jwtSecret: []byte(jwtSecret),
		tokenTTL:  30 * 24 * time.Hour,
	}
}

type AuthResult struct {
	User  *model.User
	Token string
}

func (s *AuthService) Register(email, password, name string) (*AuthResult, error) {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("%w: a valid email is required", apperrors.ErrInvalidInput)
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("%w: password must be at least 8 characters", apperrors.ErrInvalidInput)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}

	user := &model.User{
		Email:        email,
		PasswordHash: string(hash),
		Name:         strings.TrimSpace(name),
	}

	if err := db.WriteConnection().Create(user).Error; err != nil {
		if isDuplicateKey(err) {
			return nil, fmt.Errorf("%w: email already registered", apperrors.ErrConflict)
		}
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}

	token, err := s.GenerateToken(user.ID.String())
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: user, Token: token}, nil
}

func (s *AuthService) Login(email, password string) (*AuthResult, error) {
	email = normalizeEmail(email)

	var user model.User
	err := db.ReadConnection().Where("email = ?", email).First(&user).Error
	if err != nil {
		if apperrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.ErrUnauthorized
		}
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, apperrors.ErrUnauthorized
	}

	token, err := s.GenerateToken(user.ID.String())
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: &user, Token: token}, nil
}

func (s *AuthService) GenerateToken(userID string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.tokenTTL)),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}
	return signed, nil
}

// ParseToken validates the token and returns the user id from the subject claim.
func (s *AuthService) ParseToken(tokenStr string) (string, error) {
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, apperrors.ErrUnauthorized
		}
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid || claims.Subject == "" {
		return "", apperrors.ErrUnauthorized
	}
	return claims.Subject, nil
}

func normalizeEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

func isDuplicateKey(err error) bool {
	if apperrors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "23505")
}
