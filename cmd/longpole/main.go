// Command longpole reports why a Go build was slow.
//
//	longpole go build ./...
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/qwer9052/longpole/internal/actiongraph"
	"github.com/qwer9052/longpole/internal/hashlog"
	"github.com/qwer9052/longpole/internal/model"
	"github.com/qwer9052/longpole/internal/report"
	"github.com/qwer9052/longpole/internal/store"
	"github.com/qwer9052/longpole/internal/wrap"
)

const usage = `longpole — why was my Go build slow?

  longpole go build ./...            profile a build
  longpole go test ./...             profile a test build
  longpole --explain go build ./...  also explain rebuild candidates (slower)
  longpole log                       list recent runs
  longpole diff [A B]                compare two runs (default: the last two)
`

// keepRuns bounds history per project. Fifty is enough to see a trend and small
// enough that the database stays trivial.
const keepRuns = 50

func main() {
	os.Exit(run(context.Background(), os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "--explain":
		if len(args) < 2 || args[1] != "go" {
			fmt.Fprint(os.Stderr, "usage: longpole --explain go build ./...\n")
			return 2
		}
		return runWrapExplain(ctx, args[1:])
	case "go":
		return runWrap(ctx, args)
	case "log":
		return runLog(ctx)
	case "diff":
		return runDiff(ctx, args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "longpole: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// runWrap profiles a go command. Its contract: return the go command's exit
// code, whatever happens to the profiling.
func runWrap(ctx context.Context, argv []string) int {
	return wrapWith(ctx, argv, false)
}

func runWrapExplain(ctx context.Context, argv []string) int {
	return wrapWith(ctx, argv, true)
}

func wrapWith(ctx context.Context, argv []string, explain bool) int {
	if _, err := wrap.Check(argv); err != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", err)
		return 2
	}
	if err := wrap.CheckGoVersion(runtime.Version()); err != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", err)
		return 2
	}
	if wrap.UnverifiedGoVersion(runtime.Version()) {
		fmt.Fprintf(os.Stderr,
			"longpole: %s is newer than any version this was tested against; "+
				"the report may be wrong\n", runtime.Version())
	}

	cmdArgs := argv
	graphPath, userGraph := wrap.ExistingGraphPath(argv)
	var graphBefore graphFileState
	var graphStatErr error
	if userGraph && graphPath != "" {
		graphPath = wrap.ResolvePath(argv, graphPath)
		graphBefore, graphStatErr = statGraph(graphPath)
	}
	var setupErr error
	if !userGraph {
		tmp, err := os.MkdirTemp("", "longpole-")
		if err != nil {
			setupErr = fmt.Errorf("create action graph temporary directory: %w", err)
		} else {
			defer os.RemoveAll(tmp)
			graphPath = filepath.Join(tmp, "actiongraph.json")
			cmdArgs, graphPath = wrap.Inject(argv, graphPath)
		}
	}

	var env []string
	var tee *wrap.StderrTee
	var hashes *hashlog.Collector
	if explain {
		godebug := "gocachehash=1"
		if prior := os.Getenv("GODEBUG"); prior != "" {
			godebug = prior + "," + godebug
		}
		env = append(env, "GODEBUG="+godebug)
		hashes = hashlog.New()
		tee = wrap.NewStderrTee(os.Stderr, func(line []byte) bool {
			if !hashlog.IsHashLine(line) {
				return false
			}
			hashes.Line(line)
			return true
		})
	}

	res, err := wrap.Run(ctx, cmdArgs, env, tee)
	if tee != nil {
		if flushErr := tee.Flush(); flushErr != nil {
			fmt.Fprintf(os.Stderr, "longpole: copy stderr from %s: %v\n", cmdArgs[0], flushErr)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", err)
		return 1
	}

	// From here on, nothing may change the exit code.
	if setupErr != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", setupErr)
		return res.ExitCode
	}
	if graphPath == "" {
		fmt.Fprintln(os.Stderr, "longpole: action graph path is empty; skipping analysis")
		return res.ExitCode
	}
	if res.WaitErr != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", res.WaitErr)
	}
	if userGraph {
		if graphStatErr != nil {
			fmt.Fprintf(os.Stderr, "longpole: %v; skipping analysis\n", graphStatErr)
			return res.ExitCode
		}
		graphAfter, err := statGraph(graphPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "longpole: %v; skipping analysis\n", err)
			return res.ExitCode
		}
		if !graphBefore.updatedBy(graphAfter) {
			fmt.Fprintf(os.Stderr, "longpole: action graph %q was not updated; skipping analysis\n", graphPath)
			return res.ExitCode
		}
	}
	var blocks map[string]hashlog.Block
	if hashes != nil {
		blocks = hashes.Blocks()
	}
	return finishRunWith(ctx, graphPath, argv, res, persist, os.Stderr, explain, blocks)
}

