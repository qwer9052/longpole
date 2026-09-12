//go:build windows

package wrap

import "testing"

func TestForwardInterruptsUsesSharedConsoleOnWindows(t *testing.T) {
	stop := forwardInterrupts(nil)
	stop()
}
