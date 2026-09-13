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

func TestDiffMarksCurrentImportAfterPositionalTypeChange(t *testing.T) {
	before := map[string]hashlog.Block{
		"build b": block("build b", "file b.go AAAA"),
	}
	after := map[string]hashlog.Block{
		"build b": block("build b", "import config BBBB"),
	}

	changes := DiffBlocks(before, after)
	if len(changes) != 1 || len(changes[0].Changed) != 1 {
		t.Fatalf("expected one positional change, got %+v", changes)
	}
	if !changes[0].Changed[0].IsImport {
		t.Errorf("current import must be marked as an import: %+v", changes[0].Changed[0])
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

func TestRootsGroupsHashLinkedCandidates(t *testing.T) {
	const appDigest = "0011223344556677889900112233445566778899001122334455667788990011"
	appID := actionID(t, appDigest)
	configID := actionID(t, "2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803")
	const configOutput = "config-output"
	acts := []model.Action{
		{Package: "app", Kind: model.KindCompile, Ran: true, ActionID: appID, BuildID: appID + "/app-output", Deps: []int{1}},
		{Package: "config", Kind: model.KindCompile, Ran: true, ActionID: configID, BuildID: configID + "/" + configOutput},
	}
	blocks := map[string]hashlog.Block{
		"build app":    {Name: "build app", Inputs: []string{"import config " + configOutput}, Digest: appDigest},
		"build config": {Name: "build config", Digest: "2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803"},
	}

	roots := Roots(acts, blocks)
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

func TestRootsRequireDependentBlockIdentity(t *testing.T) {
	const appDigest = "0011223344556677889900112233445566778899001122334455667788990011"
	const configDigest = "2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803"
	appID := actionID(t, appDigest)
	configID := actionID(t, configDigest)
	acts := []model.Action{
		{Package: "app", Kind: model.KindCompile, Ran: true, ActionID: appID, BuildID: appID + "/app-output", Deps: []int{1}},
		{Package: "config", Kind: model.KindCompile, Ran: true, ActionID: configID, BuildID: configID + "/config-output"},
	}

	for _, tt := range []struct {
		name   string
		digest string
	}{
		{name: "missing digest"},
		{name: "mismatched digest", digest: configDigest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			blocks := map[string]hashlog.Block{
				"build app":    {Name: "build app", Inputs: []string{"import config config-output"}, Digest: tt.digest},
				"build config": {Name: "build config", Digest: configDigest},
			}

			roots := Roots(acts, blocks)
			if len(roots) != 2 || roots[0].Package != "app" || roots[1].Package != "config" {
				t.Errorf("unverified dependent block must not hide a root: %+v", roots)
			}
		})
	}
}

func TestRootsKeepIndependentActionsAsSeparateCandidates(t *testing.T) {
	configID := actionID(t, "2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803")
	acts := []model.Action{
		{Package: "app", Kind: model.KindCompile, Ran: true, Deps: []int{1}},
		{Package: "config", Kind: model.KindCompile, Ran: true, ActionID: configID, BuildID: configID + "/config-output"},
	}
	blocks := map[string]hashlog.Block{
		"build app":    block("build app", "import config different-output"),
		"build config": {Name: "build config", Digest: "2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803"},
	}

	roots := Roots(acts, blocks)
	if len(roots) != 2 {
		t.Fatalf("unmatched import must not join independent candidates; got %+v", roots)
	}
	if roots[0].Package != "app" || roots[1].Package != "config" {
		t.Errorf("roots = %+v, want app and config", roots)
	}
	if roots[0].Downstream != 0 || roots[1].Downstream != 0 {
		t.Errorf("independent candidates have no joined downstream actions: %+v", roots)
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

func TestRootsKeepIndependentActionsAsSeparateCandidatesWithoutBaseline(t *testing.T) {
	// Roots receives one run only, so these actions have no cross-run baseline.
	acts := []model.Action{
		{Package: "app", Kind: model.KindCompile, Ran: true, Deps: []int{1, -1, 9}},
		{Package: "config", Kind: model.KindCompile, Ran: true},
	}

	roots := Roots(acts, nil)
	if len(roots) != 2 || roots[0].Package != "app" || roots[1].Package != "config" {
		t.Errorf("missing hash data must leave separate candidates; got %+v", roots)
	}
}

func actionID(t *testing.T, digest string) string {
	t.Helper()
	id, err := hashlog.ActionID(digest)
	if err != nil {
		t.Fatalf("ActionID(%q): %v", digest, err)
	}
	return id
}
