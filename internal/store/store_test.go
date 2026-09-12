package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/qwer9052/longpole/internal/model"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleRun(scope string) Run {
	return Run{
		Scope:     scope,
		StartedAt: 1_789_120_000_000_000_000,
		Command:   "go build ./...",
		GoVersion: "go1.27.1",
		GOOS:      "windows",
		GOARCH:    "amd64",
		Cores:     8,
		WallNs:    12_400_000_000,
		WorkNs:    23_100_000_000,
		Ran:       195,
		Cached:    197,
		ExitCode:  0,
	}
}

func sampleActions() []model.Action {
	return []model.Action{
		{ID: 7, Mode: "build", Kind: model.KindCompile, Package: "example.com/a",
			Deps: []int{}, ActionID: "AAAA", BuildID: "build-a", WorkNs: 2_000_000_000,
			WallNs: 2_100_000_000, QueueNs: 100_000_000, Ran: true},
		{ID: 42, Mode: "link", Kind: model.KindLink, Package: "example.com/b",
			Deps: []int{7}, ActionID: "BBBB", BuildID: "build-b", WorkNs: 3_000_000_000,
			WallNs: 3_200_000_000, QueueNs: 200_000_000, Cached: true},
	}
}

func TestSaveAndLoad(t *testing.T) {
	s := openTemp(t)
	id, err := s.Save(sampleRun("mod@/dir"), sampleActions())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if id <= 0 {
		t.Fatalf("run id = %d, want positive", id)
	}

	got, acts, err := s.Load(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	wantRun := sampleRun("mod@/dir")
	wantRun.ID = id
	if !reflect.DeepEqual(got, wantRun) {
		t.Errorf("run = %#v, want %#v", got, wantRun)
	}
	if want := sampleActions(); !reflect.DeepEqual(acts, want) {
		t.Errorf("actions = %#v, want %#v", acts, want)
	}
}

func TestLoadUsesSingleSnapshot(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs.db")
	reader, err := Open(p)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()
	writer, err := Open(p)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()

	action := model.Action{ID: 7, Mode: "build", Kind: model.KindCompile,
		Package: "example.com/snapshot", ActionID: "SNAP", Ran: true}
	id, err := reader.Save(sampleRun("snapshot"), []model.Action{action})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 2_000; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}

			tx, err := writer.db.Begin()
			if err != nil {
				done <- fmt.Errorf("begin delete: %w", err)
				return
			}
			if _, err := tx.Exec(`DELETE FROM runs WHERE id = ?`, id); err != nil {
				tx.Rollback()
				done <- fmt.Errorf("delete run: %w", err)
				return
			}
			if err := tx.Commit(); err != nil {
				done <- fmt.Errorf("commit delete: %w", err)
				return
			}
			runtime.Gosched()

			tx, err = writer.db.Begin()
			if err != nil {
				done <- fmt.Errorf("begin insert: %w", err)
				return
			}
			_, err = tx.Exec(`
				INSERT INTO runs
					(id, scope, started_at, command, go_version, goos, goarch,
					 cores, wall_ns, work_ns, ran, cached, exit_code)
				VALUES (?, 'snapshot', 1, 'go build', 'go1.27', 'windows', 'amd64',
				        8, 1, 1, 1, 0, 0)`, id)
			if err != nil {
				tx.Rollback()
				done <- fmt.Errorf("insert run: %w", err)
				return
			}
			_, err = tx.Exec(`
				INSERT INTO actions
					(run_id, idx, graph_id, mode, kind, package, deps, action_id,
					 build_id, work_ns, wall_ns, queue_ns, cached, ran)
				VALUES (?, 0, 7, 'build', 1, 'example.com/snapshot', 'null', 'SNAP',
				        '', 0, 0, 0, 0, 1)`, id)
			if err != nil {
				tx.Rollback()
				done <- fmt.Errorf("insert action: %w", err)
				return
			}
			if err := tx.Commit(); err != nil {
				done <- fmt.Errorf("commit insert: %w", err)
				return
			}
			runtime.Gosched()
		}
		done <- nil
	}()

	loads := 0
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("writer: %v", err)
			}
			if loads == 0 {
				t.Fatal("no complete loads observed")
			}
			return
		default:
		}

		_, acts, err := reader.Load(id)
		if err != nil {
			if !strings.Contains(err.Error(), fmt.Sprintf("no run #%d", id)) {
				close(stop)
				<-done
				t.Fatalf("load: %v", err)
			}
			continue
		}
		loads++
		if len(acts) != 1 || acts[0].ActionID != "SNAP" {
			close(stop)
			<-done
			t.Fatalf("load returned run with actions %#v", acts)
		}
	}
}

func TestRunIDsIncrease(t *testing.T) {
	s := openTemp(t)
	a, _ := s.Save(sampleRun("x"), sampleActions())
	b, _ := s.Save(sampleRun("x"), sampleActions())
	if b <= a {
		t.Errorf("second run id %d is not greater than %d", b, a)
	}
}

