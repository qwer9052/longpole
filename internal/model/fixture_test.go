package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qwer9052/longpole/internal/actiongraph"
)

func loadFixture(t *testing.T, name string) []Action {
	t.Helper()
	p := filepath.Join("..", "actiongraph", "testdata", name)
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open %s: %v", p, err)
	}
	defer f.Close()
	raw, err := actiongraph.Parse(f)
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return NewAll(raw)
}

func counts(acts []Action) (ran, cached int) {
	for _, a := range acts {
		if a.Ran {
			ran++
		}
		if a.Cached {
			cached++
		}
	}
	return
}

func TestColdFixtureMostlyRan(t *testing.T) {
	ran, cached := counts(loadFixture(t, "cold.json"))
	if ran != 195 {
		t.Errorf("cold: ran = %d, want 195", ran)
	}
	if cached != 1 {
		t.Errorf("cold: cached = %d, want 1", cached)
	}
}

// This is the case the incumbent gets wrong: it reports cache probes as the
// slowest build steps instead of saying nothing was rebuilt.
func TestWarmFixtureIsFullyCached(t *testing.T) {
	ran, cached := counts(loadFixture(t, "warm.json"))
	if ran != 1 {
		t.Errorf("warm: ran = %d, want 1", ran)
	}
	if cached != 195 {
		t.Errorf("warm: cached = %d, want 195", cached)
	}
}

func TestSerialFixtureHasRealQueueWait(t *testing.T) {
	acts := loadFixture(t, "serial_p1.json")
	var total int64
	for _, a := range acts {
		total += a.QueueNs
	}
	if total == 0 {
		t.Error("a -p=1 build should show queue wait; got none")
	}
}

func TestNoActionIsBothCachedAndRan(t *testing.T) {
	for _, name := range []string{"cold.json", "warm.json", "gotest.json", "failed.json"} {
		for _, a := range loadFixture(t, name) {
			if a.Cached && a.Ran {
				t.Errorf("%s: action %d is both cached and ran", name, a.ID)
			}
		}
	}
}
