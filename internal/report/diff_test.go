package report

import (
	"strings"
	"testing"

	"github.com/qwer9052/longpole/internal/model"
)

func TestDiffReportsNewlyRebuiltPackages(t *testing.T) {
	before := []model.Action{
		{Package: "a", Kind: model.KindCompile, ActionID: "A1", Cached: true},
		{Package: "b", Kind: model.KindCompile, ActionID: "B1", Cached: true},
	}
	after := []model.Action{
		{Package: "a", Kind: model.KindCompile, ActionID: "A2", Ran: true, WorkNs: 2_000_000_000},
		{Package: "b", Kind: model.KindCompile, ActionID: "B1", Cached: true},
	}
	out := Diff(DiffInput{
		BeforeID: 6, AfterID: 7,
		BeforeWallNs: 8_100_000_000, AfterWallNs: 12_400_000_000,
		Before: before, After: after, TopN: 5,
	})
	if !strings.Contains(out, "a") {
		t.Errorf("package a should be listed as newly rebuilt; got:\n%s", out)
	}
	if strings.Contains(out, "\n    2.00s  b\n") {
		t.Errorf("package b was cached in both runs and must not be listed; got:\n%s", out)
	}
	if !strings.Contains(out, "A1") && !strings.Contains(out, "identity changed") {
		t.Errorf("an identity change should be explained; got:\n%s", out)
	}
}

func TestDiffReportsNoChange(t *testing.T) {
	same := []model.Action{
		{Package: "a", Kind: model.KindCompile, ActionID: "A1", Cached: true},
	}
	out := Diff(DiffInput{
		BeforeID: 1, AfterID: 2,
		BeforeWallNs: 500_000_000, AfterWallNs: 510_000_000,
		Before: same, After: same, TopN: 5,
	})
	if !strings.Contains(out, "no packages changed") {
		t.Errorf("identical runs should say so; got:\n%s", out)
	}
}

func TestDiffShowsWallTimeDelta(t *testing.T) {
	out := Diff(DiffInput{
		BeforeID: 1, AfterID: 2,
		BeforeWallNs: 8_000_000_000, AfterWallNs: 12_000_000_000,
		TopN: 5,
	})
	if !strings.Contains(out, "+4.00s") {
		t.Errorf("expected a +4.00s delta; got:\n%s", out)
	}
}

func TestDiffShowsImprovement(t *testing.T) {
	out := Diff(DiffInput{
		BeforeID: 1, AfterID: 2,
		BeforeWallNs: 12_000_000_000, AfterWallNs: 8_000_000_000,
		TopN: 5,
	})
	if !strings.Contains(out, "-4.00s") {
		t.Errorf("expected a -4.00s delta; got:\n%s", out)
	}
}

func TestDiffHandlesNewPackage(t *testing.T) {
	after := []model.Action{
		{Package: "brand-new", Kind: model.KindCompile, ActionID: "N1", Ran: true, WorkNs: 1_000_000_000},
	}
	out := Diff(DiffInput{BeforeID: 1, AfterID: 2, After: after, TopN: 5})
	if !strings.Contains(out, "brand-new") {
		t.Errorf("a package absent from the earlier run should still be listed; got:\n%s", out)
	}
}

func TestDiffAllowsNoPackageRowsWhenTopNIsNonPositive(t *testing.T) {
	after := []model.Action{
		{Package: "a", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000},
	}
	for _, tt := range []struct {
		name string
		topN int
	}{
		{name: "zero limit", topN: 0},
		{name: "negative limit", topN: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := Diff(DiffInput{After: after, TopN: tt.topN})
			if !strings.Contains(out, "+ 1 more") {
				t.Errorf("TopN %d should summarize omitted packages; got:\n%s", tt.topN, out)
			}
		})
	}
}

func TestDiffKeepsCompileAndLinkActionsDistinct(t *testing.T) {
	before := []model.Action{
		{Package: "app", Kind: model.KindCompile, Cached: true, ActionID: "compile-old"},
		{Package: "app", Kind: model.KindLink, Cached: true, ActionID: "link-old"},
	}
	after := []model.Action{
		{Package: "app", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000, ActionID: "compile-new"},
		{Package: "app", Kind: model.KindLink, Ran: true, WorkNs: 2_000_000_000, ActionID: "link-new"},
	}
	out := Diff(DiffInput{Before: before, After: after, TopN: 5})
	if !strings.Contains(out, "app (link)") {
		t.Errorf("link action should be disambiguated; got:\n%s", out)
	}
	if !strings.Contains(out, "2 packages") {
		t.Errorf("compile and link actions should both be listed; got:\n%s", out)
	}
}

func TestDiffMatchesDuplicateLinkPackageByMode(t *testing.T) {
	before := []model.Action{
		{Package: "app", Mode: "link", Kind: model.KindLink, Cached: true, ActionID: "link-old"},
		{Package: "app", Mode: "link-install", Kind: model.KindLink, Cached: true},
	}
	after := []model.Action{
		{Package: "app", Mode: "link", Kind: model.KindLink, Ran: true, WorkNs: 1_000_000_000, ActionID: "link-new"},
	}
	out := Diff(DiffInput{Before: before, After: after, TopN: 5})
	if !strings.Contains(out, "link-old -> link-new") {
		t.Errorf("link action should retain its previous identity; got:\n%s", out)
	}
}
