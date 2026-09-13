package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qwer9052/longpole/internal/model"
)

// DiffInput is everything needed to compare two recorded runs.
type DiffInput struct {
	BeforeID, AfterID         int64
	BeforeWallNs, AfterWallNs int64
	Before, After             []model.Action
	TopN                      int
}

// change is one package that behaved differently between the two runs.
type change struct {
	Package   string
	WorkNs    int64
	WasCached bool
	OldAction string
	NewAction string
	IsNew     bool
}

type actionKey struct {
	packageName string
	mode        string
}

// Diff renders the comparison of two runs.
func Diff(in DiffInput) string {
	var b strings.Builder

	delta := in.AfterWallNs - in.BeforeWallNs
	sign := "+"
	if delta < 0 {
		sign = "-"
		delta = -delta
	}
	fmt.Fprintf(&b, "\n  run %d -> run %d        %s -> %s   (%s%s)\n",
		in.BeforeID, in.AfterID, Dur(in.BeforeWallNs), Dur(in.AfterWallNs), sign, Dur(delta))

	changes := findChanges(in.Before, in.After)
	if len(changes) == 0 {
		b.WriteString("\n  no packages changed between these runs\n\n")
		return b.String()
	}

	var totalWork int64
	for _, c := range changes {
		totalWork += c.WorkNs
	}
	fmt.Fprintf(&b, "\n  ran this time, did not run last time     %d packages, +%s\n",
		len(changes), Dur(totalWork))

	n := in.TopN
	if n < 0 {
		n = 0
	}
	if n > len(changes) {
		n = len(changes)
	}
	for _, c := range changes[:n] {
		fmt.Fprintf(&b, "    %7s  %s  %s\n", Dur(c.WorkNs), c.Package, previousStatus(c))
	}
	if rest := len(changes) - n; rest > 0 {
		fmt.Fprintf(&b, "    + %d more\n", rest)
	}

	writeIdentityNote(&b, changes)
	b.WriteString("\n")
	return b.String()
}

// findChanges returns packages that ran in the later build but did not in the
// earlier one, heaviest first. Those are the ones that cost time this run and
// did not last run, which is the question a diff exists to answer.
func findChanges(before, after []model.Action) []change {
	previous := make(map[actionKey]model.Action, len(before))
	for _, a := range before {
		if a.Kind != model.KindCompile && a.Kind != model.KindLink {
			continue
		}
		previous[actionKey{packageName: a.Package, mode: a.Mode}] = a
	}

	var out []change
	for _, a := range after {
		if !a.Ran || a.Package == "" || (a.Kind != model.KindCompile && a.Kind != model.KindLink) {
			continue
		}
		old, existed := previous[actionKey{packageName: a.Package, mode: a.Mode}]
		if existed && old.Ran {
			continue
		}
		out = append(out, change{
			Package:   pkgName(a),
			WorkNs:    a.WorkNs,
			WasCached: old.Cached,
			OldAction: old.ActionID,
			NewAction: a.ActionID,
			IsNew:     !existed,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].WorkNs > out[j].WorkNs })
	return out
}

func previousStatus(c change) string {
	if c.IsNew {
		return "new package"
	}
	if c.WasCached {
		return "cached last time"
	}
	return "did not run last time"
}

// writeIdentityNote explains the mechanism behind the rebuilds. Without this
// the reader is told what happened but not why it could have happened.
func writeIdentityNote(b *strings.Builder, changes []change) {
	for _, c := range changes {
		if c.OldAction != "" && c.NewAction != "" && c.OldAction != c.NewAction {
			fmt.Fprintf(b, "\n  identity changed  %s\n", c.Package)
			fmt.Fprintf(b, "    %s -> %s\n", c.OldAction, c.NewAction)
			b.WriteString("    run with --explain to see which input changed\n")
			return
		}
	}
}