type persistFunc func(context.Context, []string, wrap.Result, model.Summary, []model.Action) (int64, int64, error)

func finishRun(ctx context.Context, graphPath string, argv []string, res wrap.Result, record persistFunc, stderr io.Writer) int {
	return finishRunWith(ctx, graphPath, argv, res, record, stderr, false, nil)
}

func finishRunWith(ctx context.Context, graphPath string, argv []string, res wrap.Result, record persistFunc, stderr io.Writer, explain bool, blocks map[string]hashlog.Block) int {
	if err := analyze(ctx, graphPath, argv, res, record, stderr, explain, blocks); err != nil {
		fmt.Fprintf(stderr, "longpole: %v\n", err)
	}
	return res.ExitCode
}

type graphFileState struct {
	exists  bool
	size    int64
	modTime time.Time
}

func statGraph(path string) (graphFileState, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return graphFileState{}, nil
	}
	if err != nil {
		return graphFileState{}, fmt.Errorf("stat action graph %q: %w", path, err)
	}
	return graphFileState{exists: true, size: info.Size(), modTime: info.ModTime()}, nil
}

func (before graphFileState) updatedBy(after graphFileState) bool {
	if !after.exists {
		return false
	}
	return !before.exists || before.size != after.size || !before.modTime.Equal(after.modTime)
}

func analyze(ctx context.Context, graphPath string, argv []string, res wrap.Result, record persistFunc, stderr io.Writer, explain bool, blocks map[string]hashlog.Block) error {
	raw, err := actiongraph.ParseFile(graphPath)
	if err != nil {
		return fmt.Errorf("could not read the action graph: %w", err)
	}
	acts := model.NewAll(raw)
	s := model.Summarize(acts, res.WallNs)
	opt := report.Options{
		TopN:       5,
		PathN:      5,
		Cores:      runtime.GOMAXPROCS(0),
		ExitCode:   res.ExitCode,
		ShowRoots:  explain,
		HashBlocks: blocks,
	}

	// Persistence is best-effort: a report the user can read matters more than
	// a row in a database they may never query.
	if id, prev, err := record(ctx, argv, res, s, acts); err != nil {
		fmt.Fprintf(stderr, "longpole: could not record this run: %v\n", err)
	} else {
		opt.RunID, opt.PrevID = id, prev
	}

	fmt.Fprint(stderr, report.Run(s, acts, opt))
	return nil
}

type runStore interface {
	Save(store.Run, []model.Action) (int64, error)
	Previous(string, int64) (int64, error)
	Prune(string, int) error
}

func persist(ctx context.Context, argv []string, res wrap.Result, s model.Summary, acts []model.Action) (id, prev int64, err error) {
	path, err := store.DefaultPath()
	if err != nil {
		return 0, 0, err
	}
	db, err := store.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			id, prev = 0, 0
			err = fmt.Errorf("close run store: %w", closeErr)
		}
	}()

	scope := wrap.CurrentScope(ctx, argv)
	return saveRun(db, store.Run{
		Scope:     scope,
		StartedAt: time.Now().UnixNano(),
		Command:   joinArgs(argv),
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		Cores:     runtime.GOMAXPROCS(0),
		WallNs:    s.WallNs,
		WorkNs:    s.WorkNs,
		Ran:       s.Ran,
		Cached:    s.Cached,
		ExitCode:  res.ExitCode,
	}, acts)
}

func saveRun(db runStore, r store.Run, acts []model.Action) (id, prev int64, err error) {
	id, err = db.Save(r, acts)
	if err != nil {
		return 0, 0, err
	}
	prev, err = db.Previous(r.Scope, id)
	if err != nil {
		return 0, 0, fmt.Errorf("look up previous run: %w", err)
	}
	if err := db.Prune(r.Scope, keepRuns); err != nil {
		return 0, 0, fmt.Errorf("prune run history: %w", err)
	}
	return id, prev, nil
}

