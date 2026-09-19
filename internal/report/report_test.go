package report

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qwer9052/longpole/internal/actiongraph"
	"github.com/qwer9052/longpole/internal/hashlog"
	"github.com/qwer9052/longpole/internal/model"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixture(t *testing.T, name string) []model.Action {
	t.Helper()
	p := filepath.Join("..", "actiongraph", "testdata", name)
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open %s: %v", p, err)
	}
	defer f.Close()
	raw, err := actiongraph.Parse(f)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return model.NewAll(raw)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden %s: %v (run: go test ./internal/report/ -update)", p, err)
	}
	if got != string(want) {
		t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestReportCold(t *testing.T) {
	acts := fixture(t, "cold.json")
	s := model.Summarize(acts, 12_400_000_000)
	golden(t, "cold.txt", Run(s, acts, Options{TopN: 3, PathN: 3, Cores: 8}))
}

func TestReportFullyCachedSaysNothingToOptimize(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Mode: "build check cache", Kind: model.KindCacheProbe, Package: "example.com/lab/cmd/app", WallNs: 350_000_000},
		{ID: 1, Mode: "build", Kind: model.KindCompile, Package: "example.com/lab/cmd/app", Cached: true},
		{ID: 2, Mode: "link", Kind: model.KindLink, Package: "example.com/lab/cmd/app", Cached: true},
	}
	s := model.Summarize(acts, 500_000_000)
	out := Run(s, acts, Options{TopN: 3, PathN: 3, Cores: 8})
	if !strings.Contains(out, "nothing to optimize") {
		t.Errorf("a fully cached build must say so; got:\n%s", out)
	}
	if strings.Contains(out, "critical path") {
		t.Errorf("a fully cached build must not show a critical path; got:\n%s", out)
	}
	if !strings.Contains(out, "across 1 action;") {
		t.Errorf("a single cache probe must use singular grammar; got:\n%s", out)
	}
	if !strings.Contains(out, "summed cache-probe spans") || !strings.Contains(out, "spans may overlap") {
		t.Errorf("cache probing must be labelled as summed, potentially overlapping spans; got:\n%s", out)
	}
	golden(t, "fully_cached.txt", out)
}

func TestReportFullyCachedOmitsEmptyProbeSummary(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Kind: model.KindCompile, Cached: true},
	}
	out := Run(model.Summarize(acts, 100_000_000), acts, Options{})
	if strings.Contains(out, "cache-probe spans") {
		t.Fatalf("empty probe groups should not render a probe summary: %s", out)
	}
}

func TestReportWarmNeverRanksCacheProbes(t *testing.T) {
	// The incumbent's bug: on a warm build it lists cache probes as the
	// slowest steps. The warm fixture still performs one real link, but its
	// cache probes must never be presented as costly package work.
	acts := fixture(t, "warm.json")
	s := model.Summarize(acts, 500_000_000)
	out := Run(s, acts, Options{TopN: 5, PathN: 5, Cores: 8})
	if strings.Contains(out, "build check cache") {
		t.Errorf("cache probes must never be presented as build steps; got:\n%s", out)
	}
	if strings.Contains(out, "internal/cpu") {
		t.Errorf("cache-probe packages must never be ranked as slow work; got:\n%s", out)
	}
	golden(t, "warm.txt", out)
}

func TestReportCriticalPathOmitsCachedActions(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Mode: "link", Kind: model.KindLink, Package: "cached-wrapper", Deps: []int{1}, Cached: true},
		{ID: 1, Mode: "build", Kind: model.KindCompile, Package: "ran-package", Ran: true, WorkNs: 100_000_000},
	}
	out := Run(model.Summarize(acts, 200_000_000), acts, Options{TopN: 2, PathN: 2, Cores: 8})
	if strings.Contains(out, "cached-wrapper") {
		t.Errorf("cached actions must not appear under a cost heading; got:\n%s", out)
	}
}

