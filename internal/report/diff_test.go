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
	if !strings.Contains(out, "no additional actions ran") {
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
	if !strings.Contains(out, "new package") {
		t.Errorf("a package absent from the earlier run should be labeled new; got:\n%s", out)
	}
	if strings.Contains(out, "cached last time") {
		t.Errorf("a new package must not be called cached; got:\n%s", out)
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
	if !strings.Contains(out, "2 actions") {
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

func TestDiffMatchesDuplicatePackageVariantsByActionID(t *testing.T) {
	before := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Cached: true, ActionID: "regular-old"},
		{Package: "p", Mode: "build", Kind: model.KindCompile, Cached: true, ActionID: "test-unchanged"},
	}
	after := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000, ActionID: "regular-new"},
		{Package: "p", Mode: "build", Kind: model.KindCompile, Cached: true, ActionID: "test-unchanged"},
	}
	out := Diff(DiffInput{Before: before, After: after, TopN: 5})
	if !strings.Contains(out, "regular-old -> regular-new") {
		t.Errorf("changed variant should retain its matching old identity; got:\n%s", out)
	}
	if strings.Contains(out, "test-unchanged -> regular-new") {
		t.Errorf("unchanged variant must not be used as the prior identity; got:\n%s", out)
	}
}

func TestDiffDoesNotClaimUnchangedWhenBothRunsRan(t *testing.T) {
	before := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, ActionID: "old"},
	}
	after := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, ActionID: "new"},
	}
	out := Diff(DiffInput{Before: before, After: after, TopN: 5})
	if !strings.Contains(out, "no additional actions ran") {
		t.Errorf("runs that both rebuilt should not be called unchanged; got:\n%s", out)
	}
	if strings.Contains(out, "no packages changed") {
		t.Errorf("runs with changed identities must not be called unchanged; got:\n%s", out)
	}
}

func TestDiffLeavesOneToManyVariantsAmbiguous(t *testing.T) {
	before := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Cached: true, ActionID: "old"},
	}
	variants := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 2_000_000_000, ActionID: "first"},
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000, ActionID: "second"},
	}
	for _, tt := range []struct {
		name  string
		after []model.Action
	}{
		{name: "original order", after: variants},
		{name: "reversed order", after: []model.Action{variants[1], variants[0]}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := Diff(DiffInput{Before: before, After: tt.after, TopN: 5})
			if !strings.Contains(out, "ambiguous action variants") {
				t.Errorf("one-to-many variants should be qualified as ambiguous; got:\n%s", out)
			}
			if strings.Contains(out, "old ->") {
				t.Errorf("ambiguous variants must not invent an identity transition; got:\n%s", out)
			}
			if strings.Contains(out, "ran this time, did not run last time") {
				t.Errorf("ambiguous variants must not be counted as confirmed additional work; got:\n%s", out)
			}
		})
	}
}

func TestDiffLeavesAllRanDuplicateVariantsAmbiguous(t *testing.T) {
	before := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, ActionID: "old-one"},
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, ActionID: "old-two"},
	}
	after := []model.Action{
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 2_000_000_000, ActionID: "new-one"},
		{Package: "p", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000, ActionID: "new-two"},
	}
	out := Diff(DiffInput{Before: before, After: after, TopN: 5})
	if !strings.Contains(out, "ambiguous action variants") {
		t.Errorf("duplicate rebuilt variants should be qualified as ambiguous; got:\n%s", out)
	}
	if strings.Contains(out, "ran this time, did not run last time") {
		t.Errorf("ambiguous rebuilt variants must not be counted as confirmed additional work; got:\n%s", out)
	}
}
