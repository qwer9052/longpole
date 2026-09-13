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
		if commandDir, err := commandDir(commands[0], dir); err == nil {
			dir = commandDir
		}
	}
	return Scope(modulePath(ctx, dir), dir)
}

// CommandDir returns the directory in which a wrapped go command will run.
func CommandDir(argv []string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return commandDir(argv, dir)
}

func commandDir(argv []string, dir string) (string, error) {
	_, dirs, err := commandParts(argv)
	if err != nil {
		return dir, err
	}
	for _, next := range dirs {
		if filepath.IsAbs(next) {
			dir = next
		} else {
			dir = filepath.Join(dir, next)
		}
		dir = filepath.Clean(dir)
	}
	return dir, nil
}

// ResolvePath maps a user-supplied relative path into the wrapped command's
// effective directory without changing the user's argv.
func ResolvePath(argv []string, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	dir, err := CommandDir(argv)
	if err != nil {
		return path
	}
	return filepath.Join(dir, path)
}

// modulePath asks the go command for the current module, returning "" when
// there is not one.
func modulePath(ctx context.Context, dir string) string {
	if m := modulePathFromFile(dir); m != "" {
		return m
	}
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

func modulePathFromFile(dir string) string {
	for dir != "" {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == "module" {
					return fields[1]
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
