package wrap

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestInjectAfterSubcommand(t *testing.T) {
	got, path := Inject([]string{"go", "build", "./..."}, "/tmp/ag.json")
	want := []string{"go", "build", "-debug-actiongraph=/tmp/ag.json", "./..."}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
	if path != "/tmp/ag.json" {
		t.Errorf("path = %q, want the injected path", path)
	}
}

func TestInjectIntoGoTest(t *testing.T) {
	got, _ := Inject([]string{"go", "test", "-race", "./..."}, "/tmp/ag.json")
	if got[2] != "-debug-actiongraph=/tmp/ag.json" {
		t.Errorf("flag not placed right after the subcommand: %v", got)
	}
}

// If the user already asked for a graph, use theirs rather than fighting over
// the flag. The go command would reject two of them anyway.
func TestRespectsExistingFlag(t *testing.T) {
	got, path := Inject(
		[]string{"go", "build", "-debug-actiongraph=mine.json", "./..."},
		"/tmp/ours.json")
	if path != "mine.json" {
		t.Errorf("path = %q, want mine.json", path)
	}
	if len(got) != 4 {
		t.Errorf("arguments were modified: %v", got)
	}
}

func TestRespectsExistingFlagSpaceForm(t *testing.T) {
	_, path := Inject(
		[]string{"go", "build", "-debug-actiongraph", "mine.json", "./..."},
		"/tmp/ours.json")
	if path != "mine.json" {
		t.Errorf("path = %q, want mine.json", path)
	}
}

func TestUnsupportedSubcommandIsReported(t *testing.T) {
	if _, err := Check([]string{"go", "mod", "tidy"}); err == nil {
		t.Error("expected an error for a subcommand that builds nothing")
	}
}

func TestSupportedSubcommands(t *testing.T) {
	for _, sub := range []string{"build", "test", "install", "vet"} {
		if _, err := Check([]string{"go", sub, "./..."}); err != nil {
			t.Errorf("go %s should be supported: %v", sub, err)
		}
	}
}

func TestCheckRejectsTooFewArgs(t *testing.T) {
	if _, err := Check([]string{"go"}); err == nil {
		t.Error("expected an error for a bare 'go'")
	}
}

func TestRunWrapsWaitErrors(t *testing.T) {
	if os.Getenv("LONGPOLE_WRAP_TEST_STDERR") == "1" {
		fmt.Fprintln(os.Stderr, "child stderr")
		return
	}

	tee := NewStderrTee(errorWriter{}, nil)
	_, err := Run(
		[]string{os.Args[0], "-test.run=^TestRunWrapsWaitErrors$"},
		[]string{"LONGPOLE_WRAP_TEST_STDERR=1"},
		tee,
	)
	if err == nil || !strings.Contains(err.Error(), "wait ") {
		t.Fatalf("error = %v, want wait operation context", err)
	}
}

func TestStderrTeeForwardsEverythingByDefault(t *testing.T) {
	var out strings.Builder
	tee := NewStderrTee(&out, nil)
	tee.Write([]byte("one\ntwo\n"))
	tee.Flush()
	if out.String() != "one\ntwo\n" {
		t.Errorf("got %q, want %q", out.String(), "one\ntwo\n")
	}
}

func TestStderrTeeSwallowsSelectedLines(t *testing.T) {
	var out strings.Builder
	tee := NewStderrTee(&out, func(l []byte) bool {
		return strings.HasPrefix(string(l), "HASH")
	})
	tee.Write([]byte("keep me\nHASH[x]: \"a\"\nkeep me too\n"))
	tee.Flush()
	if out.String() != "keep me\nkeep me too\n" {
		t.Errorf("got %q", out.String())
	}
}

func TestStderrTeeHandlesSplitWrites(t *testing.T) {
	var out strings.Builder
	tee := NewStderrTee(&out, nil)
	tee.Write([]byte("par"))
	tee.Write([]byte("tial\n"))
	tee.Flush()
	if out.String() != "partial\n" {
		t.Errorf("got %q, want %q", out.String(), "partial\n")
	}
}

func TestStderrTeeFlushesTrailingPartialLine(t *testing.T) {
	var out strings.Builder
	tee := NewStderrTee(&out, nil)
	tee.Write([]byte("no newline"))
	tee.Flush()
	if out.String() != "no newline" {
		t.Errorf("got %q, want %q", out.String(), "no newline")
	}
}
