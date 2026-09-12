package critpath

import (
	"testing"
	"time"

	"github.com/qwer9052/longpole/internal/model"
)

// chain: 0 -> 1 -> 2, costs 10, 20, 30. The path is the whole chain.
func TestSimpleChain(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Package: "top", WorkNs: 10, Deps: []int{1}},
		{ID: 1, Package: "mid", WorkNs: 20, Deps: []int{2}},
		{ID: 2, Package: "leaf", WorkNs: 30},
	}
	path, total := Find(acts)
	if total != 60 {
		t.Errorf("total = %d, want 60", total)
	}
	if len(path) != 3 {
		t.Fatalf("path length = %d, want 3", len(path))
	}
	// Reported leaf-first, because that is the order work happens.
	if path[0].Package != "leaf" || path[2].Package != "top" {
		t.Errorf("path order wrong: %s -> %s -> %s",
			path[0].Package, path[1].Package, path[2].Package)
	}
}

// diamond: 0 depends on 1 and 2, both depend on 3. The heavier branch wins.
func TestDiamondPicksHeavierBranch(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Package: "top", WorkNs: 1, Deps: []int{1, 2}},
		{ID: 1, Package: "light", WorkNs: 5, Deps: []int{3}},
		{ID: 2, Package: "heavy", WorkNs: 50, Deps: []int{3}},
		{ID: 3, Package: "base", WorkNs: 10},
	}
	path, total := Find(acts)
	if total != 61 {
		t.Errorf("total = %d, want 61", total)
	}
	var names []string
	for _, a := range path {
		names = append(names, a.Package)
	}
	if len(names) != 3 || names[1] != "heavy" {
		t.Errorf("path = %v, want the heavy branch", names)
	}
}

func TestWideGraphHasShortPath(t *testing.T) {
	// One root depending on 50 independent leaves of cost 10 each.
	acts := []model.Action{{ID: 0, Package: "root", WorkNs: 1}}
	for i := 1; i <= 50; i++ {
		acts[0].Deps = append(acts[0].Deps, i)
		acts = append(acts, model.Action{ID: i, WorkNs: 10})
	}
	_, total := Find(acts)
	if total != 11 {
		t.Errorf("total = %d, want 11 (one leaf plus the root)", total)
	}
}

func TestEmptyGraph(t *testing.T) {
	path, total := Find(nil)
	if len(path) != 0 || total != 0 {
		t.Errorf("empty graph gave path=%d total=%d, want 0 and 0", len(path), total)
	}
}

func TestOutOfRangeDepIsIgnored(t *testing.T) {
	acts := []model.Action{
		{ID: 0, WorkNs: 5, Deps: []int{99}},
	}
	_, total := Find(acts)
	if total != 5 {
		t.Errorf("total = %d, want 5; a dangling dep must not panic or add cost", total)
	}
}

func TestCycleDoesNotHang(t *testing.T) {
	// The go command never emits cycles, but a corrupt file must not hang.
	acts := []model.Action{
		{ID: 0, WorkNs: 1, Deps: []int{1}},
		{ID: 1, WorkNs: 1, Deps: []int{0}},
	}
	done := make(chan struct{})
	go func() {
		Find(acts)
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutAfterOneSecond():
		t.Fatal("Find did not terminate on a cyclic graph")
	}
}

func TestBlastRadius(t *testing.T) {
	// 3 and 2 both feed 0; 1 feeds nothing.
	acts := []model.Action{
		{ID: 0, Package: "top", Deps: []int{2}},
		{ID: 1, Package: "orphan"},
		{ID: 2, Package: "mid", Deps: []int{3}},
		{ID: 3, Package: "base"},
	}
	r := BlastRadius(acts)
	if r[3] != 2 {
		t.Errorf("base blocks %d actions, want 2", r[3])
	}
	if r[1] != 0 {
		t.Errorf("orphan blocks %d actions, want 0", r[1])
	}
}

func TestBlastRadiusExcludesActionItselfInCycle(t *testing.T) {
	acts := []model.Action{
		{ID: 0, Deps: []int{1}},
		{ID: 1, Deps: []int{0}},
	}
	r := BlastRadius(acts)
	if r[0] != 1 || r[1] != 1 {
		t.Errorf("cycle blast radius = %v, want [1 1]", r)
	}
}

func BenchmarkBlastRadiusRealGraph(b *testing.B) {
	acts := make([]model.Action, 400)
	for i := range acts {
		acts[i] = model.Action{ID: i, WorkNs: int64(i)}
		if i > 0 {
			acts[i].Deps = []int{i - 1}
		}
	}
	// One link-like action depending on everything, as real graphs have.
	for i := 1; i < 400; i++ {
		acts[0].Deps = append(acts[0].Deps, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlastRadius(acts)
	}
}

func timeoutAfterOneSecond() <-chan time.Time {
	return time.After(time.Second)
}
