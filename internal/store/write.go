package store

import (
	"fmt"
	"time"

	"github.com/qwer9052/longpole/internal/model"
)

// Save records a run and its actions, returning the new run id.
func (s *Store) Save(r Run, acts []model.Action) (int64, error) {
	if r.StartedAt == 0 {
		r.StartedAt = time.Now().UnixNano()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO runs
			(scope, started_at, command, go_version, goos, goarch,
			 cores, wall_ns, work_ns, ran, cached, exit_code)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Scope, r.StartedAt, r.Command, r.GoVersion, r.GOOS, r.GOARCH,
		r.Cores, r.WallNs, r.WorkNs, r.Ran, r.Cached, r.ExitCode)
	if err != nil {
		return 0, fmt.Errorf("insert run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read run id: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO actions
			(run_id, idx, mode, kind, package, action_id, build_id,
			 work_ns, wall_ns, queue_ns, cached, ran)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare action insert: %w", err)
	}
	defer stmt.Close()

	for i, a := range acts {
		if _, err := stmt.Exec(id, i, a.Mode, int(a.Kind), a.Package,
			a.ActionID, a.BuildID, a.WorkNs, a.WallNs, a.QueueNs,
			boolInt(a.Cached), boolInt(a.Ran)); err != nil {
			return 0, fmt.Errorf("insert action %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// Prune deletes all but the newest keep runs in a scope.
func (s *Store) Prune(scope string, keep int) error {
	_, err := s.db.Exec(`
		DELETE FROM runs
		WHERE scope = ?
		  AND id NOT IN (
			SELECT id FROM runs WHERE scope = ? ORDER BY id DESC LIMIT ?
		  )`, scope, scope, keep)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	// Foreign keys are on, so action rows cascade. Reclaim the space.
	_, _ = s.db.Exec(`DELETE FROM actions WHERE run_id NOT IN (SELECT id FROM runs)`)
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
