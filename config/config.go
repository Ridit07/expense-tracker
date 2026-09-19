package config

import (
	"errors"
	"log"
	"os"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for the service. Values are sourced
// from environment variables (a local .env file is loaded for development).
type Config struct {
	DBReadURL  string
	DBWriteURL string
	HTTPPort   string
	JWTSecret  string
	Env        string
	LogLevel   string
}

// LoadConfig reads configuration from the environment. In local development a
// .env file is loaded if present; in production env vars are injected directly.
func LoadConfig() (*Config, error) {
	// Only load .env file if it exists (for local development).
	// In Docker, environment variables are set via docker-compose.yml.
	if _, err := os.Stat(".env"); err == nil {
		godotenv.Load()
	}

	cfg := &Config{
		DBReadURL:  os.Getenv("DB_READ_URL"),
		DBWriteURL: os.Getenv("DB_WRITE_URL"),
		HTTPPort:   os.Getenv("HTTP_PORT"),
		JWTSecret:  os.Getenv("JWT_SECRET"),
		Env:        os.Getenv("ENV"),
		LogLevel:   os.Getenv("LOG_LEVEL"),
	}

	// Validate required fields.
	if cfg.DBReadURL == "" || cfg.DBWriteURL == "" {
		log.Println("failed to load env: missing database URLs")
		return nil, errors.New("missing required environment variables")
	}

	if cfg.HTTPPort == "" {
		cfg.HTTPPort = "8080"
	}

	if cfg.JWTSecret == "" {
		log.Println("failed to load env: missing JWT_SECRET")
		return nil, errors.New("missing JWT_SECRET")
	}

	return cfg, nil
}
