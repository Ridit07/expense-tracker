package db

import (
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var readDB *gorm.DB
var writeDB *gorm.DB

// InitDB opens the read and write connection pools. A read/write split is used
// so heavy analytics reads can later be pointed at a replica without touching
// the write path.
func InitDB(
	readURL string,
	writeURL string,
) error {

	var err error

	readDB, err = gorm.Open(
		postgres.Open(readURL),
		&gorm.Config{
			Logger: logger.Default.LogMode(logger.Info),
		},
	)

	if err != nil {
		return err
	}

	writeDB, err = gorm.Open(
		postgres.Open(writeURL),
		&gorm.Config{
			Logger: logger.Default.LogMode(logger.Info),
		},
	)

	if err != nil {
		return err
	}

	// Configure read pool.
	readSQLDB, err := readDB.DB()

	if err != nil {
		return err
	}

	readSQLDB.SetMaxOpenConns(50)
	readSQLDB.SetMaxIdleConns(10)
	readSQLDB.SetConnMaxLifetime(time.Hour)

	// Configure write pool.
	writeSQLDB, err := writeDB.DB()

	if err != nil {
		return err
	}

	writeSQLDB.SetMaxOpenConns(50)
	writeSQLDB.SetMaxIdleConns(10)
	writeSQLDB.SetConnMaxLifetime(time.Hour)

	return nil
}

func ReadConnection() *gorm.DB {
	return readDB
}

func WriteConnection() *gorm.DB {
	return writeDB
}