func runLog(ctx context.Context) int {
	path, err := store.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", err)
		return 1
	}
	return runLogFrom(path, wrap.CurrentScope(ctx), os.Stdout, os.Stderr)
}

func runLogFrom(path, scope string, stdout, stderr io.Writer) (exitCode int) {
	db, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "longpole: %v\n", err)
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil && exitCode == 0 {
			fmt.Fprintf(stderr, "longpole: close run store: %v\n", err)
			exitCode = 1
		}
	}()

	runs, err := db.Recent(scope, 20)
	if err != nil {
		fmt.Fprintf(stderr, "longpole: %v\n", err)
		return 1
	}
	if len(runs) == 0 {
		fmt.Fprint(stdout, "\n  no runs recorded here yet — try:  longpole go build ./...\n\n")
		return 0
	}

	fmt.Fprintln(stdout)
	for _, r := range runs {
		when := time.Unix(0, r.StartedAt).Format("01-02 15:04")
		status := ""
		if r.ExitCode != 0 {
			status = "  failed"
		}
		fmt.Fprintf(stdout, "  #%-4d %s  %8s  %3d ran %3d cached  %s%s\n",
			r.ID, when, report.Dur(r.WallNs), r.Ran, r.Cached, r.Command, status)
	}
	fmt.Fprintln(stdout)
	return 0
}

// runDiff compares two runs. With no arguments it compares the two most recent.
func runDiff(ctx context.Context, args []string) int {
	path, err := store.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", err)
		return 1
	}
	return runDiffFrom(path, wrap.CurrentScope(ctx), args, os.Stdout, os.Stderr)
}

func runDiffFrom(path, scope string, args []string, stdout, stderr io.Writer) (exitCode int) {
	db, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "longpole: %v\n", err)
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil && exitCode == 0 {
			fmt.Fprintf(stderr, "longpole: close run store: %v\n", err)
			exitCode = 1
		}
	}()

	var beforeID, afterID int64
	switch len(args) {
	case 0:
		runs, err := db.Recent(scope, keepRuns)
		if err != nil {
			fmt.Fprintf(stderr, "longpole: %v\n", err)
			return 1
		}
		if len(runs) < 2 {
			fmt.Fprintf(stderr,
				"longpole: need two runs to compare, have %d — run a build again\n", len(runs))
			return 1
		}
		afterID = runs[0].ID
		for _, run := range runs[1:] {
			if run.Command == runs[0].Command {
				beforeID = run.ID
				break
			}
		}
		if beforeID == 0 {
			fmt.Fprintf(stderr,
				"longpole: need two runs of %q to compare — run that command again\n", runs[0].Command)
			return 1
		}
	case 2:
		beforeID, err = strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "longpole: %q is not a run number\n", args[0])
			return 2
		}
		afterID, err = strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "longpole: %q is not a run number\n", args[1])
			return 2
		}
	default:
		fmt.Fprint(stderr, "usage: longpole diff [BEFORE AFTER]\n")
		return 2
	}

	beforeRun, beforeActs, err := db.Load(beforeID)
	if err != nil {
		fmt.Fprintf(stderr, "longpole: %v\n", err)
		return 1
	}
	afterRun, afterActs, err := db.Load(afterID)
	if err != nil {
		fmt.Fprintf(stderr, "longpole: %v\n", err)
		return 1
	}
	if beforeRun.Scope != scope {
		fmt.Fprintf(stderr, "longpole: run #%d belongs to a different scope\n", beforeRun.ID)
		return 1
	}
	if afterRun.Scope != scope {
		fmt.Fprintf(stderr, "longpole: run #%d belongs to a different scope\n", afterRun.ID)
		return 1
	}
	if beforeRun.Command != afterRun.Command {
		fmt.Fprintf(stdout, "\n  note: comparing different commands: %q -> %q\n", beforeRun.Command, afterRun.Command)
	}

	fmt.Fprint(stdout, report.Diff(report.DiffInput{
		BeforeID: beforeRun.ID, AfterID: afterRun.ID,
		BeforeWallNs: beforeRun.WallNs, AfterWallNs: afterRun.WallNs,
		Before: beforeActs, After: afterActs,
		TopN: 10,
	}))
	return 0
}

func joinArgs(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
