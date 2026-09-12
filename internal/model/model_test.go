package model

import (
	"testing"
	"time"

	"github.com/qwer9052/longpole/internal/actiongraph"
)

func at(sec int, ns int) time.Time {
	return time.Date(2026, 9, 12, 0, 0, sec, ns, time.UTC)
}

func TestRanActionIsNotCached(t *testing.T) {
	a := New(actiongraph.Action{
		Mode: "build", Package: "example.com/a",
		Cmd: []string{"compile ..."}, CmdReal: 50 * time.Millisecond,
	})
	if a.Cached {
		t.Error("an action with a Cmd must not be reported as cached")
	}
	if !a.Ran {
		t.Error("an action with a Cmd must be reported as ran")
	}
	if a.WorkNs != int64(50*time.Millisecond) {
		t.Errorf("WorkNs = %d, want %d", a.WorkNs, int64(50*time.Millisecond))
	}
}

func TestNonNilEmptyCmdIsRan(t *testing.T) {
	a := New(actiongraph.Action{Mode: "build", Cmd: []string{}})
	if a.Cached {
		t.Error("a non-nil empty Cmd must not be reported as cached")
	}
	if !a.Ran {
		t.Error("a non-nil empty Cmd must be reported as ran")
	}
}

func TestCachedActionHasNoCmd(t *testing.T) {
	a := New(actiongraph.Action{
		Mode: "build", Package: "example.com/a", Cmd: nil,
	})
	if !a.Cached {
		t.Error("a build action with a nil Cmd must be reported as cached")
	}
	if a.Ran {
		t.Error("a cached action must not be reported as ran")
	}
}

// These three fields look like they answer "was it cached" and do not. The
// measured counts are identical on cold and warm builds. Pinned by test so a
// future refactor cannot quietly start trusting them.
func TestNeedBuildIsNotACacheSignal(t *testing.T) {
	a := New(actiongraph.Action{Mode: "build", NeedBuild: true, Cmd: nil})
	if !a.Cached {
		t.Error("NeedBuild must not override the Cmd==nil rule")
	}
}

func TestBuiltIsNotACacheSignal(t *testing.T) {
	a := New(actiongraph.Action{Mode: "link", Built: "/tmp/app", Cmd: nil})
	if !a.Cached {
		t.Error("Built must not override the Cmd==nil rule")
	}
}

func TestFailedIsNotACacheSignal(t *testing.T) {
	// Failed is always false in the final file, so it must not gate anything.
	a := New(actiongraph.Action{
		Mode: "build", Failed: false,
		Cmd: []string{"compile"}, CmdReal: time.Millisecond,
	})
	if a.Cached {
		t.Error("Failed==false must not make a ran action look cached")
	}
}

// unsafe produces NeedBuild:true, Cmd:nil, zero duration. It is neither cached
// nor rebuilt; counting it either way skews every report.
func TestBuiltinPackageIsNeitherCachedNorRan(t *testing.T) {
	a := New(actiongraph.Action{
		Mode: "built-in package", Package: "unsafe", NeedBuild: true,
	})
	if a.Cached {
		t.Error("built-in package must not count as cached")
	}
	if a.Ran {
		t.Error("built-in package must not count as ran")
	}
}

// Bookkeeping actions are not compilation and must not appear in cache stats.
func TestBookkeepingModesAreNeither(t *testing.T) {
	for _, mode := range []string{"nop", "go build", "go test", "test barrier", "build check cache"} {
		a := New(actiongraph.Action{Mode: mode})
		if a.Cached || a.Ran {
			t.Errorf("mode %q: Cached=%v Ran=%v, want both false", mode, a.Cached, a.Ran)
		}
	}
}

func TestQueueWait(t *testing.T) {
	a := New(actiongraph.Action{
		Mode:      "build",
		TimeReady: at(0, 0),
		TimeStart: at(0, int(200*time.Millisecond)),
		TimeDone:  at(1, 0),
		Cmd:       []string{"compile"}, CmdReal: 800 * time.Millisecond,
	})
	if a.QueueNs != int64(200*time.Millisecond) {
		t.Errorf("QueueNs = %d, want %d", a.QueueNs, int64(200*time.Millisecond))
	}
	if a.WallNs != int64(800*time.Millisecond) {
		t.Errorf("WallNs = %d, want %d", a.WallNs, int64(800*time.Millisecond))
	}
}

func TestQueueWaitNeverNegative(t *testing.T) {
	// Clock skew or a zero TimeReady must not produce a negative wait.
	a := New(actiongraph.Action{
		Mode:      "build",
		TimeStart: at(0, 0),
		TimeDone:  at(1, 0),
		Cmd:       []string{"compile"},
	})
	if a.QueueNs < 0 {
		t.Errorf("QueueNs = %d, want >= 0", a.QueueNs)
	}
}

func TestKindClassification(t *testing.T) {
	cases := map[string]Kind{
		"build":             KindCompile,
		"link":              KindLink,
		"link-install":      KindLink,
		"build check cache": KindCacheProbe,
		"vet":               KindVet,
		"nop":               KindOther,
		"something new":     KindOther,
	}
	for mode, want := range cases {
		if got := New(actiongraph.Action{Mode: mode}).Kind; got != want {
			t.Errorf("mode %q: Kind = %v, want %v", mode, got, want)
		}
	}
}
