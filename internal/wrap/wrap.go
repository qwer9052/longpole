// Package wrap runs the user's go command with an action graph flag injected,
// passing stdio and the exit code through unchanged.
//
// The guiding rule: longpole must never be the reason a build behaves
// differently. If anything here fails, the build's own result still wins.
package wrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	if path, ok := ExistingGraphPath(argv); ok {
		return argv, path
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

// ExistingGraphPath returns a user-supplied action graph path. Arguments after
// -args or -- belong to the built program or test binary, not to the go command.
func ExistingGraphPath(argv []string) (string, bool) {
	for i := 2; i < len(argv); i++ {
		a := argv[i]
		if a == "-args" || a == "--" {
			break
		}
		if strings.HasPrefix(a, flagName+"=") {
			return strings.TrimPrefix(a, flagName+"="), true
		}
		if a == flagName && i+1 < len(argv) {
			return argv[i+1], true
		}
	}
	return "", false
}

// Result is what the wrapped command produced.
type Result struct {
	ExitCode int
	WallNs   int64
	// WaitErr reports a non-fatal stdio-copy failure after ProcessState made the
	// child's exit code authoritative.
	WaitErr error
}

// Run executes argv with stdio connected to this process. Interrupt handling is
// platform-specific: it forwards signals where os.Process supports that, while
// Windows relies on the console delivering Ctrl-C to both processes. It returns
// the child's exit code rather than an error for a non-zero exit: a failing
// build is a normal outcome, not a longpole failure.
func Run(ctx context.Context, argv []string, extraEnv []string, stderr *StderrTee) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("no command to run")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
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

	stopForwarding := forwardInterrupts(cmd.Process)
	err := cmd.Wait()
	stopForwarding()
	wall := time.Since(start).Nanoseconds()

	res := Result{WallNs: wall}
	if cmd.ProcessState == nil {
		if err != nil {
			return res, fmt.Errorf("wait %s: %w", argv[0], err)
		}
		return res, fmt.Errorf("wait %s: process state unavailable", argv[0])
	}
	res.ExitCode = cmd.ProcessState.ExitCode()
	if err != nil {
		var ee *exec.ExitError
		if errorsAs(err, &ee) {
			return res, nil
		}
		res.WaitErr = fmt.Errorf("wait %s: %w", argv[0], err)
	}
	return res, nil
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
