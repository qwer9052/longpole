//go:build !windows

package wrap

import (
	"os"
	"os/signal"
)

func forwardInterrupts(process *os.Process) func() {
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigs, os.Interrupt)
	go func() {
		for {
			select {
			case sig := <-sigs:
				_ = process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}