func TestReportSkipsSplitHintForShortCriticalPath(t *testing.T) {
	acts := []model.Action{{ID: 0, Kind: model.KindCompile, Package: "short", Ran: true, WorkNs: 200_000_000}}
	out := Run(model.Summarize(acts, 2_000_000_000), acts, Options{})
	if strings.Contains(out, "consider splitting") {
		t.Fatalf("short work should not trigger a split hint: %s", out)
	}
}

func TestReportSuggestsReducingRelinks(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Kind: model.KindLink, Package: "one", Ran: true, WorkNs: 2_000_000_000},
		{ID: 1, Kind: model.KindLink, Package: "two", Ran: true, WorkNs: 2_000_000_000},
	}
	out := Run(model.Summarize(acts, 5_000_000_000), acts, Options{})
	if !strings.Contains(out, "2 binaries were re-linked") {
		t.Fatalf("multiple relinks should be called out: %s", out)
	}
}

func TestReportLabelsActionGraphOutsideTimeAsEstimate(t *testing.T) {
	s := model.Summarize([]model.Action{{ID: 0, Kind: model.KindCompile, Ran: true, WorkNs: 100_000_000}}, 2_000_000_000)
	s.ActionSpanNs = 1_000_000_000
	out := Run(s, nil, Options{})
	if !strings.Contains(out, "outside the action graph") || strings.Contains(out, "before actions") {
		t.Fatalf("outside-action time wording should not imply only startup: %s", out)
	}
}

func TestReportCriticalPathShowsVetWork(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Kind: model.KindVet, Mode: "vet", Package: "pkg", WorkNs: 50_000_000, Deps: []int{1}},
		{ID: 1, Kind: model.KindCompile, Mode: "build", Package: "dep", Cached: true},
	}
	out := Run(model.Summarize(acts, 100_000_000), acts, Options{PathN: 2})
	if !strings.Contains(out, "pkg") {
		t.Fatalf("vet work on the critical path should be listed: %s", out)
	}
	if !strings.Contains(out, "(vet)") {
		t.Fatalf("vet critical-path entries should be labelled: %s", out)
	}
}

func TestReportKeepsVetActionCount(t *testing.T) {
	acts := []model.Action{{ID: 0, Kind: model.KindVet, Mode: "vet", Package: "pkg", WorkNs: 50_000_000}}
	out := Run(model.Summarize(acts, 100_000_000), acts, Options{PathN: 1})
	if !strings.Contains(out, "1 action") {
		t.Fatalf("vet row should retain its action count: %s", out)
	}
}

func TestReportLabelsRanActionsWithinKindTotals(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Kind: model.KindCompile, Mode: "build", Package: "ran", Ran: true, WorkNs: 10},
		{ID: 1, Kind: model.KindCompile, Mode: "build", Package: "cached", Cached: true},
	}
	out := Run(model.Summarize(acts, 100), acts, Options{TopN: 1, PathN: 1})
	if !strings.Contains(out, "1 of 2 actions") {
		t.Fatalf("kind totals should distinguish ran actions: %s", out)
	}
}

func TestReportLabelsCacheProbeTimeAsAggregateOverlappingSpans(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "ran-package", Ran: true, WorkNs: 100_000_000},
		{ID: 1, Mode: "build check cache", Kind: model.KindCacheProbe, Package: "cached-package", WallNs: 200_000_000},
	}
	out := Run(model.Summarize(acts, 300_000_000), acts, Options{TopN: 1, PathN: 1, Cores: 8})
	if !strings.Contains(out, "span sum") || !strings.Contains(out, "may overlap") {
		t.Errorf("cache probing must be labelled as summed, potentially overlapping spans; got:\n%s", out)
	}
}

