// Package store implements the SQLite persistence layer for Orchard MDM.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	if path == ":memory:" {
		// a shared in-memory database so every pooled connection sees the same data
		dsn = "file:orchard-mem-" + randomHex(6) + "?mode=memory&cache=shared&_pragma=busy_timeout(15000)&_pragma=foreign_keys(ON)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxIdleTime(5 * time.Minute)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if path != ":memory:" {
		_ = os.Chmod(path, 0o600)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle (used by tests and maintenance jobs).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for i, m := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range splitSQL(m) {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w\n%s", v, err, stmt)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)`, v, Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func splitSQL(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ";\n") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Now returns the current unix time in seconds.
func Now() int64 { return time.Now().Unix() }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mustJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalJSON[T any](s string) T {
	var v T
	if s != "" {
		_ = json.Unmarshal([]byte(s), &v)
	}
	return v
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func int64sToAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, s := range ids {
		out[i] = s
	}
	return out
}
