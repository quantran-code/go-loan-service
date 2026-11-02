package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open connects to SQLite and applies every pending goose migration. One
// connection: SQLite's writes serialise anyway, and a second connection would
// only wait behind the first while holding a transaction.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dsn, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	root, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("reading migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, root)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating migration provider: %w", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("applying migrations: %w", err)
	}
	return db, nil
}
