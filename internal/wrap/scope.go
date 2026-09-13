package wrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

// CurrentScope derives the scope for the process's working directory, or the
// directory selected by a wrapped go command's leading -C flag. A missing
// module is not an error: builds outside a module are still builds.
func CurrentScope(ctx context.Context, commands ...[]string) string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "unknown"
	}
	if len(commands) != 0 {
		dir = commandDir(commands[0], dir)
	}
	return Scope(modulePath(ctx, dir), dir)
}

func commandDir(argv []string, dir string) string {
	for i := 1; i < len(argv); {
		var next string
		switch {
		case argv[i] == "-C" && i+1 < len(argv):
			next = argv[i+1]
			i += 2
		case strings.HasPrefix(argv[i], "-C="):
			next = strings.TrimPrefix(argv[i], "-C=")
			i++
		default:
			return dir
		}
		if next == "" {
			return dir
		}
		if filepath.IsAbs(next) {
			dir = next
		} else {
			dir = filepath.Join(dir, next)
		}
		dir = filepath.Clean(dir)
	}
	return dir
}

// modulePath asks the go command for the current module, returning "" when
// there is not one.
func modulePath(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, moduleLookupTimeout)
	defer cancel()

	// GOFLAGS may contain list-only flags that change output or conflict with
	// -m. Command-line overrides keep the scope stable without discarding
	// module-selection flags such as -mod or -modfile.
	cmd := exec.CommandContext(ctx, "go", "list",
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
	)
	if dir != "" && dir != "unknown" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
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
