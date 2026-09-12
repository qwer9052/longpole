package wrap

import (
	"os"
	"os/exec"
	"strings"
)

// Scope identifies a project so that run history and diffs never mix unrelated
// builds. It is the module path plus the working directory, because the same
// module can be checked out more than once and the build cache keys on the
// absolute directory too.
func Scope(module, dir string) string {
	return module + "@" + strings.ReplaceAll(dir, `\`, "/")
}

// CurrentScope derives the scope for the process's working directory. A
// missing module is not an error: builds outside a module are still builds.
func CurrentScope() string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "unknown"
	}
	return Scope(modulePath(), dir)
}

// modulePath asks the go command for the current module, returning "" when
// there is not one.
func modulePath() string {
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return ""
	}
	m := strings.TrimSpace(string(out))
	// Outside a module the go command prints a diagnostic rather than a path.
	if m == "" || strings.Contains(m, " ") {
		return ""
	}
	return m
}
