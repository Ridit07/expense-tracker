package db

import (
	"os"
	"strconv"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var readDB *gorm.DB
var writeDB *gorm.DB

// InitDB opens the read and write connection pools. Pool sizes are tunable via
// DB_MAX_OPEN_CONNS / DB_MAX_IDLE_CONNS (keep them tiny on serverless).
func InitDB(readURL, writeURL string) error {
	var err error

	if readDB, err = open(readURL); err != nil {
		return err
	}
	if writeDB, err = open(writeURL); err != nil {
		return err
	}

	maxOpen := envInt("DB_MAX_OPEN_CONNS", 50)
	maxIdle := envInt("DB_MAX_IDLE_CONNS", 10)

	if err := configurePool(readDB, maxOpen, maxIdle); err != nil {
		return err
	}
	return configurePool(writeDB, maxOpen, maxIdle)
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(
		// PreferSimpleProtocol disables prepared-statement caching so the
		// Supabase transaction pooler (pgbouncer) works under serverless.
		postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Warn)},
	)
}

func configurePool(gdb *gorm.DB, maxOpen, maxIdle int) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return nil
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func ReadConnection() *gorm.DB {
	return readDB
}

func WriteConnection() *gorm.DB {
	return writeDB
}
