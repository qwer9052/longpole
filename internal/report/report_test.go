package report

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qwer9052/longpole/internal/actiongraph"
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
	if !strings.Contains(out, "across 1 action)") {
		t.Errorf("a single cache probe must use singular grammar; got:\n%s", out)
	}
	golden(t, "fully_cached.txt", out)
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

func TestReportLabelsCacheProbeTimeAsWallTime(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "ran-package", Ran: true, WorkNs: 100_000_000},
		{ID: 1, Mode: "build check cache", Kind: model.KindCacheProbe, Package: "cached-package", WallNs: 200_000_000},
	}
	out := Run(model.Summarize(acts, 300_000_000), acts, Options{TopN: 1, PathN: 1, Cores: 8})
	if !strings.Contains(out, "cache       0.20s  wall") {
		t.Errorf("cache probing must be labelled as wall time, not a share of work; got:\n%s", out)
	}
}

func TestReportUsesSingularAction(t *testing.T) {
	acts := []model.Action{{ID: 0, Mode: "build", Kind: model.KindCompile, Package: "one", Ran: true, WorkNs: 100_000_000, QueueNs: 60_000_000}}
	out := Run(model.Summarize(acts, 200_000_000), acts, Options{TopN: 1, PathN: 1, Cores: 8})
	if strings.Contains(out, "1 actions") {
		t.Errorf("a count of one must use singular grammar; got:\n%s", out)
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
