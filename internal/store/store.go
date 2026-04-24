package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	// modernc.org/sqlite driver name is "sqlite".
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// SQLite with the modernc driver serialises writers internally; one conn
	// keeps semantics predictable for the indexer.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Additive migrations for pre-existing DBs. Silent-ignore "duplicate column"
	// errors since CREATE TABLE IF NOT EXISTS above never re-applies ALTERs.
	for _, stmt := range []string{
		`ALTER TABLE peers ADD COLUMN latitude REAL`,
		`ALTER TABLE peers ADD COLUMN longitude REAL`,
		`ALTER TABLE geoip_cache ADD COLUMN latitude REAL`,
		`ALTER TABLE geoip_cache ADD COLUMN longitude REAL`,
	} {
		_, _ = db.ExecContext(context.Background(), stmt)
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }
