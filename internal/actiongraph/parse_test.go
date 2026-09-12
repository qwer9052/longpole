package actiongraph

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, name string) []Action {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	acts, err := Parse(f)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return acts
}

func TestParseCold(t *testing.T) {
	acts := load(t, "cold.json")
	if len(acts) != 392 {
		t.Fatalf("got %d actions, want 392", len(acts))
	}
	var ran int
	for _, a := range acts {
		if a.CmdReal > 0 {
			ran++
		}
	}
	if ran != 195 {
		t.Errorf("actions with CmdReal = %d, want 195", ran)
	}
}

func TestParseWarmHasAlmostNoWork(t *testing.T) {
	acts := load(t, "warm.json")
	var ran int
	for _, a := range acts {
		if a.CmdReal > 0 {
			ran++
		}
	}
	if ran != 1 {
		t.Errorf("warm build ran %d actions, want 1", ran)
	}
}

// Go 1.26 split the cache probe into its own action. Both shapes must parse.
func TestParseHandlesCacheCheckMode(t *testing.T) {
	acts := load(t, "cold.json")
	var checks int
	for _, a := range acts {
		if a.Mode == "build check cache" {
			checks++
		}
	}
	if checks == 0 {
		t.Fatal("no 'build check cache' actions found; fixture or parser is wrong")
	}
	for _, a := range acts {
		if a.Mode == "build check cache" && a.ActionID != "" {
			t.Errorf("cache-check action %d unexpectedly has an ActionID", a.ID)
		}
	}
}

func TestParseFailedBuild(t *testing.T) {
	acts := load(t, "failed.json")
	if len(acts) == 0 {
		t.Fatal("failed-build graph is empty")
	}
	// The failing action ran (has a Cmd) but produced no output, so no BuildID.
	var found bool
	for _, a := range acts {
		if a.Mode == "build" && len(a.Cmd) > 0 && a.BuildID == "" {
			found = true
		}
	}
	if !found {
		t.Error("expected a ran-but-no-BuildID action in a failed build")
	}
}

func TestParseKeepsDependencyEdges(t *testing.T) {
	acts := load(t, "cold.json")
	var withDeps int
	for _, a := range acts {
		if len(a.Deps) > 0 {
			withDeps++
		}
	}
	if withDeps == 0 {
		t.Fatal("no dependency edges parsed")
	}
	// Deps are indexes into this same slice; every one must be in range.
	for _, a := range acts {
		for _, d := range a.Deps {
			if d < 0 || d >= len(acts) {
				t.Fatalf("action %d has out-of-range dep %d", a.ID, d)
			}
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse(stringReader("not json")); err == nil {
		t.Error("expected an error parsing garbage")
	}
}

func TestParseEmptyArray(t *testing.T) {
	acts, err := Parse(stringReader("[]"))
	if err != nil {
		t.Fatalf("empty array should parse: %v", err)
	}
	if len(acts) != 0 {
		t.Errorf("got %d actions, want 0", len(acts))
	}
}

func TestParseRejectsTrailingValues(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "trailing garbage",
			input:   "[] garbage",
			wantErr: true,
		},
		{
			name:    "second JSON value",
			input:   "[] []",
			wantErr: true,
		},
		{
			name:  "trailing whitespace",
			input: "[] \n\t ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(stringReader(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %t", tt.input, err, tt.wantErr)
			}
		})
	}
}

type sr struct {
	s string
	i int
}

func stringReader(s string) *sr { return &sr{s: s} }

func (r *sr) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
