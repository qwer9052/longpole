//go:build !unix

package wrap

import "os"

func processExitCode(state *os.ProcessState) int {
	return state.ExitCode()
}