func TestRecentIsScopedAndNewestFirst(t *testing.T) {
	s := openTemp(t)
	s.Save(sampleRun("proj-a"), sampleActions())
	s.Save(sampleRun("proj-b"), sampleActions())
	last, _ := s.Save(sampleRun("proj-a"), sampleActions())

	runs, err := s.Recent("proj-a", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs for proj-a, want 2", len(runs))
	}
	if runs[0].ID != last {
		t.Errorf("newest run is %d, want %d", runs[0].ID, last)
	}
}

func TestRecentZeroLimitReturnsNoRuns(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Save(sampleRun("p"), sampleActions()); err != nil {
		t.Fatalf("save: %v", err)
	}

	runs, err := s.Recent("p", 0)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("got %d runs, want none", len(runs))
	}
}

func TestRecentRejectsNegativeLimit(t *testing.T) {
	s := openTemp(t)
	_, err := s.Recent("p", -1)
	if err == nil {
		t.Fatal("recent accepted a negative limit")
	}
	if !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Errorf("recent error = %q, want negative-limit context", err)
	}
}

func TestPreviousReturnsTheRunBefore(t *testing.T) {
	s := openTemp(t)
	first, _ := s.Save(sampleRun("p"), sampleActions())
	second, _ := s.Save(sampleRun("p"), sampleActions())

	prev, err := s.Previous("p", second)
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	if prev != first {
		t.Errorf("previous = %d, want %d", prev, first)
	}
}

func TestPreviousOnFirstRunIsZero(t *testing.T) {
	s := openTemp(t)
	id, _ := s.Save(sampleRun("p"), sampleActions())
	prev, err := s.Previous("p", id)
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	if prev != 0 {
		t.Errorf("previous = %d, want 0 for the first run", prev)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	s := openTemp(t)
	var ids []int64
	for i := 0; i < 5; i++ {
		id, _ := s.Save(sampleRun("p"), sampleActions())
		ids = append(ids, id)
	}
	if err := s.Prune("p", 2); err != nil {
		t.Fatalf("prune: %v", err)
	}
	runs, _ := s.Recent("p", 10)
	if len(runs) != 2 {
		t.Fatalf("after prune got %d runs, want 2", len(runs))
	}
	if runs[0].ID != ids[4] || runs[1].ID != ids[3] {
		t.Errorf("prune kept the wrong runs: %d, %d", runs[0].ID, runs[1].ID)
	}
}

func TestPruneZeroRemovesOnlyTheScope(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Save(sampleRun("p"), sampleActions()); err != nil {
		t.Fatalf("save p: %v", err)
	}
	other, err := s.Save(sampleRun("other"), sampleActions())
	if err != nil {
		t.Fatalf("save other: %v", err)
	}

	if err := s.Prune("p", 0); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if runs, err := s.Recent("p", 10); err != nil {
		t.Fatalf("recent p: %v", err)
	} else if len(runs) != 0 {
		t.Errorf("got %d runs for pruned scope, want none", len(runs))
	}
	if runs, err := s.Recent("other", 10); err != nil {
		t.Fatalf("recent other: %v", err)
	} else if len(runs) != 1 || runs[0].ID != other {
		t.Errorf("other scope runs = %#v, want run %d", runs, other)
	}
}

func TestPruneRejectsNegativeKeep(t *testing.T) {
	s := openTemp(t)
	err := s.Prune("p", -1)
	if err == nil {
		t.Fatal("prune accepted a negative keep count")
	}
	if !strings.Contains(err.Error(), "keep must be non-negative") {
		t.Errorf("prune error = %q, want negative-keep context", err)
	}
}

func TestPruneRemovesOrphanedActions(t *testing.T) {
	s := openTemp(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Save(sampleRun("p"), sampleActions()); err != nil {
			t.Fatalf("save p: %v", err)
		}
	}
	if _, err := s.Save(sampleRun("other"), sampleActions()); err != nil {
		t.Fatalf("save other: %v", err)
	}
	if err := s.Prune("p", 1); err != nil {
		t.Fatalf("prune: %v", err)
	}
	n, err := s.countActions()
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("got %d action rows after scoped prune, want 4", n)
	}
}

