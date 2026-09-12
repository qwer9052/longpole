package wrap

import (
	"context"
	"testing"
)

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

func TestModulePathIgnoresOutputFormattingGOFLAGS(t *testing.T) {
	tests := []struct {
		name    string
		goFlags string
	}{
		{name: "json", goFlags: "-json"},
		{name: "custom format", goFlags: "-f={{.Dir}}"},
		{name: "dependencies", goFlags: "-deps"},
		{name: "test variants", goFlags: "-test"},
		{name: "export data", goFlags: "-export"},
		{name: "compiled files", goFlags: "-compiled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOFLAGS", tt.goFlags)
			if got := modulePath(context.Background()); got != "github.com/qwer9052/longpole" {
				t.Errorf("module path = %q", got)
			}
		})
	}
}

func TestModulePathHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got := modulePath(ctx); got != "" {
		t.Errorf("module path = %q, want empty after cancellation", got)
	}
}