// A regression would hide test-binary execution again, making a cached go test
// appear to account for almost none of its own wall time.
func TestReportShowsTestExecutionAndLabelsTotalWallDenominators(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "package", Ran: true, WorkNs: 100_000_000},
		{ID: 1, Mode: "test run", Kind: model.KindTest, Package: "package", WallNs: 4_000_000_000},
	}
	out := Run(model.Summarize(acts, 5_000_000_000), acts, Options{TopN: 1, PathN: 1, Cores: 8})
	if !strings.Contains(out, "test execution; may overlap") || !strings.Contains(out, "4.00s") {
		t.Errorf("test execution must be shown separately; got:\n%s", out)
	}
	if !strings.Contains(out, "build critical path") || !strings.Contains(out, "includes test execution") {
		t.Errorf("build-only metrics must label total-wall denominators; got:\n%s", out)
	}
}

func TestReportDoesNotCallTestExecutionFullyCached(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "package", Cached: true},
		{ID: 1, Mode: "test run", Kind: model.KindTest, Package: "package", WallNs: 4_000_000_000},
	}
	out := Run(model.Summarize(acts, 5_000_000_000), acts, Options{TopN: 1, PathN: 1, Cores: 8})
	if strings.Contains(out, "nothing to optimize") {
		t.Errorf("test execution is not a fully cached command; got:\n%s", out)
	}
}

func TestReportGotestFixtureShowsTestRunTime(t *testing.T) {
	acts := fixture(t, "gotest.json")
	out := Run(model.Summarize(acts, 1_000_000_000), acts, Options{TopN: 1, PathN: 1, Cores: 8})
	if !strings.Contains(out, "test execution; may overlap") {
		t.Errorf("the real go test fixture must show its test-run span; got:\n%s", out)
	}
}

func TestReportUsesSingularAction(t *testing.T) {
	acts := []model.Action{{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "one", Ran: true, WorkNs: 100_000_000, QueueNs: 60_000_000}}
	out := Run(model.Summarize(acts, 200_000_000), acts, Options{TopN: 1, PathN: 1, Cores: 8})
	if strings.Contains(out, "1 actions") {
		t.Errorf("a count of one must use singular grammar; got:\n%s", out)
	}
}

func TestReportSeparatesSummedQueueWaitFromHeavyWaiterCount(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "heavy", Ran: true, WorkNs: 100_000_000, QueueNs: 60_000_000},
		{ID: 1, Mode: "build", Kind: model.KindCompile, Package: "short", Ran: true, WorkNs: 100_000_000, QueueNs: 40_000_000},
	}
	out := Run(model.Summarize(acts, 200_000_000), acts, Options{TopN: 2, PathN: 2, Cores: 8})
	if !strings.Contains(out, "0.10s summed queue wait") {
		t.Errorf("queue duration must include both short and heavy waiters; got:\n%s", out)
	}
	if !strings.Contains(out, "1 action waited over 50ms") {
		t.Errorf("heavy-waiter count must be reported separately; got:\n%s", out)
	}
	if strings.Contains(out, "graph is narrow") {
		t.Errorf("queue wait alone does not prove the graph is narrow; got:\n%s", out)
	}
}

func TestReportFailedBuild(t *testing.T) {
	acts := fixture(t, "failed.json")
	s := model.Summarize(acts, 50_000_000)
	out := Run(s, acts, Options{TopN: 3, PathN: 3, Cores: 8, ExitCode: 1})
	if !strings.Contains(out, "build failed") {
		t.Errorf("a failed build must be labelled; got:\n%s", out)
	}
}

func TestReportEmptyGraph(t *testing.T) {
	out := Run(model.Summarize(nil, 0), nil, Options{TopN: 3, PathN: 3, Cores: 8})
	if out == "" {
		t.Error("an empty graph should still produce a line, not nothing")
	}
}

func TestReportEmptyGraphLabelsFailedBuild(t *testing.T) {
	out := Run(model.Summarize(nil, 0), nil, Options{TopN: 3, PathN: 3, Cores: 8, ExitCode: 2})
	if !strings.Contains(out, "build failed (exit 2)") {
		t.Errorf("a failed build with no actions must still be labelled; got:\n%s", out)
	}
	if !strings.Contains(out, "no build actions recorded") {
		t.Errorf("a failed build with no actions must explain the empty graph; got:\n%s", out)
	}
}

