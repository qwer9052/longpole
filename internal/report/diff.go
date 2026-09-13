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

// change is one action that behaved differently between the two runs.
type change struct {
	Package        string
	WorkNs         int64
	WasCached      bool
	OldAction      string
	NewAction      string
	IsNewPackage   bool
	IsNewAction    bool
	VariantUnknown bool
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
		b.WriteString("\n  no additional actions ran between these runs\n\n")
		return b.String()
	}

	confirmed, ambiguous := splitChanges(changes)
	if len(confirmed) > 0 {
		writeChanges(&b, "ran this time, did not run last time", confirmed, in.TopN)
		writeIdentityNote(&b, confirmed)
	}
	if len(ambiguous) > 0 {
		writeChanges(&b, "ambiguous action variants (not counted as additional work)", ambiguous, in.TopN)
	}
	b.WriteString("\n")
	return b.String()
}

func splitChanges(changes []change) (confirmed, ambiguous []change) {
	for _, c := range changes {
		if c.VariantUnknown {
			ambiguous = append(ambiguous, c)
		} else {
			confirmed = append(confirmed, c)
		}
	}
	return confirmed, ambiguous
}

func writeChanges(b *strings.Builder, heading string, changes []change, topN int) {
	var totalWork int64
	for _, c := range changes {
		totalWork += c.WorkNs
	}
	fmt.Fprintf(b, "\n  %s     %d %s, +%s\n", heading,
		len(changes), actionWord(len(changes)), Dur(totalWork))

	n := topN
	if n < 0 {
		n = 0
	}
	if n > len(changes) {
		n = len(changes)
	}
	for _, c := range changes[:n] {
		fmt.Fprintf(b, "    %7s  %s  %s\n", Dur(c.WorkNs), c.Package, previousStatus(c))
	}
	if rest := len(changes) - n; rest > 0 {
		fmt.Fprintf(b, "    + %d more\n", rest)
	}
}

// findChanges returns actions that ran in the later build but did not in the
// earlier one, heaviest first. Those are the ones that cost time this run and
// did not last run, which is the question a diff exists to answer.
func findChanges(before, after []model.Action) []change {
	previous := make([]model.Action, 0, len(before))
	knownPackages := make(map[string]bool, len(before))
	knownActions := make(map[actionKey]bool, len(before))
	byIdentity := make(map[actionKey]map[string][]int, len(before))
	for _, a := range before {
		if !isWorkAction(a) {
			continue
		}
		index := len(previous)
		previous = append(previous, a)
		key := keyFor(a)
		knownPackages[a.Package] = true
		knownActions[key] = true
		if a.ActionID == "" {
			continue
		}
		if byIdentity[key] == nil {
			byIdentity[key] = make(map[string][]int)
		}
		byIdentity[key][a.ActionID] = append(byIdentity[key][a.ActionID], index)
	}

	matchedBefore := make([]bool, len(previous))
	matches := make(map[int]int, len(after))
	// Match stable identities first: test variants can share package and mode
	// with regular builds, so pairing by either alone can invent a transition.
	for i, a := range after {
		if !isWorkAction(a) || a.ActionID == "" {
			continue
		}
		key := keyFor(a)
		for _, beforeIndex := range byIdentity[key][a.ActionID] {
			if !matchedBefore[beforeIndex] {
				matches[i] = beforeIndex
				matchedBefore[beforeIndex] = true
				break
			}
		}
	}
	unmatchedByKey := make(map[actionKey][]int, len(previous))
	for i, a := range previous {
		if !matchedBefore[i] {
			key := keyFor(a)
			unmatchedByKey[key] = append(unmatchedByKey[key], i)
		}
	}
	unmatchedAfterByKey := make(map[actionKey][]int, len(after))
	for i, a := range after {
		if !isWorkAction(a) {
			continue
		}
		if _, ok := matches[i]; ok {
			continue
		}
		unmatchedAfterByKey[keyFor(a)] = append(unmatchedAfterByKey[keyFor(a)], i)
	}
	for key, afterIndexes := range unmatchedAfterByKey {
		beforeIndexes := unmatchedByKey[key]
		// Pair only a one-to-one remainder. More candidates on either side do
		// not identify a variant, so treating one as changed would be a guess.
		if len(beforeIndexes) == 1 && len(afterIndexes) == 1 {
			matches[afterIndexes[0]] = beforeIndexes[0]
		}
	}

	var out []change
	for i, a := range after {
		if !a.Ran || !isWorkAction(a) {
			continue
		}
		beforeIndex, matched := matches[i]
		if matched && previous[beforeIndex].Ran {
			continue
		}

		c := change{Package: pkgName(a), WorkNs: a.WorkNs, NewAction: a.ActionID}
		if matched {
			old := previous[beforeIndex]
			c.WasCached = old.Cached
			c.OldAction = old.ActionID
		} else if !knownPackages[a.Package] {
			c.IsNewPackage = true
		} else if !knownActions[keyFor(a)] {
			c.IsNewAction = true
		} else {
			c.VariantUnknown = true
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].WorkNs > out[j].WorkNs })
	return out
}

func isWorkAction(a model.Action) bool {
	return a.Package != "" && (a.Kind == model.KindCompile || a.Kind == model.KindLink)
}

func keyFor(a model.Action) actionKey {
	return actionKey{packageName: a.Package, mode: a.Mode}
}

func previousStatus(c change) string {
	if c.IsNewPackage {
		return "new package"
	}
	if c.IsNewAction {
		return "new action"
	}
	if c.VariantUnknown {
		return "previous variant unknown"
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
