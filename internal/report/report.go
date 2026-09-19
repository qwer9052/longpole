package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qwer9052/longpole/internal/cause"
	"github.com/qwer9052/longpole/internal/critpath"
	"github.com/qwer9052/longpole/internal/hashlog"
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
	// ShowRoots prints the conservative rebuild-candidate frontier collected
	// under --explain. HashBlocks supplies the corresponding hash evidence.
	ShowRoots  bool
	HashBlocks map[string]hashlog.Block
}

// Run renders the single-build report.
func Run(s model.Summary, acts []model.Action, opt Options) string {
	var b strings.Builder

	if s.Actions == 0 {
		b.WriteString("\n")
		if opt.ExitCode != 0 {
			fmt.Fprintf(&b, "  build failed (exit %d)\n", opt.ExitCode)
		}
		b.WriteString("  no build actions recorded\n")
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
		writeLoading(&b, s)
		probe := s.ByKind[model.KindCacheProbe]
		if probe.Count > 0 {
			fmt.Fprintf(&b, "  (%s summed cache-probe spans across %d %s; spans may overlap)\n",
				Dur(probe.WallNs), probe.Count, actionWord(probe.Count))
		}
		return b.String()
	}

	writeKinds(&b, s, acts)
	writeLoading(&b, s)
	writeCriticalPath(&b, acts, s, opt)
	writeSuggestions(&b, acts, s)
	writeParallelism(&b, s, opt)
	writeBiggest(&b, acts, opt)
	if opt.ShowRoots {
		writeRoots(&b, acts, opt.HashBlocks)
	}
	writeFooter(&b, opt)

	return b.String()
}

func writeSuggestions(b *strings.Builder, acts []model.Action, s model.Summary) {
	path, total := critpath.Find(acts)
	var top model.Action
	for _, a := range path {
		if a.WorkNs > top.WorkNs {
			top = a
		}
	}
	if total > 0 && top.Kind == model.KindCompile && top.Package != "" &&
		top.WorkNs >= 1_000_000_000 && top.WorkNs*2 >= total && top.WorkNs*5 >= s.WallNs {
		fmt.Fprintf(b, "\n  worth a look\n    %s is %s of the critical path; consider splitting this build step\n",
			pkgName(top), Pct(top.WorkNs, total))
	}

	links, linkWork := 0, int64(0)
	for _, a := range acts {
		if a.Kind == model.KindLink && a.Ran && a.WorkNs > 0 {
			links++
			linkWork += a.WorkNs
		}
	}
	if links >= 2 && linkWork >= 1_000_000_000 {
		fmt.Fprintf(b, "\n  worth a look\n    %d binaries were re-linked (%s); build only the binaries you need when possible\n",
			links, Dur(linkWork))
	}
}

func writeKinds(b *strings.Builder, s model.Summary, acts []model.Action) {
	b.WriteString("\n  time went to\n")
	kinds := []model.Kind{model.KindCompile, model.KindLink, model.KindVet, model.KindTest, model.KindCacheProbe}
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
		if k == model.KindTest {
			if st.WallNs == 0 {
				continue
			}
			// The action graph omits CmdReal for test runs. Their wall spans are
			// still the only honest measurement of time spent in test binaries.
			fmt.Fprintf(b, "    %-9s %7s  span sum   %d %s (test execution; may overlap)\n",
				k.String(), Dur(st.WallNs), st.Count, actionWord(st.Count))
			continue
		}
		amount := st.WorkNs
		if amount == 0 {
			continue
		}
		count := st.Count
		label := fmt.Sprintf("%d %s", count, actionWord(count))
		if k == model.KindCompile || k == model.KindLink {
			ran := 0
			for _, a := range acts {
				if a.Kind == k && a.Ran {
					ran++
				}
			}
			label = fmt.Sprintf("%d of %d %s", ran, count, actionWord(count))
		}
		fmt.Fprintf(b, "    %-9s %7s  %4s   %s\n",
			k.String(), Dur(amount), Pct(amount, s.WorkNs), label)
	}
}

