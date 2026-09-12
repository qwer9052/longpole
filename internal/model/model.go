// Package model turns raw action-graph records into the values longpole
// reports on. It is the only place that decides whether an action was cached.
package model

import (
	"time"

	"github.com/qwer9052/longpole/internal/actiongraph"
)

// Kind groups action modes into the categories a report shows.
type Kind int

const (
	KindOther Kind = iota
	KindCompile
	KindLink
	KindCacheProbe
	KindVet
)

func (k Kind) String() string {
	switch k {
	case KindCompile:
		return "compile"
	case KindLink:
		return "link"
	case KindCacheProbe:
		return "cache"
	case KindVet:
		return "vet"
	default:
		return "other"
	}
}

// Action is one action with its derived measurements.
type Action struct {
	ID      int
	Mode    string
	Kind    Kind
	Package string
	Deps    []int

	ActionID string
	BuildID  string

	// WorkNs is time actually spent in subprocesses. It is the honest measure
	// of cost. WallNs includes scheduling and bookkeeping; QueueNs is time the
	// action sat ready but unscheduled.
	WorkNs  int64
	WallNs  int64
	QueueNs int64

	// Cached and Ran are mutually exclusive, and both are false for actions
	// that are neither compilation nor linking.
	Cached bool
	Ran    bool
}

// classify maps a mode string to a Kind. Unknown modes become KindOther rather
// than an error: the go command has added modes before and will again.
func classify(mode string) Kind {
	switch mode {
	case "build":
		return KindCompile
	case "link", "link-install":
		return KindLink
	case "build check cache":
		return KindCacheProbe
	case "vet":
		return KindVet
	default:
		return KindOther
	}
}

// New derives an Action from a raw record.
//
// The cache rule: an action is cached when it is real work (a compile or a
// link) and no subprocess ran for it. There is no "cached" field in the graph,
// and NeedBuild, Built and Failed all look like one but are not — they are
// frozen before execution and measure identically on cold and warm builds.
// See the design spec for the measurements.
func New(raw actiongraph.Action) Action {
	kind := classify(raw.Mode)
	isWork := kind == KindCompile || kind == KindLink
	ran := isWork && raw.Cmd != nil

	a := Action{
		ID:       raw.ID,
		Mode:     raw.Mode,
		Kind:     kind,
		Package:  raw.Package,
		Deps:     raw.Deps,
		ActionID: raw.ActionID,
		BuildID:  raw.BuildID,
		WorkNs:   int64(raw.CmdReal),
		Cached:   isWork && !ran,
		Ran:      ran,
	}

	if !raw.TimeDone.IsZero() && !raw.TimeStart.IsZero() {
		a.WallNs = raw.TimeDone.Sub(raw.TimeStart).Nanoseconds()
	}
	if !raw.TimeStart.IsZero() && !raw.TimeReady.IsZero() {
		if q := raw.TimeStart.Sub(raw.TimeReady).Nanoseconds(); q > 0 {
			a.QueueNs = q
		}
	}
	return a
}

// NewAll derives every action in a graph, preserving order so that Deps
// indexes stay valid.
func NewAll(raw []actiongraph.Action) []Action {
	out := make([]Action, len(raw))
	for i, r := range raw {
		out[i] = New(r)
	}
	return out
}

// Span returns the wall-clock window the whole build occupied, computed from
// the earliest TimeReady to the latest TimeDone. The go command does not record
// its own start time in the graph, so this slightly understates total wall time.
func Span(raw []actiongraph.Action) time.Duration {
	var first, last time.Time
	for _, r := range raw {
		if !r.TimeReady.IsZero() && (first.IsZero() || r.TimeReady.Before(first)) {
			first = r.TimeReady
		}
		if r.TimeDone.After(last) {
			last = r.TimeDone
		}
	}
	if first.IsZero() || last.IsZero() {
		return 0
	}
	return last.Sub(first)
}
