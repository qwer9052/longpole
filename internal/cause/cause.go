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

// Root is a rebuilt package with no hash-evidenced rebuilt dependency.
// Downstream is the number of other rebuilt actions hash-evidenced to depend
// on it.
type Root struct {
	Package    string
	Downstream int
}

// Roots identifies the starts of rebuild cascades in one action graph. An
// action-graph dependency alone is not causal evidence: independent changes
// can rebuild both sides. An edge is followed only when the dependent's import
// input matches the dependency's output content ID, and the dependency's hash
// digest identifies that action. Missing or inconsistent evidence leaves both
// actions as roots rather than guessing.
func Roots(actions []model.Action, blocks map[string]hashlog.Block) []Root {
	ran := make([]bool, len(actions))
	for i, action := range actions {
		ran[i] = action.Ran && (action.Kind == model.KindCompile || action.Kind == model.KindLink)
	}

	dependents := causalDependents(actions, ran, blocks)
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

func causalDependents(actions []model.Action, ran []bool, blocks map[string]hashlog.Block) [][]int {
	dependents := make([][]int, len(actions))
	for dependent, action := range actions {
		if !ran[dependent] {
			continue
		}
		block, ok := blockForAction(blocks, action)
		if !ok || !blockMatchesAction(block, action) {
			continue
		}
		imports := importedOutputs(block)
		for _, dependency := range action.Deps {
			if dependency < 0 || dependency >= len(actions) || !ran[dependency] {
				continue
			}
			output, ok := actionOutput(actions[dependency], blocks)
			if !ok || !imports[actions[dependency].Package][output] {
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

func importedOutputs(block hashlog.Block) map[string]map[string]bool {
	outputs := make(map[string]map[string]bool)
	for _, input := range block.Inputs {
		fields := strings.Fields(trimLine(input))
		if len(fields) != 3 || fields[0] != "import" {
			continue
		}
		if outputs[fields[1]] == nil {
			outputs[fields[1]] = make(map[string]bool)
		}
		outputs[fields[1]][fields[2]] = true
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