func writeCriticalPath(b *strings.Builder, acts []model.Action, s model.Summary, opt Options) {
	path, total := critpath.Find(acts)
	if total == 0 {
		return
	}
	if s.ByKind[model.KindTest].WallNs > 0 {
		fmt.Fprintf(b, "\n  build critical path  %s of %s total wall (%s; includes test execution)\n",
			Dur(total), Dur(s.WallNs), Pct(total, s.WallNs))
	} else {
		fmt.Fprintf(b, "\n  critical path  %s of %s wall (%s)\n",
			Dur(total), Dur(s.WallNs), Pct(total, s.WallNs))
	}

	// A zero-cost cached wrapper can still be part of the dependency chain, but
	// showing it under a cost heading would imply it did work.
	shown := make([]model.Action, 0, len(path))
	for _, a := range path {
		if a.WorkNs > 0 && (a.Ran || a.Kind == model.KindVet) {
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
	if s.ByKind[model.KindTest].WallNs > 0 {
		fmt.Fprintf(b, "\n  build parallelism  %.1fx of %d cores  (build work %s / total wall %s; includes test execution)\n",
			s.Parallelism, opt.Cores, Dur(s.WorkNs), Dur(s.WallNs))
	} else {
		fmt.Fprintf(b, "\n  parallelism  %.1fx of %d cores  (work %s / wall %s)\n",
			s.Parallelism, opt.Cores, Dur(s.WorkNs), Dur(s.WallNs))
	}
	if s.QueueHeavy > 0 {
		fmt.Fprintf(b, "    ! %s summed queue wait; %d %s waited over 50ms\n",
			Dur(s.QueueNs), s.QueueHeavy, actionWord(s.QueueHeavy))
	}
	if s.Parallelism > 1.5 {
		b.WriteString("    subprocess wall includes CPU contention from parallel actions\n")
	}
}

func writeLoading(b *strings.Builder, s model.Summary) {
	if s.WallNs <= 0 || s.ActionSpanNs <= 0 || s.ActionSpanNs >= s.WallNs {
		return
	}
	fmt.Fprintf(b, "\n  go command ≈%s outside the action graph (loading, startup and exit; estimated)\n",
		Dur(s.WallNs-s.ActionSpanNs))
}

func writeBiggest(b *strings.Builder, acts []model.Action, opt Options) {
	top := model.TopByWork(acts, opt.TopN)
	if len(top) == 0 {
		return
	}
	positions := make(map[int]int, len(acts))
	for i, a := range acts {
		positions[a.ID] = i
	}
	indexes := make([]int, 0, len(top))
	for _, a := range top {
		if i, ok := positions[a.ID]; ok {
			indexes = append(indexes, i)
		}
	}
	radius := critpath.BlastRadiusFor(acts, indexes)
	b.WriteString("\n  slowest packages\n")
	for _, a := range top {
		blocks := ""
		if i, ok := positions[a.ID]; ok && radius[i] > 0 {
			blocks = fmt.Sprintf(", blocks %d", radius[i])
		}
		fmt.Fprintf(b, "    %7s  %s%s\n", Dur(a.WorkNs), pkgName(a), blocks)
	}
}

func writeRoots(b *strings.Builder, acts []model.Action, blocks map[string]hashlog.Block) {
	roots := cause.Roots(acts, blocks)
	if len(roots) == 0 {
		return
	}
	b.WriteString("\n  why it rebuilt (candidate roots)\n")
	n := len(roots)
	if n > 3 {
		n = 3
	}
	for _, root := range roots[:n] {
		if root.Package == "" {
			continue
		}
		if root.Downstream > 0 {
			fmt.Fprintf(b, "    %s  (candidate root; %d %s followed)\n",
				root.Package, root.Downstream, actionWord(root.Downstream))
			continue
		}
		fmt.Fprintf(b, "    %s  (candidate root)\n", root.Package)
	}
	if rest := len(roots) - n; rest > 0 {
		fmt.Fprintf(b, "    + %d more candidate roots\n", rest)
	}
}

func writeFooter(b *strings.Builder, opt Options) {
	if opt.RunID == 0 {
		return
	}
	if opt.PrevID > 0 {
		fmt.Fprintf(b, "\n  saved as run #%d.  compare:  longpole diff %d %d\n",
			opt.RunID, opt.PrevID, opt.RunID)
		return
	}
	fmt.Fprintf(b, "\n  saved as run #%d\n", opt.RunID)
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
	if a.Kind == model.KindVet {
		return a.Package + " (vet)"
	}
	return a.Package
}

func actionWord(n int) string {
	if n == 1 {
		return "action"
	}
	return "actions"
}
