package hashlog

import (
	"strings"
	"testing"
)

const sample = `HASH[build example.com/lab/a]
HASH[build example.com/lab/a]: "go1.27.1"
HASH[build example.com/lab/a]: "compile\n"
HASH[build example.com/lab/a]: "dir C:\\lab\\a\n"
HASH[build example.com/lab/a]: "file a.go 9SqAMQcEh9zzRXLLCibe\n"
HASH[build example.com/lab/a]: "import fmt 6yHny8j1aq7yvW1BoyvM\n"
HASH[build example.com/lab/a]: 2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803
HASH[build example.com/lab/b]: "go1.27.1"
HASH[build example.com/lab/b]: 0011223344556677889900112233445566778899001122334455667788990011
`

func TestCollectorCollectsBlocksByName(t *testing.T) {
	c := New()
	for _, line := range strings.SplitAfter(sample, "\n") {
		c.Line([]byte(line))
	}

	blocks := c.Blocks()
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	a, ok := blocks["build example.com/lab/a"]
	if !ok {
		t.Fatal("block for package a missing")
	}
	if len(a.Inputs) != 5 {
		t.Errorf("got %d inputs, want 5: %v", len(a.Inputs), a.Inputs)
	}
	if a.Inputs[0] != "go1.27.1" {
		t.Errorf("first input = %q", a.Inputs[0])
	}
}

func TestCollectorUnquotesEscapes(t *testing.T) {
	c := New()
	c.Line([]byte(`HASH[x]: "dir C:\\lab\\a\n"` + "\n"))
	got := c.Blocks()["x"].Inputs[0]
	if got != `dir C:\lab\a`+"\n" {
		t.Errorf("got %q, want an unquoted path with a real newline", got)
	}
}

func TestCollectorCapturesFinalDigest(t *testing.T) {
	c := New()
	for _, line := range strings.SplitAfter(sample, "\n") {
		c.Line([]byte(line))
	}

	want := "2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803"
	if got := c.Blocks()["build example.com/lab/a"].Digest; got != want {
		t.Errorf("digest = %q, want %q", got, want)
	}
}

// The digest is the full SHA-256; the action graph's ActionID is the first 15
// bytes in base64url. Joining the two lets a hash diff be attributed to a
// package and its cost.
func TestActionIDFromDigest(t *testing.T) {
	got, err := ActionID("2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803")
	if err != nil {
		t.Fatalf("ActionID: %v", err)
	}
	if got != "LJaA78fjRHyrIOReFVuZ" {
		t.Errorf("ActionID = %q, want LJaA78fjRHyrIOReFVuZ", got)
	}
}

func TestActionIDRejectsInvalidDigest(t *testing.T) {
	for _, digest := range []string{"abcd", strings.Repeat("x", 64)} {
		if _, err := ActionID(digest); err == nil {
			t.Errorf("ActionID(%q) succeeded, want an error", digest)
		}
	}
}

func TestIsHashLine(t *testing.T) {
	yes := []string{
		`HASH[build x]: "a"`,
		`HASH[build x]`,
		`HASH subkey abc stdout = def`,
		`HASH C:\lab\a.go: 2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803`,
	}
	for _, line := range yes {
		if !IsHashLine([]byte(line)) {
			t.Errorf("should be recognised as hash output: %q", line)
		}
	}

	no := []string{
		"# example.com/lab/bad",
		"bad.go:2:16: declared and not used: x",
		"HASHER is not a hash line",
		"HASH file.go: not-a-digest",
		"",
		"HASH[not closed",
		"HASH[build x] user output",
		"HASH subkey ",
	}
	for _, line := range no {
		if IsHashLine([]byte(line)) {
			t.Errorf("must pass through to the user: %q", line)
		}
	}
}

func TestRootsOnlyCollectorRetainsDependencyEvidence(t *testing.T) {
	c := NewRootsOnly()
	c.Line([]byte("HASH[build x]\n"))
	c.Line([]byte(`HASH[build x]: "file x.go abc"` + "\n"))
	c.Line([]byte(`HASH[build x]: "import y def"` + "\n"))
	c.Line([]byte(`HASH[build x]: "packagefile z=ghi"` + "\n"))
	block := c.Blocks()["build x"]
	if len(block.Inputs) != 2 || block.Inputs[0] != "import y def" || block.Inputs[1] != "packagefile z=ghi" {
		t.Fatalf("retained inputs = %v", block.Inputs)
	}
}

func TestCollectorIgnoresNonHashLines(t *testing.T) {
	c := New()
	c.Line([]byte("some compiler error\n"))
	if len(c.Blocks()) != 0 {
		t.Error("non-hash lines must not create blocks")
	}
}

func TestCollectorOmitsSequentialRepeatedName(t *testing.T) {
	c := New()
	for _, line := range []string{
		"HASH[build example.com/reused]\n",
		`HASH[build example.com/reused]: "first\n"` + "\n",
		"HASH[build example.com/reused]: 0011223344556677889900112233445566778899001122334455667788990011\n",
		"HASH[build example.com/reused]\n",
		`HASH[build example.com/reused]: "second\n"` + "\n",
		"HASH[build example.com/reused]: 1122334455667788990011223344556677889900112233445566778899001122\n",
	} {
		c.Line([]byte(line))
	}

	if _, ok := c.Blocks()["build example.com/reused"]; ok {
		t.Error("repeated hash names must be omitted instead of merging two instances")
	}
}

func TestCollectorOmitsOverlappingRepeatedName(t *testing.T) {
	c := New()
	for _, line := range []string{
		"HASH[build example.com/reused]\n",
		`HASH[build example.com/reused]: "first\n"` + "\n",
		"HASH[build example.com/other]\n",
		"HASH[build example.com/reused]\n",
		`HASH[build example.com/reused]: "second\n"` + "\n",
		"HASH[build example.com/other]: 0011223344556677889900112233445566778899001122334455667788990011\n",
	} {
		c.Line([]byte(line))
	}

	blocks := c.Blocks()
	if _, ok := blocks["build example.com/reused"]; ok {
		t.Error("overlapping repeated hash names must be omitted instead of mixing inputs")
	}
	if _, ok := blocks["build example.com/other"]; !ok {
		t.Error("an unrelated block must remain available")
	}
}

func BenchmarkCollectorLargeStream(b *testing.B) {
	line := []byte(`HASH[build example.com/pkg]: "file x.go 9SqAMQcEh9zzRXLLCibe\n"` + "\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c := New()
		for j := 0; j < 10_000; j++ {
			c.Line(line)
		}
	}
}
