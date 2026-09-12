// Package wrap runs the user's go command with an action graph flag injected,
// passing stdio and the exit code through unchanged.
//
// The guiding rule: longpole must never be the reason a build behaves
// differently. If anything here fails, the build's own result still wins.
package wrap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"
)

const flagName = "-debug-actiongraph"

// buildingSubcommands are the go subcommands that compile something and
// therefore produce a useful action graph.
var buildingSubcommands = map[string]bool{
	"build":   true,
	"test":    true,
	"install": true,
	"vet":     true,
	"run":     true,
}

// Check validates that argv is a go command longpole can profile, and returns
// the subcommand.
func Check(argv []string) (string, error) {
	if len(argv) < 2 {
		return "", fmt.Errorf("usage: longpole go build ./...")
	}
	sub := argv[1]
	if !buildingSubcommands[sub] {
		return "", fmt.Errorf("`go %s` does not build anything, so there is nothing to profile", sub)
	}
	return sub, nil
}

// Inject places the action graph flag immediately after the go subcommand and
// returns the modified argv plus the graph path that will be written.
//
// If the caller already passed the flag, their argv is returned untouched and
// their path is used: the go command rejects a repeated flag, and silently
// overriding an explicit choice would be worse than not helping.
func Inject(argv []string, graphPath string) ([]string, string) {
	for i, a := range argv {
		if strings.HasPrefix(a, flagName+"=") {
			return argv, strings.TrimPrefix(a, flagName+"=")
		}
		if a == flagName && i+1 < len(argv) {
			return argv, argv[i+1]
		}
	}
	if len(argv) < 2 {
		return argv, graphPath
	}
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[0], argv[1])
	out = append(out, flagName+"="+graphPath)
	out = append(out, argv[2:]...)
	return out, graphPath
}

// Result is what the wrapped command produced.
type Result struct {
	ExitCode int
	WallNs   int64
}

// Run executes argv with stdio connected to this process and signals forwarded.
// It returns the child's exit code rather than an error for a non-zero exit:
// a failing build is a normal outcome, not a longpole failure.
func Run(argv []string, extraEnv []string, stderr *StderrTee) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("no command to run")
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start %s: %w", argv[0], err)
	}

	// Forward interrupts so Ctrl-C reaches the build, then let the child decide
	// when to exit. Killing it ourselves would lose the action graph.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()
	close(done)
	signal.Stop(sigs)
	wall := time.Since(start).Nanoseconds()

	res := Result{WallNs: wall}
	if err != nil {
		var ee *exec.ExitError
		if errorsAs(err, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("wait %s: %w", argv[0], err)
	}
	return res, nil
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
