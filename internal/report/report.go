package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qwer9052/longpole/internal/critpath"
	"github.com/qwer9052/longpole/internal/model"
)

// Options controls how much of the report is shown.
type Options struct {
	TopN     int // entries in the slowest-package list
	PathN    int // entries shown from the critical path
	Cores    int // GOMAXPROCS, for the parallelism line
	ExitCode int // the wrapped go command's exit code
	RunID    int64
	PrevID   int64
}

// Run renders the single-build report.
func Run(s model.Summary, acts []model.Action, opt Options) string {
	var b strings.Builder

	if s.Actions == 0 {
		b.WriteString("\n")
		if opt.ExitCode != 0 {
			fmt.Fprintf(&b, "  build failed (exit %d)\n", opt.ExitCode)
		}
		b.WriteString("  no build actions recorded\n\n")
		return b.String()
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "  build: %d %s, %d ran, %d cached (%s)        %s wall\n",
		s.Actions, actionWord(s.Actions), s.Ran, s.Cached,
		Pct(int64(s.Cached), int64(s.Ran+s.Cached)), Dur(s.WallNs))

	if opt.ExitCode != 0 {
		fmt.Fprintf(&b, "  build failed (exit %d) — showing what ran before the failure\n", opt.ExitCode)
	}

	// The headline case the incumbent gets wrong.
	if s.FullyCached {
		b.WriteString("\n  nothing to optimize — everything came from cache\n")
		probe := s.ByKind[model.KindCacheProbe]
		fmt.Fprintf(&b, "  (%s summed cache-probe spans across %d %s; spans may overlap)\n\n",
			Dur(probe.WallNs), probe.Count, actionWord(probe.Count))
		return b.String()
	}

	writeKinds(&b, s)
	writeCriticalPath(&b, acts, s, opt)
	writeParallelism(&b, s, opt)
	writeBiggest(&b, acts, opt)
	writeFooter(&b, opt)

	return b.String()
}

func writeKinds(b *strings.Builder, s model.Summary) {
	b.WriteString("\n  time went to\n")
	kinds := []model.Kind{model.KindCompile, model.KindLink, model.KindVet, model.KindCacheProbe}
	for _, k := range kinds {
		st, ok := s.ByKind[k]
		if !ok || st.Count == 0 {
			continue
		}
		if k == model.KindCacheProbe {
			if st.WallNs == 0 {
				continue
			}
			// Probe spans can overlap, so their sum is neither elapsed time nor a
			// share of the independent subprocess-work total.
			fmt.Fprintf(b, "    %-9s %7s  span sum   %d %s (may overlap)\n",
				k.String(), Dur(st.WallNs), st.Count, actionWord(st.Count))
			continue
		}
		amount := st.WorkNs
		if amount == 0 {
			continue
		}
		fmt.Fprintf(b, "    %-9s %7s  %4s   %d %s\n",
			k.String(), Dur(amount), Pct(amount, s.WorkNs), st.Count, actionWord(st.Count))
	}
}

func writeCriticalPath(b *strings.Builder, acts []model.Action, s model.Summary, opt Options) {
	path, total := critpath.Find(acts)
	if total == 0 {
		return
	}
	fmt.Fprintf(b, "\n  critical path  %s of %s wall (%s)\n",
		Dur(total), Dur(s.WallNs), Pct(total, s.WallNs))

	// A zero-cost cached wrapper can still be part of the dependency chain, but
	// showing it under a cost heading would imply it did work.
	shown := make([]model.Action, 0, len(path))
	for _, a := range path {
		if a.Ran && a.WorkNs > 0 {
			shown = append(shown, a)
		}
	}
	// Heaviest entries first: those are the ones worth acting on.
	sort.SliceStable(shown, func(i, j int) bool { return shown[i].WorkNs > shown[j].WorkNs })
	n := opt.PathN
	if n > len(shown) {
		n = len(shown)
	}
	for _, a := range shown[:n] {
		fmt.Fprintf(b, "    %7s  %s\n", Dur(a.WorkNs), pkgName(a))
	}
	if rest := len(shown) - n; rest > 0 {
		fmt.Fprintf(b, "    + %d more\n", rest)
	}
}

func writeParallelism(b *strings.Builder, s model.Summary, opt Options) {
	if s.Parallelism == 0 {
		return
	}
	fmt.Fprintf(b, "\n  parallelism  %.1fx of %d cores  (work %s / wall %s)\n",
		s.Parallelism, opt.Cores, Dur(s.WorkNs), Dur(s.WallNs))
	if s.QueueHeavy > 0 {
		fmt.Fprintf(b, "    ! %s queue wait across %d %s — the graph is narrow here\n",
			Dur(s.QueueNs), s.QueueHeavy, actionWord(s.QueueHeavy))
	}
}

func writeBiggest(b *strings.Builder, acts []model.Action, opt Options) {
	top := model.TopByWork(acts, opt.TopN)
	if len(top) == 0 {
		return
	}
	radius := critpath.BlastRadius(acts)
	b.WriteString("\n  slowest packages\n")
	for _, a := range top {
		blocks := ""
		if a.ID >= 0 && a.ID < len(radius) && radius[a.ID] > 0 {
			blocks = fmt.Sprintf(", blocks %d", radius[a.ID])
		}
		fmt.Fprintf(b, "    %7s  %s%s\n", Dur(a.WorkNs), pkgName(a), blocks)
	}
}

func writeFooter(b *strings.Builder, opt Options) {
	if opt.RunID == 0 {
		b.WriteString("\n")
		return
	}
	if opt.PrevID > 0 {
		fmt.Fprintf(b, "\n  saved as run #%d.  compare:  longpole diff %d %d\n\n",
			opt.RunID, opt.PrevID, opt.RunID)
		return
	}
	fmt.Fprintf(b, "\n  saved as run #%d\n\n", opt.RunID)
}

// pkgName renders an action's identity. Link actions share a package name with
// their compile action, so the mode disambiguates them.
func pkgName(a model.Action) string {
	if a.Package == "" {
		return a.Mode
	}
	if a.Kind == model.KindLink {
		return a.Package + " (link)"
	}
	return a.Package
}

func actionWord(n int) string {
	if n == 1 {
		return "action"
	}
	return "actions"
}
