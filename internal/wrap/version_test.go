package wrap

import "testing"

func TestSupportedVersions(t *testing.T) {
	for _, v := range []string{"go1.21", "go1.24.0", "go1.27.1"} {
		if err := CheckGoVersion(v); err != nil {
			t.Errorf("%s should be supported: %v", v, err)
		}
	}
}

func TestTooOldIsRejected(t *testing.T) {
	if err := CheckGoVersion("go1.20.5"); err == nil {
		t.Error("Go 1.20 should be rejected: the graph shape is unverified there")
	}
}

func TestNewerIsAllowedWithAWarning(t *testing.T) {
	err := CheckGoVersion("go1.30")
	if err != nil {
		t.Errorf("a newer Go must still be attempted: %v", err)
	}
	if !UnverifiedGoVersion("go1.30") {
		t.Error("a newer Go should be flagged as unverified")
	}
	if UnverifiedGoVersion("go1.27.1") {
		t.Error("a tested version must not be flagged")
	}
}

func TestUnparseableVersionIsAllowed(t *testing.T) {
	// Devel builds and vendor toolchains have odd version strings. Refusing to
	// run because a string did not parse would be worse than trying.
	if err := CheckGoVersion("devel +abc123"); err != nil {
		t.Errorf("an unparseable version must not block the build: %v", err)
	}
}
