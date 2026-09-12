// Command longpole reports why a Go build was slow.
//
//	longpole go build ./...
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/qwer9052/longpole/internal/actiongraph"
	"github.com/qwer9052/longpole/internal/model"
	"github.com/qwer9052/longpole/internal/report"
	"github.com/qwer9052/longpole/internal/wrap"
)

const usage = `longpole — why was my Go build slow?

  longpole go build ./...      profile a build
  longpole go test ./...       profile a test build
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if args[0] != "go" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	os.Exit(runWrap(context.Background(), args))
}

// runWrap profiles a go command. Its contract: return the go command's exit
// code, whatever happens to the profiling.
func runWrap(ctx context.Context, argv []string) int {
	if _, err := wrap.Check(argv); err != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", err)
		return 2
	}

	cmdArgs := argv
	graphPath, userGraph := wrap.ExistingGraphPath(argv)
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

	res, err := wrap.Run(ctx, cmdArgs, nil, nil)
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
	if err := analyze(graphPath, res); err != nil {
		fmt.Fprintf(os.Stderr, "longpole: %v\n", err)
	}
	return res.ExitCode
}

func analyze(graphPath string, res wrap.Result) error {
	raw, err := actiongraph.ParseFile(graphPath)
	if err != nil {
		return fmt.Errorf("could not read the action graph: %w", err)
	}
	acts := model.NewAll(raw)
	s := model.Summarize(acts, res.WallNs)
	fmt.Fprint(os.Stderr, report.Run(s, acts, report.Options{
		TopN:     5,
		PathN:    5,
		Cores:    runtime.GOMAXPROCS(0),
		ExitCode: res.ExitCode,
	}))
	return nil
}
