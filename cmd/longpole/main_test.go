package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/qwer9052/longpole/internal/model"
	"github.com/qwer9052/longpole/internal/store"
	"github.com/qwer9052/longpole/internal/wrap"
)

func TestRunWrapSetupFailureStillRunsChild(t *testing.T) {
	if os.Getenv("LONGPOLE_MAIN_TEST_CHILD") == "1" {
		os.Exit(7)
	}

	missingTemp := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missingTemp)
	t.Setenv("TMP", missingTemp)
	t.Setenv("TEMP", missingTemp)
	t.Setenv("LONGPOLE_MAIN_TEST_CHILD", "1")

	readWarning, writeWarning, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = writeWarning
	defer func() { os.Stderr = originalStderr }()

	got := runWrap(context.Background(), []string{os.Args[0], "build"})
	writeWarning.Close()
	warning, err := io.ReadAll(readWarning)
	if err != nil {
		t.Fatal(err)
	}
	readWarning.Close()

	if got != 7 {
		t.Errorf("exit code = %d, want child exit code 7", got)
	}
	if lines := strings.Count(strings.TrimSpace(string(warning)), "\n") + 1; lines != 1 {
		t.Errorf("warning lines = %d, want at most one; stderr = %q", lines, warning)
	}
	if !strings.Contains(string(warning), "create action graph temporary directory") {
		t.Errorf("stderr = %q, want setup warning", warning)
	}
}

func TestRunWrapExistingGraphFlagDoesNotNeedTempDirectory(t *testing.T) {
	graphPath := filepath.Join(t.TempDir(), "actiongraph.json")
	missingTemp := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missingTemp)
	t.Setenv("TMP", missingTemp)
	t.Setenv("TEMP", missingTemp)

	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	goPath := filepath.Join(runtime.GOROOT(), "bin", goName)
	got := runWrap(context.Background(), []string{
		goPath,
		"build",
		"-debug-actiongraph=" + graphPath,
		"./does-not-exist",
	})
	if got != 1 {
		t.Errorf("exit code = %d, want child exit code 1", got)
	}
}

func TestRunWrapSkipsStaleExistingGraph(t *testing.T) {
	graphPath := filepath.Join(t.TempDir(), "actiongraph.json")
	if err := os.WriteFile(graphPath, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}

	readStderr, writeStderr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = writeStderr
	defer func() { os.Stderr = originalStderr }()

	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	goPath := filepath.Join(runtime.GOROOT(), "bin", goName)
	got := runWrap(context.Background(), []string{
		goPath,
		"build",
		"-debug-actiongraph=" + graphPath,
		"./does-not-exist",
	})
	writeStderr.Close()
	stderr, err := io.ReadAll(readStderr)
	if err != nil {
		t.Fatal(err)
	}
	readStderr.Close()

	if got != 1 {
		t.Errorf("exit code = %d, want child exit code 1", got)
	}
	if !strings.Contains(string(stderr), "was not updated; skipping analysis") {
		t.Errorf("stderr = %q, want stale graph warning", stderr)
	}
	if strings.Contains(string(stderr), "no build actions recorded") {
		t.Errorf("stale graph was analyzed: %q", stderr)
	}
	graph, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(graph) != "[]" {
		t.Errorf("user graph was modified: %q", graph)
	}
}

func TestFinishRunPersistenceFailureKeepsExitAndReport(t *testing.T) {
	graphPath := filepath.Join("..", "..", "internal", "actiongraph", "testdata", "cold.json")
	var stderr strings.Builder

	got := finishRun(
		context.Background(),
		graphPath,
		[]string{"go", "build", "./..."},
		wrap.Result{ExitCode: 7, WallNs: 100_000_000},
		func(context.Context, []string, wrap.Result, model.Summary, []model.Action) (int64, int64, error) {
			return 0, 0, errors.New("database unavailable")
		},
		&stderr,
	)

	if got != 7 {
		t.Errorf("exit code = %d, want child exit code 7", got)
	}
	if !strings.Contains(stderr.String(), "could not record this run: database unavailable") {
		t.Errorf("stderr = %q, want persistence warning", stderr.String())
	}
	if !strings.Contains(stderr.String(), "build failed (exit 7)") {
		t.Errorf("stderr = %q, want failed-build report", stderr.String())
	}
}

