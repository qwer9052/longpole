package wrap

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// The action graph record shape was verified byte-for-byte identical from Go
// 1.21 through 1.27. Outside that window longpole is guessing, so it says so.
const (
	minMinor       = 21
	maxTestedMinor = 27
)

// CheckGoVersion rejects toolchains too old to have the flag in a known shape.
// Newer toolchains are allowed: a warning is better than refusing to run.
func CheckGoVersion(v string) error {
	minor, ok := parseMinor(v)
	if !ok {
		return nil
	}
	if minor < minMinor {
		return fmt.Errorf("longpole needs Go %d.%d or newer; this is %s", 1, minMinor, v)
	}
	return nil
}

// UnverifiedGoVersion reports whether the toolchain is newer than any longpole
// has been tested against.
func UnverifiedGoVersion(v string) bool {
	minor, ok := parseMinor(v)
	return ok && minor > maxTestedMinor
}

// CommandVersion asks the executable that will actually run the build for its
// version. This accounts for PATH differences and GOTOOLCHAIN selection.
func CommandVersion(ctx context.Context, goPath string, dirs ...string) (string, error) {
	cmd := exec.CommandContext(ctx, goPath, "version")
	if len(dirs) > 0 && dirs[0] != "" {
		cmd.Dir = dirs[0]
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run %s version: %w", goPath, err)
	}
	for _, field := range strings.Fields(string(out)) {
		if strings.HasPrefix(field, "go1.") {
			return field, nil
		}
	}
	return "", fmt.Errorf("parse %s version output %q", goPath, strings.TrimSpace(string(out)))
}

// parseMinor extracts 27 from "go1.27.1". It returns false for anything that
// does not look like a release version, including devel builds.
func parseMinor(v string) (int, bool) {
	v = strings.TrimPrefix(v, "go")
	parts := strings.Split(v, ".")
	if len(parts) < 2 || parts[0] != "1" {
		return 0, false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	return n, true
}
