package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/qwer9052/longpole/internal/model"
)

// Load returns a run and its actions.
func (s *Store) Load(id int64) (Run, []model.Action, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, nil, fmt.Errorf("begin load: %w", err)
	}
	defer tx.Rollback()

	var r Run
	err = tx.QueryRow(`
		SELECT id, scope, started_at, command, go_version, goos, goarch,
		       cores, wall_ns, work_ns, ran, cached, exit_code
		FROM runs WHERE id = ?`, id).
		Scan(&r.ID, &r.Scope, &r.StartedAt, &r.Command, &r.GoVersion,
			&r.GOOS, &r.GOARCH, &r.Cores, &r.WallNs, &r.WorkNs,
			&r.Ran, &r.Cached, &r.ExitCode)
	if err == sql.ErrNoRows {
		return Run{}, nil, fmt.Errorf("no run #%d", id)
	}
	if err != nil {
		return Run{}, nil, fmt.Errorf("load run: %w", err)
	}

	rows, err := tx.Query(`
		SELECT graph_id, mode, kind, package, deps, action_id, build_id,
		       work_ns, wall_ns, queue_ns, cached, ran
		FROM actions WHERE run_id = ? ORDER BY idx`, id)
	if err != nil {
		return r, nil, fmt.Errorf("load actions: %w", err)
	}
	defer rows.Close()

	var acts []model.Action
	for i := 0; rows.Next(); i++ {
		var a model.Action
		var kind, cached, ran int
		var deps string
		if err := rows.Scan(&a.ID, &a.Mode, &kind, &a.Package, &deps, &a.ActionID,
			&a.BuildID, &a.WorkNs, &a.WallNs, &a.QueueNs, &cached, &ran); err != nil {
			return r, nil, fmt.Errorf("scan action: %w", err)
		}
		if err := json.Unmarshal([]byte(deps), &a.Deps); err != nil {
			return r, nil, fmt.Errorf("decode action %d dependencies: %w", i, err)
		}
		a.Kind = model.Kind(kind)
		a.Cached = cached == 1
		a.Ran = ran == 1
		acts = append(acts, a)
	}
	if err := rows.Err(); err != nil {
		return r, nil, fmt.Errorf("iterate actions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return r, nil, fmt.Errorf("close action rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return r, nil, fmt.Errorf("commit load: %w", err)
	}
	return r, acts, nil
}

// Recent returns up to limit runs in a scope, newest first.
func (s *Store) Recent(scope string, limit int) ([]Run, error) {
	if limit < 0 {
		return nil, fmt.Errorf("recent limit must be non-negative: %d", limit)
	}
	rows, err := s.db.Query(`
		SELECT id, scope, started_at, command, go_version, goos, goarch,
		       cores, wall_ns, work_ns, ran, cached, exit_code
		FROM runs WHERE scope = ? ORDER BY id DESC LIMIT ?`, scope, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Scope, &r.StartedAt, &r.Command,
			&r.GoVersion, &r.GOOS, &r.GOARCH, &r.Cores, &r.WallNs,
			&r.WorkNs, &r.Ran, &r.Cached, &r.ExitCode); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	return out, nil
}

// Previous returns the id of the run immediately before id in the same scope,
// or 0 when there is none.
func (s *Store) Previous(scope string, id int64) (int64, error) {
	var prev int64
	err := s.db.QueryRow(`
		SELECT id FROM runs
		WHERE scope = ? AND id < ?
		ORDER BY id DESC LIMIT 1`, scope, id).Scan(&prev)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find previous run: %w", err)
	}
	return prev, nil
}
