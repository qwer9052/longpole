// Package cause identifies evidence associated with actions that rebuilt.
//
// Across runs, hash-input differences identify observed input differences.
// Within one run, verified import/output joins form a conservative candidate
// rebuild frontier. A single run has no earlier value to compare, so its
// frontier is not a causal verdict.
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
			IsImport: strings.HasPrefix(strings.TrimSpace(new), "import "),
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

// Root is one package on the candidate rebuild frontier. Downstream is the
// number of other rebuilt actions joined to it by current-run hash evidence.
// Neither field states why an action rebuilt; that requires a cross-run diff.
type Root struct {
	Package    string
	Downstream int
}

// Roots returns a conservative candidate rebuild frontier for one action
// graph. An action-graph dependency alone does not join candidates: independent
// rebuilds can have the same edge. Candidates are joined only when the
// dependent's current import or packagefile input matches the dependency's
// current output content ID and both hash blocks identify their actions. This
// is evidence of an observed relationship, not proof that either action's
// input changed.
func Roots(actions []model.Action, blocks map[string]hashlog.Block) []Root {
	ran := make([]bool, len(actions))
	for i, action := range actions {
		ran[i] = action.Ran && (action.Kind == model.KindCompile || action.Kind == model.KindLink)
	}

	dependents := candidateDependents(actions, ran, blocks)
	hasCause := make([]bool, len(actions))
	for _, downstream := range dependents {
		for _, dependent := range downstream {
			hasCause[dependent] = true
		}
	}
	var roots []Root
	for i, action := range actions {
		if !ran[i] || hasCause[i] {
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

func candidateDependents(actions []model.Action, ran []bool, blocks map[string]hashlog.Block) [][]int {
	dependents := make([][]int, len(actions))
	for dependent, action := range actions {
		if !ran[dependent] {
			continue
		}
		block, ok := blockForAction(blocks, action)
		if !ok || !blockMatchesAction(block, action) {
			continue
		}
		references := referencedOutputs(block)
		for _, dependency := range action.Deps {
			if dependency < 0 || dependency >= len(actions) || !ran[dependency] {
				continue
			}
			output, ok := actionOutput(actions[dependency], blocks)
			if !ok || !references[actions[dependency].Package][output] {
				continue
			}
			dependents[dependency] = append(dependents[dependency], dependent)
		}
	}
	return dependents
}

func blockForAction(blocks map[string]hashlog.Block, action model.Action) (hashlog.Block, bool) {
	for _, name := range []string{action.Mode + " " + action.Package, "build " + action.Package, "link " + action.Package} {
		if block, ok := blocks[name]; ok {
			return block, true
		}
	}
	return hashlog.Block{}, false
}

func referencedOutputs(block hashlog.Block) map[string]map[string]bool {
	outputs := make(map[string]map[string]bool)
	for _, input := range block.Inputs {
		fields := strings.Fields(trimLine(input))
		var pkg, output string
		switch {
		case len(fields) == 3 && fields[0] == "import":
			pkg, output = fields[1], fields[2]
		case len(fields) == 2 && fields[0] == "packagefile":
			var ok bool
			pkg, output, ok = strings.Cut(fields[1], "=")
			if !ok || pkg == "" || output == "" {
				continue
			}
		default:
			continue
		}
		if outputs[pkg] == nil {
			outputs[pkg] = make(map[string]bool)
		}
		outputs[pkg][output] = true
	}
	return outputs
}

func actionOutput(action model.Action, blocks map[string]hashlog.Block) (string, bool) {
	block, ok := blockForAction(blocks, action)
	if !ok || !blockMatchesAction(block, action) {
		return "", false
	}
	_, output, _ := strings.Cut(action.BuildID, "/")
	return output, true
}

func blockMatchesAction(block hashlog.Block, action model.Action) bool {
	digestID, err := hashlog.ActionID(block.Digest)
	if err != nil {
		return false
	}
	buildActionID, output, ok := strings.Cut(action.BuildID, "/")
	if !ok || output == "" || digestID != buildActionID {
		return false
	}
	if action.ActionID != "" && action.ActionID != digestID {
		return false
	}
	return true
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
