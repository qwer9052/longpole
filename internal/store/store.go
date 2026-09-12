// Package store persists build runs so longpole can compare them over time.
//
// Every failure here is non-fatal by contract: a build that cannot be recorded
// is still a build that succeeded, and the caller prints its report regardless.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store is a handle on the run database.
type Store struct {
	db *sql.DB
}

// Run is one recorded build.
type Run struct {
	ID        int64
	Scope     string // module path plus working directory, so diffs compare like with like
	StartedAt int64  // unix nanoseconds
	Command   string
	GoVersion string
	GOOS      string
	GOARCH    string
	Cores     int
	WallNs    int64
	WorkNs    int64
	Ran       int
	Cached    int
	ExitCode  int
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	scope       TEXT    NOT NULL,
	started_at  INTEGER NOT NULL,
	command     TEXT    NOT NULL,
	go_version  TEXT    NOT NULL,
	goos        TEXT    NOT NULL,
	goarch      TEXT    NOT NULL,
	cores       INTEGER NOT NULL,
	wall_ns     INTEGER NOT NULL,
	work_ns     INTEGER NOT NULL,
	ran         INTEGER NOT NULL,
	cached      INTEGER NOT NULL,
	exit_code   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_scope_id ON runs(scope, id DESC);

CREATE TABLE IF NOT EXISTS actions (
	run_id     INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	idx        INTEGER NOT NULL,
	graph_id   INTEGER NOT NULL,
	mode       TEXT    NOT NULL,
	kind       INTEGER NOT NULL,
	package    TEXT    NOT NULL,
	deps       TEXT    NOT NULL,
	action_id  TEXT    NOT NULL,
	build_id   TEXT    NOT NULL,
	work_ns    INTEGER NOT NULL,
	wall_ns    INTEGER NOT NULL,
	queue_ns   INTEGER NOT NULL,
	cached     INTEGER NOT NULL,
	ran        INTEGER NOT NULL,
	PRIMARY KEY (run_id, idx)
);
CREATE INDEX IF NOT EXISTS actions_run_pkg ON actions(run_id, package);
`

// Open opens or creates the database at path, creating parent directories as
// needed.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	// Foreign-key enforcement is connection-local in SQLite. Put it in the DSN
	// so database/sql cannot open a replacement connection without it.
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=wal")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One writer at a time. The database is tiny and contention is nil, and
	// this avoids "database is locked" under concurrent builds.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DefaultPath is where runs are recorded when no override is given.
func DefaultPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}
	return filepath.Join(dir, "longpole", "runs.db"), nil
}

func (s *Store) countActions() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM actions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count actions: %w", err)
	}
	return n, nil
}
