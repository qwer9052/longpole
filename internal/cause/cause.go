// Package cause identifies evidence for actions that rebuilt.
//
// Across runs, hash-input differences identify the input that changed. Within
// a single run, dependency edges distinguish a package with a rebuilt
// dependency from the package where a rebuild cascade began. Neither result
// claims a cause when the required baseline data is absent.
package cause

import (
	"sort"
	"strings"

	"github.com/qwer9052/longpole/internal/hashlog"
	"github.com/qwer9052/longpole/internal/model"
)

// Input is one hash input that differed between runs.
type Input struct {
	Label    string
	Before   string
	After    string
	IsImport bool
}

// Change is the changed inputs for one hash block.
type Change struct {
	Name    string
	Changed []Input
}

// DiffBlocks compares ordered hash inputs from two runs. A block missing from
// before is excluded because it has no baseline and therefore no evidence of
// what changed.
func DiffBlocks(before, after map[string]hashlog.Block) []Change {
	var changes []Change
	for name, current := range after {
		prior, ok := before[name]
		if !ok {
			continue
		}

		changed := diffInputs(prior.Inputs, current.Inputs)
		if len(changed) != 0 {
			changes = append(changes, Change{Name: name, Changed: changed})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}

func diffInputs(before, after []string) []Input {
	max := len(before)
	if len(after) > max {
		max = len(after)
	}

	var changed []Input
	for i := 0; i < max; i++ {
		var old, new string
		if i < len(before) {
			old = before[i]
		}
		if i < len(after) {
			new = after[i]
		}
		if old == new {
			continue
		}

		label := inputLabel(old, new)
		changed = append(changed, Input{
			Label:    label,
			Before:   trimLine(old),
			After:    trimLine(new),
			IsImport: strings.HasPrefix(label, "import "),
		})
	}
	return changed
}

// inputLabel omits a trailing value for the two input forms whose stable name
// is useful to readers. Other forms are retained verbatim because their value
// is itself the evidence.
func inputLabel(before, after string) string {
	line := trimLine(before)
	if line == "" {
		line = trimLine(after)
	}
	fields := strings.Fields(line)
	if len(fields) >= 3 && (fields[0] == "file" || fields[0] == "import") {
		return fields[0] + " " + fields[1]
	}
	return line
}

func trimLine(line string) string {
	return strings.TrimRight(line, "\r\n")
}

// Root is a rebuilt package with no rebuilt dependency. Downstream is the
// number of other rebuilt actions that transitively depend on it.
type Root struct {
	Package    string
	Downstream int
}

// Roots identifies the starts of rebuild cascades in one action graph. Hash
// blocks are accepted so callers can use the same data they use for DiffBlocks;
// action-graph dependencies are the available evidence for a first-run chain.
func Roots(actions []model.Action, _ map[string]hashlog.Block) []Root {
	ran := make([]bool, len(actions))
	for i, action := range actions {
		ran[i] = action.Ran && (action.Kind == model.KindCompile || action.Kind == model.KindLink)
	}

	dependents := rebuiltDependents(actions)
	var roots []Root
	for i, action := range actions {
		if !ran[i] || hasRebuiltDependency(action.Deps, ran) {
			continue
		}
		roots = append(roots, Root{
			Package:    action.Package,
			Downstream: countDownstream(dependents, ran, i),
		})
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].Downstream != roots[j].Downstream {
			return roots[i].Downstream > roots[j].Downstream
		}
		return roots[i].Package < roots[j].Package
	})
	return roots
}

func hasRebuiltDependency(deps []int, ran []bool) bool {
	for _, dep := range deps {
		if dep >= 0 && dep < len(ran) && ran[dep] {
			return true
		}
	}
	return false
}

func rebuiltDependents(actions []model.Action) [][]int {
	dependents := make([][]int, len(actions))
	for i, action := range actions {
		for _, dep := range action.Deps {
			if dep >= 0 && dep < len(actions) {
				dependents[dep] = append(dependents[dep], i)
			}
		}
	}
	return dependents
}

func countDownstream(dependents [][]int, ran []bool, start int) int {
	seen := make([]bool, len(ran))
	seen[start] = true
	stack := []int{start}
	count := 0
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, dependent := range dependents[current] {
			if seen[dependent] {
				continue
			}
			seen[dependent] = true
			if ran[dependent] {
				count++
			}
			stack = append(stack, dependent)
		}
	}
	return count
}