func TestReportShowsRebuildRootCandidates(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Package: "app", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000, Deps: []int{1}},
		{ID: 1, Package: "config", Kind: model.KindCompile, Ran: true, WorkNs: 500_000_000},
	}
	out := Run(model.Summarize(acts, 2_000_000_000), acts, Options{
		TopN: 5, PathN: 5, Cores: 8, ShowRoots: true,
	})
	if !strings.Contains(out, "why it rebuilt") {
		t.Errorf("expected a rebuild-root section; got:\n%s", out)
	}
	if !strings.Contains(out, "config") {
		t.Errorf("the rebuild-root candidate should be named; got:\n%s", out)
	}
	if strings.Contains(out, "changed on its own") {
		t.Errorf("a single run cannot prove an action changed on its own; got:\n%s", out)
	}
}

func TestReportUsesHashEvidenceToGroupRootCandidates(t *testing.T) {
	const appDigest = "0011223344556677889900112233445566778899001122334455667788990011"
	const configDigest = "2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803"
	appID := reportActionID(t, appDigest)
	configID := reportActionID(t, configDigest)
	acts := []model.Action{
		{ID: 0, Package: "app", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000, ActionID: appID, BuildID: appID + "/app-output", Deps: []int{1}},
		{ID: 1, Package: "config", Kind: model.KindCompile, Ran: true, WorkNs: 500_000_000, ActionID: configID, BuildID: configID + "/config-output"},
	}
	blocks := map[string]hashlog.Block{
		"build app":    {Name: "build app", Inputs: []string{"import config config-output"}, Digest: appDigest},
		"build config": {Name: "build config", Digest: configDigest},
	}
	out := Run(model.Summarize(acts, 2_000_000_000), acts, Options{
		TopN: 5, PathN: 5, Cores: 8, ShowRoots: true, HashBlocks: blocks,
	})
	if !strings.Contains(out, "config  (candidate root; 1 action followed)") {
		t.Errorf("hash evidence should group app below config; got:\n%s", out)
	}
}

func TestReportOmitsRootsWhenNotRequested(t *testing.T) {
	acts := []model.Action{{ID: 0, Package: "app", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000}}
	out := Run(model.Summarize(acts, 2_000_000_000), acts, Options{TopN: 5, PathN: 5, Cores: 8})
	if strings.Contains(out, "why it rebuilt") {
		t.Errorf("roots must only appear under --explain; got:\n%s", out)
	}
}

func TestReportEndsWithOneNewline(t *testing.T) {
	ran := []model.Action{{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "one", Ran: true, WorkNs: 100_000_000}}
	cached := []model.Action{{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "one", Cached: true}}
	tests := []struct {
		name string
		out  string
	}{
		{name: "empty", out: Run(model.Summarize(nil, 0), nil, Options{})},
		{name: "failed empty", out: Run(model.Summarize(nil, 0), nil, Options{ExitCode: 2})},
		{name: "fully cached", out: Run(model.Summarize(cached, 200_000_000), cached, Options{})},
		{name: "detailed", out: Run(model.Summarize(ran, 200_000_000), ran, Options{TopN: 1, PathN: 1})},
		{name: "saved", out: Run(model.Summarize(ran, 200_000_000), ran, Options{TopN: 1, PathN: 1, RunID: 2})},
		{name: "comparison", out: Run(model.Summarize(ran, 200_000_000), ran, Options{TopN: 1, PathN: 1, RunID: 2, PrevID: 1})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasSuffix(tt.out, "\n") || strings.HasSuffix(tt.out, "\n\n") {
				t.Errorf("report must end with exactly one newline; got %q", tt.out)
			}
		})
	}
}

func reportActionID(t *testing.T, digest string) string {
	t.Helper()
	id, err := hashlog.ActionID(digest)
	if err != nil {
		t.Fatalf("ActionID(%q): %v", digest, err)
	}
	return id
}