func TestPruneRollsBackWhenCleanupFails(t *testing.T) {
	s := openTemp(t)
	if _, err := s.Save(sampleRun("p"), sampleActions()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.db.Exec(`DROP TABLE actions`); err != nil {
		t.Fatalf("drop actions: %v", err)
	}

	err := s.Prune("p", 0)
	if err == nil {
		t.Fatal("prune succeeded with no actions table")
	}
	if !strings.Contains(err.Error(), "remove orphaned actions") {
		t.Errorf("prune error = %q, want cleanup context", err)
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE scope = ?`, "p").Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if n != 1 {
		t.Errorf("run count = %d, want 1 after rollback", n)
	}
}

func TestOpenEnablesForeignKeysOnNewConnections(t *testing.T) {
	s := openTemp(t)
	s.db.SetMaxIdleConns(0)

	var enabled int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if enabled != 1 {
		t.Errorf("foreign_keys = %d on replacement connection, want 1", enabled)
	}
}

func TestSQLiteFileDSNEscapesQuestionMark(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs?#%.db")
	dsn, err := sqliteFileDSN(p)
	if err != nil {
		t.Fatalf("construct DSN: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	want, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("absolute path: %v", err)
	}
	got := u.Path
	if runtime.GOOS == "windows" {
		got = strings.TrimPrefix(got, "/")
	}
	if got != filepath.ToSlash(want) {
		t.Errorf("DSN path = %q, want %q", got, filepath.ToSlash(want))
	}
	if u.Query().Get("_foreign_keys") != "on" {
		t.Errorf("foreign key parameter = %q, want on", u.Query().Get("_foreign_keys"))
	}
}

func TestOpenRoundTripsReservedPath(t *testing.T) {
	name := "runs#%.db"
	if runtime.GOOS != "windows" {
		name = "runs?#%.db"
	}
	p := filepath.Join(t.TempDir(), name)
	s, err := Open(p)
	if err != nil {
		t.Fatalf("open reserved path: %v", err)
	}
	id, err := s.Save(sampleRun("reserved"), sampleActions())
	if err != nil {
		s.Close()
		t.Fatalf("save reserved path: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close reserved path: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("stat reserved path: %v", err)
	}

	s, err = Open(p)
	if err != nil {
		t.Fatalf("reopen reserved path: %v", err)
	}
	defer s.Close()
	_, got, err := s.Load(id)
	if err != nil {
		t.Fatalf("load reserved path: %v", err)
	}
	if want := sampleActions(); !reflect.DeepEqual(got, want) {
		t.Errorf("actions = %#v, want %#v", got, want)
	}
	var foreignKeys int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "runs.db")
	s1, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatalf("reopening an existing database failed: %v", err)
	}
	s2.Close()
}

func TestOpenMigratesLegacyActions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			started_at INTEGER NOT NULL,
			command TEXT NOT NULL,
			go_version TEXT NOT NULL,
			goos TEXT NOT NULL,
			goarch TEXT NOT NULL,
			cores INTEGER NOT NULL,
			wall_ns INTEGER NOT NULL,
			work_ns INTEGER NOT NULL,
			ran INTEGER NOT NULL,
			cached INTEGER NOT NULL,
			exit_code INTEGER NOT NULL
		);
		CREATE TABLE actions (
			run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			idx INTEGER NOT NULL,
			mode TEXT NOT NULL,
			kind INTEGER NOT NULL,
			package TEXT NOT NULL,
			action_id TEXT NOT NULL,
			build_id TEXT NOT NULL,
			work_ns INTEGER NOT NULL,
			wall_ns INTEGER NOT NULL,
			queue_ns INTEGER NOT NULL,
			cached INTEGER NOT NULL,
			ran INTEGER NOT NULL,
			PRIMARY KEY (run_id, idx)
		);
		INSERT INTO runs VALUES
			(1, 'legacy', 123, 'go build', 'go1.26', 'linux', 'amd64', 4,
			 1000, 900, 1, 0, 0);
		INSERT INTO actions VALUES
			(1, 17, 'build', 1, 'example.com/legacy', 'OLD', 'old-build',
			 900, 1000, 100, 0, 1);
	`)
	if err != nil {
		db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	s, err := Open(p)
	if err != nil {
		t.Fatalf("open for migration: %v", err)
	}
	_, legacy, err := s.Load(1)
	if err != nil {
		s.Close()
		t.Fatalf("load migrated action: %v", err)
	}
	wantLegacy := []model.Action{{
		ID: 17, Mode: "build", Kind: model.KindCompile, Package: "example.com/legacy",
		ActionID: "OLD", BuildID: "old-build", WorkNs: 900, WallNs: 1000,
		QueueNs: 100, Ran: true,
	}}
	if !reflect.DeepEqual(legacy, wantLegacy) {
		t.Errorf("legacy actions = %#v, want %#v", legacy, wantLegacy)
	}

	newActions := []model.Action{{
		ID: 99, Mode: "link", Kind: model.KindLink, Package: "example.com/new",
		Deps: []int{17}, ActionID: "NEW", BuildID: "new-build", Cached: true,
	}}
	id, err := s.Save(sampleRun("legacy"), newActions)
	if err != nil {
		s.Close()
		t.Fatalf("save after migration: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	s, err = Open(p)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer s.Close()
	_, got, err := s.Load(id)
	if err != nil {
		t.Fatalf("load after reopening migrated database: %v", err)
	}
	if !reflect.DeepEqual(got, newActions) {
		t.Errorf("new actions = %#v, want %#v", got, newActions)
	}
}
