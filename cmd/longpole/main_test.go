package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
