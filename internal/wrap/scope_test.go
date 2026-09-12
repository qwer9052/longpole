package wrap

import "testing"

func TestScopeCombinesModuleAndDir(t *testing.T) {
	got := Scope("example.com/app", "/home/x/app")
	if got != "example.com/app@/home/x/app" {
		t.Errorf("got %q", got)
	}
}

func TestScopeFallsBackToDirWhenModuleUnknown(t *testing.T) {
	got := Scope("", "/home/x/app")
	if got != "@/home/x/app" {
		t.Errorf("got %q", got)
	}
}

func TestScopeNormalizesWindowsSeparators(t *testing.T) {
	// The same directory must produce the same scope regardless of how the
	// path was spelled, or history silently splits in two.
	a := Scope("m", `C:\Users\x\app`)
	b := Scope("m", "C:/Users/x/app")
	if a != b {
		t.Errorf("%q != %q", a, b)
	}
}
