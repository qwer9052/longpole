package report

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/qwer9052/longpole/internal/model"
)

// minGateRegressionNs keeps tiny builds from failing on noise: a 0.3s build
// that takes 0.4s is 33% slower and means nothing.
const minGateRegressionNs = 1_000_000_000

// GateInput is what a regression check compares.
type GateInput struct {
	BeforeID, AfterID     int64
	BeforeWork, AfterWork int64
	Before, After         []model.Action
	LimitPct              float64
	TopN                  int
}

// Gate reports whether the later run's work time grew past the limit. It
// compares work time rather than wall time because wall time also measures how
// busy the machine was, which a CI runner does not control.
func Gate(in GateInput) (string, bool) {
	var b strings.Builder
	delta := in.AfterWork - in.BeforeWork
	// An all-cached baseline did no work, so any real work is an unbounded
	// increase: usually the build cache was lost.
	pct := math.Inf(1)
	if in.BeforeWork > 0 {
		pct = float64(delta) * 100 / float64(in.BeforeWork)
	} else if delta <= 0 {
		pct = 0
	}
	failed := delta >= minGateRegressionNs && pct > in.LimitPct

	sign := "+"
	shown := delta
	if delta < 0 {
		sign, shown = "-", -delta
	}
	verdict := "within"
	if failed {
		verdict = "over"
	}
	change := fmt.Sprintf("%+.0f%%", pct)
	if math.IsInf(pct, 1) {
		change = "from no work"
	}
	fmt.Fprintf(&b, "  gate  work %s -> %s (%s%s, %s), %s the %g%% limit\n",
		Dur(in.BeforeWork), Dur(in.AfterWork), sign, Dur(shown), change, verdict, in.LimitPct)
	if !failed {
		return b.String(), false
	}

	slower := slowerActions(in.Before, in.After)
	if len(slower) > 0 {
		b.WriteString("\n  ran both times, slower this time\n")
		n := in.TopN
		if n > len(slower) {
			n = len(slower)
		}
		for _, s := range slower[:n] {
			fmt.Fprintf(&b, "    %7s  %s  (%s -> %s)\n",
				"+"+Dur(s.after-s.before), s.name, Dur(s.before), Dur(s.after))
		}
		if rest := len(slower) - n; rest > 0 {
			fmt.Fprintf(&b, "    + %d more\n", rest)
		}
	}
	b.WriteString("\n")
	return b.String(), true
}

type slowdown struct {
	name          string
	before, after int64
}

// slowerActions pairs actions that ran in both builds and got slower. Newly
// run actions are already listed by Diff. Keys that appear more than once on
// either side (test variants) are skipped: pairing them would be a guess.
func slowerActions(before, after []model.Action) []slowdown {
	type entry struct {
		a     model.Action
		count int
	}
	index := func(acts []model.Action) map[actionKey]*entry {
		m := make(map[actionKey]*entry)
		for _, a := range acts {
			if !isWorkAction(a) {
				continue
			}
			k := keyFor(a)
			if e, ok := m[k]; ok {
				e.count++
				continue
			}
			m[k] = &entry{a: a, count: 1}
		}
		return m
	}
	prev, cur := index(before), index(after)

	var out []slowdown
	for k, c := range cur {
		p, ok := prev[k]
		if !ok || c.count != 1 || p.count != 1 || !c.a.Ran || !p.a.Ran {
			continue
		}
		if c.a.WorkNs > p.a.WorkNs {
			out = append(out, slowdown{name: pkgName(c.a), before: p.a.WorkNs, after: c.a.WorkNs})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i].after-out[i].before, out[j].after-out[j].before
		if di != dj {
			return di > dj
		}
		return out[i].name < out[j].name
	})
	return out
}
