package wrap

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

const moduleLookupTimeout = 5 * time.Second

// Scope identifies a project so that run history and diffs never mix unrelated
// builds. It is the module path plus the working directory, because the same
// module can be checked out more than once and the build cache keys on the
// absolute directory too.
func Scope(module, dir string) string {
	return module + "@" + strings.ReplaceAll(dir, `\`, "/")
}

// CurrentScope derives the scope for the process's working directory. A
// missing module is not an error: builds outside a module are still builds.
func CurrentScope(ctx context.Context) string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "unknown"
	}
	return Scope(modulePath(ctx), dir)
}

// modulePath asks the go command for the current module, returning "" when
// there is not one.
func modulePath(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, moduleLookupTimeout)
	defer cancel()

	// GOFLAGS may contain list-only flags that change output or conflict with
	// -m. Command-line overrides keep the scope stable without discarding
	// module-selection flags such as -mod or -modfile.
	out, err := exec.CommandContext(ctx, "go", "list",
		"-m",
		"-json=false",
		"-f={{.Path}}",
		"-deps=false",
		"-test=false",
		"-export=false",
		"-compiled=false",
		"-find=false",
		"-reuse=",
		"-u=false",
		"-versions=false",
		"-retracted=false",
	).Output()
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
