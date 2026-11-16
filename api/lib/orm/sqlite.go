package orm

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"time"
)

type SqliteConfig struct {
	MaxIdleConns int
	MaxOpenConns int
}

func NewSqlite(sqliteConf *SqliteConfig, logwriter logger.Writer) *gorm.DB {
	if err := os.MkdirAll("./data", 0700); err != nil {
		panic(fmt.Errorf("create database directory: %w", err))
	}
	db, err := gorm.Open(sqlite.Open("./data/rustdeskapi.db?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger: logger.New(
			logwriter, // io writer
			logger.Config{
				SlowThreshold:             time.Second, // Slow SQL threshold
				LogLevel:                  logger.Warn, // Log level
				IgnoreRecordNotFoundError: true,        // Ignore ErrRecordNotFound error for logger
				ParameterizedQueries:      true,        // Don't include params in the SQL log
				Colorful:                  true,
			},
		),
	})
	if err != nil {
		panic(fmt.Errorf("open sqlite: %w", err))
	}
	sqlDB, err2 := db.DB()
	if err2 != nil {
		panic(fmt.Errorf("initialize sqlite: %w", err2))
	}
	// SetMaxIdleConns 设置空闲连接池中连接的最大数量
	sqlDB.SetMaxIdleConns(1)

	// SetMaxOpenConns 设置打开数据库连接的最大数量。
	sqlDB.SetMaxOpenConns(1)

	return db
}
