package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/go-sql-driver/mysql"
)

// Open opens a MySQL connection pool with recommended settings.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

// Migrate runs all pending up migrations from migrationsPath.
func Migrate(dsn, migrationsPath string) error {
	m, err := migrate.New(
		"file://"+migrationsPath,
		"mysql://"+stripQueryParams(dsn),
	)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}
	slog.Info("database migrations applied")
	return nil
}

// stripQueryParams removes query parameters from DSN for golang-migrate.
func stripQueryParams(dsn string) string {
	if i := strings.Index(dsn, "?"); i != -1 {
		return dsn[:i]
	}
	return dsn
}
