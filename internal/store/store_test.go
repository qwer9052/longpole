package store

import (
	"path/filepath"
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
		{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "example.com/a",
			ActionID: "AAAA", WorkNs: 2_000_000_000, WallNs: 2_100_000_000, Ran: true},
		{ID: 1, Mode: "build", Kind: model.KindCompile, Package: "example.com/b",
			ActionID: "BBBB", Cached: true},
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
	if got.Ran != 195 || got.Cached != 197 {
		t.Errorf("counts round-tripped wrong: ran=%d cached=%d", got.Ran, got.Cached)
	}
	if len(acts) != 2 {
		t.Fatalf("got %d actions, want 2", len(acts))
	}
	if acts[0].ActionID != "AAAA" {
		t.Errorf("ActionID = %q, want AAAA", acts[0].ActionID)
	}
	if !acts[1].Cached {
		t.Error("cached flag did not round-trip")
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

func TestPruneRemovesOrphanedActions(t *testing.T) {
	s := openTemp(t)
	for i := 0; i < 3; i++ {
		s.Save(sampleRun("p"), sampleActions())
	}
	s.Prune("p", 1)
	n, err := s.countActions()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("got %d action rows after prune, want 2", n)
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
