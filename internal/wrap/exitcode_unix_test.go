//go:build unix

package wrap

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRunMapsSignalTerminationToShellExitCode(t *testing.T) {
	if os.Getenv("LONGPOLE_WRAP_TEST_SIGNAL") == "1" {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			os.Exit(99)
		}
		time.Sleep(time.Second)
		os.Exit(99)
	}

	res, err := Run(
		context.Background(),
		[]string{os.Args[0], "-test.run=^TestRunMapsSignalTerminationToShellExitCode$"},
		[]string{"LONGPOLE_WRAP_TEST_SIGNAL=1"},
		nil,
	)
	if err != nil {
		t.Fatalf("Run returned a fatal error after the child was signaled: %v", err)
	}
	want := 128 + int(syscall.SIGTERM)
	if res.ExitCode != want {
		t.Errorf("exit code = %d, want shell signal status %d", res.ExitCode, want)
	}
}