func TestFinishRunPassesContextToPersister(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "scope context")
	graphPath := filepath.Join("..", "..", "internal", "actiongraph", "testdata", "cold.json")
	var gotValue any

	finishRun(
		ctx,
		graphPath,
		[]string{"go", "build", "./..."},
		wrap.Result{},
		func(ctx context.Context, _ []string, _ wrap.Result, _ model.Summary, _ []model.Action) (int64, int64, error) {
			gotValue = ctx.Value(contextKey{})
			return 1, 0, nil
		},
		io.Discard,
	)

	if gotValue != "scope context" {
		t.Errorf("context value = %v", gotValue)
	}
}

func TestSaveRunSurfacesHistoryErrors(t *testing.T) {
	tests := []struct {
		name string
		db   *historyErrorStore
		want string
	}{
		{
			name: "previous",
			db:   &historyErrorStore{previousErr: errors.New("previous unavailable")},
			want: "previous unavailable",
		},
		{
			name: "prune",
			db:   &historyErrorStore{pruneErr: errors.New("prune unavailable")},
			want: "prune unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := saveRun(tt.db, store.Run{Scope: "scope"}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunLogShowsScopedRunsNewestFirstAndMarksFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := db.Save(store.Run{
		Scope: "target", StartedAt: 1, Command: "go build ./old", WallNs: 1_000_000,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := db.Save(store.Run{
		Scope: "other", StartedAt: 2, Command: "go build ./other", WallNs: 2_000_000,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	newID, err := db.Save(store.Run{
		Scope: "target", StartedAt: 3, Command: "go test ./new", WallNs: 3_000_000, ExitCode: 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if got := runLogFrom(path, "target", &stdout, &stderr); got != 0 {
		t.Fatalf("exit code = %d, stderr = %q", got, stderr.String())
	}
	out := stdout.String()
	newPos := strings.Index(out, "#"+strconv.FormatInt(newID, 10))
	oldPos := strings.Index(out, "#"+strconv.FormatInt(oldID, 10))
	if newPos < 0 || oldPos < 0 || newPos >= oldPos {
		t.Errorf("log order = %q, want run #%d before #%d", out, newID, oldID)
	}
	if strings.Contains(out, "#"+strconv.FormatInt(otherID, 10)) || strings.Contains(out, "./other") {
		t.Errorf("log includes other scope: %q", out)
	}
	if !strings.Contains(out, "go test ./new  failed") {
		t.Errorf("log = %q, want failed label", out)
	}
}

func TestRunDiffComparesTwoMostRecentScopedRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeID, err := db.Save(store.Run{Scope: "target", StartedAt: 1, WallNs: 1_000_000_000}, []model.Action{
		{Package: "a", Kind: model.KindCompile, Cached: true, ActionID: "before"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Save(store.Run{Scope: "other", StartedAt: 2, WallNs: 9_000_000_000}, nil); err != nil {
		t.Fatal(err)
	}
	afterID, err := db.Save(store.Run{Scope: "target", StartedAt: 3, WallNs: 3_000_000_000}, []model.Action{
		{Package: "a", Kind: model.KindCompile, Ran: true, WorkNs: 2_000_000_000, ActionID: "after"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if got := runDiffFrom(path, "target", nil, &stdout, &stderr); got != 0 {
		t.Fatalf("exit code = %d, stderr = %q", got, stderr.String())
	}
	out := stdout.String()
	wantRuns := "run " + strconv.FormatInt(beforeID, 10) + " -> run " + strconv.FormatInt(afterID, 10)
	if !strings.Contains(out, wantRuns) || !strings.Contains(out, "a") {
		t.Errorf("diff = %q, want %q and package a", out, wantRuns)
	}
}

func TestRunDiffNeedsTwoRunsInScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Save(store.Run{Scope: "target"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if got := runDiffFrom(path, "target", nil, &stdout, &stderr); got != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "need two runs to compare, have 1") {
		t.Errorf("stderr = %q, want not-enough-runs message", stderr.String())
	}
}

type historyErrorStore struct {
	previousErr error
	pruneErr    error
}

func (s *historyErrorStore) Save(store.Run, []model.Action) (int64, error) {
	return 1, nil
}

func (s *historyErrorStore) Previous(string, int64) (int64, error) {
	return 0, s.previousErr
}

func (s *historyErrorStore) Prune(string, int) error {
	return s.pruneErr
}
