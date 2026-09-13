// Package store persists build runs so longpole can compare them over time.
//
// Every failure here is non-fatal by contract: a build that cannot be recorded
// is still a build that succeeded, and the caller prints its report regardless.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	dsn, err := sqliteFileDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection serializes writes made by this Store. Other longpole
	// processes have independent pools; their contention waits via the DSN's
	// busy timeout below.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if err := migrateActions(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func sqliteFileDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	query := u.Query()
	// Foreign-key enforcement is connection-local in SQLite. Put it in the DSN
	// so database/sql cannot open a replacement connection without it.
	query.Set("_foreign_keys", "on")
	query.Set("_journal_mode", "wal")
	query.Add("_pragma", "busy_timeout(30000)")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func migrateActions(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin action schema migration: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`PRAGMA table_info(actions)`)
	if err != nil {
		return fmt.Errorf("inspect action schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan action schema: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate action schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close action schema rows: %w", err)
	}

	if !columns["graph_id"] {
		if _, err := tx.Exec(`ALTER TABLE actions ADD COLUMN graph_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add action graph_id column: %w", err)
		}
		// The old reader exposed idx as Action.ID, so retain that identity for
		// rows written before graph_id was persisted separately.
		if _, err := tx.Exec(`UPDATE actions SET graph_id = idx`); err != nil {
			return fmt.Errorf("backfill action graph_id: %w", err)
		}
	}
	if !columns["deps"] {
		if _, err := tx.Exec(`ALTER TABLE actions ADD COLUMN deps TEXT NOT NULL DEFAULT 'null'`); err != nil {
			return fmt.Errorf("add action deps column: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit action schema migration: %w", err)
	}
	return nil
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
