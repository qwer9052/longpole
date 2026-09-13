package main

import (
	"context"
	"errors"
	"fmt"
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

func TestRunExplainRequiresGoCommand(t *testing.T) {
	if got := run(context.Background(), []string{"--explain", "build", "./..."}); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
}

func TestUsageDescribesSameCommandDefaultDiff(t *testing.T) {
	if !strings.Contains(usage, "previous run of the same command") {
		t.Errorf("diff usage should describe same-command matching; got:\n%s", usage)
	}
}

func TestRunWrapExplainSuppressesOnlyHashLines(t *testing.T) {
	if os.Getenv("LONGPOLE_EXPLAIN_TEST_CHILD") == "1" {
		fmt.Fprintln(os.Stderr, "HASH[build example.com/project]")
		fmt.Fprintln(os.Stderr, "HASH regular diagnostic")
		os.Exit(7)
	}

	t.Setenv("LONGPOLE_EXPLAIN_TEST_CHILD", "1")
	readStderr, writeStderr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = writeStderr
	defer func() { os.Stderr = originalStderr }()

	got := runWrapExplain(context.Background(), []string{os.Args[0], "build"})
	if err := writeStderr.Close(); err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(readStderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := readStderr.Close(); err != nil {
		t.Fatal(err)
	}

	if got != 7 {
		t.Errorf("exit code = %d, want child exit code 7", got)
	}
	if strings.Contains(string(stderr), "HASH[build") {
		t.Errorf("strict HASH line reached the user: %q", stderr)
	}
	if !strings.Contains(string(stderr), "HASH regular diagnostic") {
		t.Errorf("non-HASH diagnostic was swallowed: %q", stderr)
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

func TestRunWrapResolvesRelativeGraphPathFromChangeDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/relative-graph\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package hello\n"), 0o600); err != nil {
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
		goPath, "-C", dir, "build", "-debug-actiongraph=graph.json", ".",
	})
	if err := writeStderr.Close(); err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(readStderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := readStderr.Close(); err != nil {
		t.Fatal(err)
	}

	if got != 0 {
		t.Errorf("exit code = %d, want 0; stderr = %q", got, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "graph.json")); err != nil {
		t.Fatalf("relative graph was not written under -C directory: %v", err)
	}
	if strings.Contains(string(stderr), "skipping analysis") || !strings.Contains(string(stderr), "build:") {
		t.Errorf("relative graph was not analyzed: %q", stderr)
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
		{
			name: "global prune",
			db:   &historyErrorStore{globalPruneErr: errors.New("global prune unavailable")},
			want: "global prune unavailable",
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

func TestRunDiffDefaultsToPreviousRunOfSameCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeID, err := db.Save(store.Run{Scope: "target", Command: "go build ./..."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Save(store.Run{Scope: "target", Command: "go test ./..."}, nil); err != nil {
		t.Fatal(err)
	}
	afterID, err := db.Save(store.Run{Scope: "target", Command: "go build ./..."}, nil)
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
	want := "run " + strconv.FormatInt(beforeID, 10) + " -> run " + strconv.FormatInt(afterID, 10)
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("diff = %q, want matching-command runs %q", stdout.String(), want)
	}
}

func TestRunDiffNotesDifferentCommandsForExplicitIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeID, err := db.Save(store.Run{Scope: "target", Command: "go build ./..."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	afterID, err := db.Save(store.Run{Scope: "target", Command: "go test ./..."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	args := []string{strconv.FormatInt(beforeID, 10), strconv.FormatInt(afterID, 10)}
	if got := runDiffFrom(path, "target", args, &stdout, &stderr); got != 0 {
		t.Fatalf("exit code = %d, stderr = %q", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "note: comparing different commands") {
		t.Errorf("diff = %q, want different-command note", stdout.String())
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

func TestRunDiffExplicitIDsRequireCurrentScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	targetBefore, err := db.Save(store.Run{Scope: "target"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	targetAfter, err := db.Save(store.Run{Scope: "target"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := db.Save(store.Run{Scope: "other"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		beforeID int64
		afterID  int64
		wantExit int
	}{
		{name: "both runs in current scope", beforeID: targetBefore, afterID: targetAfter, wantExit: 0},
		{name: "foreign runs", beforeID: foreign, afterID: foreign, wantExit: 1},
		{name: "mixed runs", beforeID: targetBefore, afterID: foreign, wantExit: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			args := []string{strconv.FormatInt(tt.beforeID, 10), strconv.FormatInt(tt.afterID, 10)}
			if got := runDiffFrom(path, "target", args, &stdout, &stderr); got != tt.wantExit {
				t.Fatalf("exit code = %d, want %d; stderr = %q", got, tt.wantExit, stderr.String())
			}
			if tt.wantExit != 0 && !strings.Contains(stderr.String(), "different scope") {
				t.Errorf("stderr = %q, want scope error", stderr.String())
			}
		})
	}
}

type historyErrorStore struct {
	previousErr    error
	pruneErr       error
	globalPruneErr error
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

func (s *historyErrorStore) PruneGlobal(int) error {
	return s.globalPruneErr
}
