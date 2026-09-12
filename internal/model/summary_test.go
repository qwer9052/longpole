package model

import "testing"

func TestSummarizeCold(t *testing.T) {
	s := Summarize(loadFixture(t, "cold.json"), 0)
	if s.Ran != 195 {
		t.Errorf("Ran = %d, want 195", s.Ran)
	}
	if s.Cached != 1 {
		t.Errorf("Cached = %d, want 1", s.Cached)
	}
	if s.WorkNs <= 0 {
		t.Error("WorkNs should be positive on a cold build")
	}
	if s.ByKind[KindCompile].WorkNs <= 0 {
		t.Error("compile work should be positive on a cold build")
	}
}

func TestFullyCachedRequiresCachedWorkAndNoRanActions(t *testing.T) {
	s := Summarize([]Action{{Kind: KindCompile, Cached: true}}, 0)
	if !s.FullyCached {
		t.Error("cached work with no ran actions should be fully cached")
	}
}

func TestColdBuildIsNotFullyCached(t *testing.T) {
	s := Summarize(loadFixture(t, "cold.json"), 0)
	if s.FullyCached {
		t.Error("cold build must not be reported as fully cached")
	}
}

func TestEmptyGraphIsNotFullyCached(t *testing.T) {
	s := Summarize(nil, 0)
	if s.FullyCached {
		t.Error("a graph with no real work must not be reported as fully cached")
	}
}

func TestParallelismIsWorkOverWall(t *testing.T) {
	acts := []Action{
		{Kind: KindCompile, Ran: true, WorkNs: 4_000_000_000},
	}
	s := Summarize(acts, 2_000_000_000)
	if s.Parallelism < 1.99 || s.Parallelism > 2.01 {
		t.Errorf("Parallelism = %v, want ~2", s.Parallelism)
	}
}

func TestParallelismZeroWallIsSafe(t *testing.T) {
	s := Summarize([]Action{{Kind: KindCompile, Ran: true, WorkNs: 1}}, 0)
	if s.Parallelism != 0 {
		t.Errorf("Parallelism = %v, want 0 when wall time is unknown", s.Parallelism)
	}
}

func TestTopByWorkIsSortedDescending(t *testing.T) {
	acts := []Action{
		{Package: "slow", Kind: KindCompile, Ran: true, WorkNs: 300},
		{Package: "fast", Kind: KindCompile, Ran: true, WorkNs: 100},
		{Package: "mid", Kind: KindCompile, Ran: true, WorkNs: 200},
		{Package: "cached", Kind: KindCompile, Cached: true, WorkNs: 1_000},
	}
	top := TopByWork(acts, 10)
	if len(top) != 3 {
		t.Fatalf("got %d entries, want 3 (cached actions excluded)", len(top))
	}
	if top[0].Package != "slow" || top[1].Package != "mid" || top[2].Package != "fast" {
		t.Errorf("wrong order: %v", []string{top[0].Package, top[1].Package, top[2].Package})
	}
}

func TestTopByWorkRespectsLimit(t *testing.T) {
	acts := []Action{
		{Package: "a", Kind: KindCompile, Ran: true, WorkNs: 3},
		{Package: "b", Kind: KindCompile, Ran: true, WorkNs: 2},
		{Package: "c", Kind: KindCompile, Ran: true, WorkNs: 1},
	}
	if got := len(TopByWork(acts, 2)); got != 2 {
		t.Errorf("got %d entries, want 2", got)
	}
}

func TestTopByWorkNonPositiveLimitIsEmpty(t *testing.T) {
	acts := []Action{{Package: "a", Kind: KindCompile, Ran: true, WorkNs: 1}}
	for _, n := range []int{0, -1} {
		if got := TopByWork(acts, n); len(got) != 0 {
			t.Errorf("TopByWork(_, %d) returned %d entries, want none", n, len(got))
		}
	}
}
