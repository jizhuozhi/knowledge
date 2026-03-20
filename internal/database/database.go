package database

import (
	"fmt"

	"github.com/jizhuozhi/knowledge/internal/config"
	"github.com/jizhuozhi/knowledge/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// InitDB initializes the database connection
func InitDB(cfg config.DatabaseConfig) error {
	dsn := cfg.DSN()
	
	logLevel := logger.Warn
	if config.GlobalConfig.App.Debug {
		logLevel = logger.Info
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)

	DB = db
	return nil
}

// AutoMigrate runs auto migration for models
func AutoMigrate() error {
	return DB.AutoMigrate(
		&models.Tenant{},
		&models.KnowledgeBase{},
		&models.Document{},
		&models.DocumentTOCIndex{},
		&models.Chunk{},
		&models.GraphEntity{},
		&models.GraphRelation{},
		&models.ProcessingEvent{},
		&models.LLMUsageRecord{},
	)
}

// Close closes the database connection
func Close() error {
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// GetDB returns the database instance for a specific context
func GetDB() *gorm.DB {
	return DB
}

// TenantDB returns a database session scoped to a tenant
func TenantDB(tenantID string) *gorm.DB {
	return DB.Where("tenant_id = ?", tenantID)
}
