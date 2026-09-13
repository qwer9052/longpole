package cause

import (
	"testing"

	"github.com/qwer9052/longpole/internal/hashlog"
	"github.com/qwer9052/longpole/internal/model"
)

func block(name string, inputs ...string) hashlog.Block {
	return hashlog.Block{Name: name, Inputs: inputs}
}

func TestDiffFindsChangedFile(t *testing.T) {
	before := map[string]hashlog.Block{
		"build a": block("build a", "go1.27.1", "file a.go AAAA", "import fmt FFFF"),
	}
	after := map[string]hashlog.Block{
		"build a": block("build a", "go1.27.1", "file a.go BBBB", "import fmt FFFF"),
	}

	changes := DiffBlocks(before, after)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Name != "build a" {
		t.Errorf("name = %q, want build a", changes[0].Name)
	}
	if len(changes[0].Changed) != 1 {
		t.Fatalf("got %d changed inputs, want 1", len(changes[0].Changed))
	}
	if got := changes[0].Changed[0].Label; got != "file a.go" {
		t.Errorf("label = %q, want file a.go", got)
	}
}

func TestDiffFindsChangedDependency(t *testing.T) {
	before := map[string]hashlog.Block{
		"build b": block("build b", "import a AAAA"),
	}
	after := map[string]hashlog.Block{
		"build b": block("build b", "import a BBBB"),
	}

	changes := DiffBlocks(before, after)
	if len(changes) != 1 || len(changes[0].Changed) != 1 {
		t.Fatalf("expected one changed dependency, got %+v", changes)
	}
	if got := changes[0].Changed[0].Label; got != "import a" {
		t.Errorf("label = %q, want import a", got)
	}
	if !changes[0].Changed[0].IsImport {
		t.Error("an import change should be flagged as such")
	}
}

func TestDiffFindsChangedConfiguration(t *testing.T) {
	before := map[string]hashlog.Block{"build a": block("build a", "GOAMD64=v1")}
	after := map[string]hashlog.Block{"build a": block("build a", "GOAMD64=v3")}

	changes := DiffBlocks(before, after)
	if len(changes) != 1 || len(changes[0].Changed) != 1 || changes[0].Changed[0].Label != "GOAMD64=v1" {
		t.Fatalf("expected the configuration line, got %+v", changes)
	}
}

func TestDiffIgnoresUnchangedAndNewBlocks(t *testing.T) {
	before := map[string]hashlog.Block{"build a": block("build a", "file a.go AAAA")}
	after := map[string]hashlog.Block{
		"build a":   block("build a", "file a.go AAAA"),
		"build new": block("build new", "file n.go AAAA"),
	}

	if got := DiffBlocks(before, after); len(got) != 0 {
		t.Errorf("got %d changes, want 0", len(got))
	}
}

func TestRootsFindsCascadeStartAndCountsRebuiltDependents(t *testing.T) {
	acts := []model.Action{
		{Package: "app", Kind: model.KindCompile, Ran: true, Deps: []int{1}},
		{Package: "config", Kind: model.KindCompile, Ran: true},
	}

	roots := Roots(acts, nil)
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1: %+v", len(roots), roots)
	}
	if roots[0].Package != "config" {
		t.Errorf("root = %q, want config", roots[0].Package)
	}
	if roots[0].Downstream != 1 {
		t.Errorf("downstream = %d, want 1", roots[0].Downstream)
	}
}

func TestRootsIgnoreCachedAndNonWorkActions(t *testing.T) {
	acts := []model.Action{
		{Package: "cached", Kind: model.KindCompile, Cached: true},
		{Package: "probe", Kind: model.KindCacheProbe, Ran: true},
	}

	if got := Roots(acts, nil); len(got) != 0 {
		t.Errorf("a build with no rebuilt work has no roots; got %+v", got)
	}
}

func TestRootsHandlesMissingHashDataAndInvalidDependencies(t *testing.T) {
	acts := []model.Action{
		{Package: "app", Kind: model.KindCompile, Ran: true, Deps: []int{-1, 9}},
	}

	roots := Roots(acts, nil)
	if len(roots) != 1 || roots[0].Package != "app" {
		t.Errorf("missing hash data and invalid dependencies must not hide a root; got %+v", roots)
	}
}
