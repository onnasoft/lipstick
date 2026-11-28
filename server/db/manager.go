package db

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/OnnaSoft/lipstick/server/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
	connMu            sync.Mutex
	defaultConnection *gorm.DB
)

func NewConnection(conf config.DatabaseConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s user=%s dbname=%s sslmode=%s password=%s",
		conf.Host, conf.User, conf.Database, conf.SSLMode, conf.Password,
	)

	connection, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if os.Getenv("DEBUG") == "true" {
		db, _ := connection.DB()
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		fmt.Println("DEBUG mode: Database logging enabled.")
	}

	return connection, nil
}

// GetConnection returns the shared database handle, opening it on first use.
// It is safe to call from several goroutines.
func GetConnection(conf config.DatabaseConfig) (*gorm.DB, error) {
	connMu.Lock()
	defer connMu.Unlock()

	if defaultConnection == nil {
		connection, err := NewConnection(conf)
		if err != nil {
			return nil, err
		}
		defaultConnection = connection
	}

	return defaultConnection, nil
}

func CloseConnection() {
	if defaultConnection != nil {
		sqlDB, err := defaultConnection.DB()
		if err != nil {
			log.Printf("failed to get native DB connection: %v", err)
		}
		sqlDB.Close()
	}
}

func Migrate(conf config.DatabaseConfig) {
	connection, err := GetConnection(conf)
	if err != nil {
		log.Fatal(err)
	}

	if err := connection.AutoMigrate(&Domain{}, &DailyConsumption{}, &RequestLogEntry{}, &VisitorUser{}, &VisitorToken{}); err != nil {
		log.Fatal(err.Error())
	}
	// Visitor users used to hold a password hash. That scheme is gone: the column is
	// dropped, and users created before this change have no secret, so they cannot
	// sign requests until they are created again.
	if connection.Migrator().HasColumn(&VisitorUser{}, "password_hash") {
		if err := connection.Migrator().DropColumn(&VisitorUser{}, "password_hash"); err != nil {
			log.Fatal(err.Error())
		}
	}
}
