// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package store owns the SQLite-backed intent record: which instances
// should exist, what ports they own, what repos they track. Observable
// container state (running/exited, IPs) is never duplicated here — it is
// read live from Docker and merged at query time. See docs/architecture.md
// §5, §10.1.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Open opens (creating if absent) the SQLite database at path, enables WAL
// mode, and applies any pending migrations. Safe to call from multiple
// concurrent processes — phase 1 has N CLI invocations sharing one file
// (see docs/architecture.md §12.4/CLI-first); WAL plus a busy timeout is
// what keeps that safe rather than merely usually-safe.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// A single connection avoids SQLITE_BUSY races between goroutines in one
	// process; cross-process contention is handled by the busy_timeout above.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("store: check schema_version: %w", err)
	}

	applied := 0
	if exists == 1 {
		row := s.db.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_version`)
		if err := row.Scan(&applied); err != nil {
			return fmt.Errorf("store: read schema version: %w", err)
		}
	}

	for i := applied; i < len(migrations); i++ {
		version := i + 1
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: apply migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
			version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit migration %d: %w", version, err)
		}
	}
	return nil
}
