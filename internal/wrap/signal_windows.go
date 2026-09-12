//go:build windows

package wrap

import (
	"os"
	"os/signal"
)

func forwardInterrupts(*os.Process) func() {
	// Windows sends Ctrl-C to every process sharing the console. Calling
	// Process.Signal(os.Interrupt) is unsupported, so keep longpole alive while
	// the child receives the console event directly.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	return func() { signal.Stop(sigs) }
}
