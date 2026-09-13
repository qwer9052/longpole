// Package critpath finds the longest chain of dependent work in a build. That
// chain is the floor on build time: no amount of extra parallelism makes the
// build faster than its critical path.
package critpath

import "github.com/qwer9052/longpole/internal/model"

// Find returns the heaviest dependency chain and its total cost in nanoseconds.
// The path is ordered leaf-first, matching the order the work happens.
//
// Cost is subprocess work time, not wall time, so cached actions contribute
// zero and the path naturally follows what actually had to be rebuilt.
func Find(acts []model.Action) ([]model.Action, int64) {
	n := len(acts)
	if n == 0 {
		return nil, 0
	}

	// cost[i] is the heaviest chain ending at i, inclusive. next[i] is the dep
	// that chain continues into, or -1.
	cost := make([]int64, n)
	next := make([]int, n)
	// 0 = unvisited, 1 = in progress, 2 = done. "In progress" is how a cyclic
	// graph is made to terminate: a back edge contributes nothing.
	state := make([]int8, n)

	var walk func(i int) int64
	walk = func(i int) int64 {
		if i < 0 || i >= n {
			return 0
		}
		if state[i] == 2 {
			return cost[i]
		}
		if state[i] == 1 {
			return 0
		}
		state[i] = 1

		var best int64
		bestDep := -1
		for _, d := range acts[i].Deps {
			if d < 0 || d >= n {
				continue
			}
			if c := walk(d); c > best {
				best, bestDep = c, d
			}
		}

		cost[i] = acts[i].WorkNs + best
		next[i] = bestDep
		state[i] = 2
		return cost[i]
	}

	var start int
	var total int64
	for i := range acts {
		if c := walk(i); c > total {
			total, start = c, i
		}
	}
	if total == 0 {
		return nil, 0
	}

	// Collect root-to-leaf, then reverse so the report reads leaf-first.
	var path []model.Action
	for i := start; i >= 0; i = next[i] {
		path = append(path, acts[i])
	}
	for l, r := 0, len(path)-1; l < r; l, r = l+1, r-1 {
		path[l], path[r] = path[r], path[l]
	}
	return path, total
}

// BlastRadius counts, for each action index, how many other actions depend on
// it transitively. A high count on a slow package is the clearest signal that
// splitting that package would help.
func BlastRadius(acts []model.Action) []int {
	n := len(acts)
	out := make([]int, n)
	if n == 0 {
		return out
	}
	indexes := make([]int, n)
	for i := range indexes {
		indexes[i] = i
	}
	for i, radius := range BlastRadiusFor(acts, indexes) {
		out[i] = radius
	}
	return out
}

// BlastRadiusFor returns the transitive dependent count for only the requested
// action indexes. Report rendering needs this narrow form for its TopN list,
// while BlastRadius retains the complete result for callers that need it.
func BlastRadiusFor(acts []model.Action, indexes []int) map[int]int {
	n := len(acts)
	out := make(map[int]int, len(indexes))
	if n == 0 {
		return out
	}

	// Reverse the edges: dependents[d] lists everything that depends on d.
	dependents := make([][]int, n)
	for i, a := range acts {
		for _, d := range a.Deps {
			if d >= 0 && d < n {
				dependents[d] = append(dependents[d], i)
			}
		}
	}

	for _, i := range indexes {
		if i < 0 || i >= n {
			continue
		}
		out[i] = 0
		seen := make([]bool, n)
		seen[i] = true
		stack := []int{i}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, up := range dependents[cur] {
				if !seen[up] {
					seen[up] = true
					out[i]++
					stack = append(stack, up)
				}
			}
		}
	}
	return out
}
