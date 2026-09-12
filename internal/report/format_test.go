package report

import "testing"

func TestDur(t *testing.T) {
	tests := []struct {
		name string
		ns   int64
		want string
	}{
		{"zero", 0, "0s"},
		{"sub-millisecond rounds to zero", 400_000, "0.00s"},
		{"milliseconds", 35_000_000, "0.04s"},
		{"seconds", 2_046_000_000, "2.05s"},
		{"over a minute", 125_000_000_000, "2m05.00s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Dur(tt.ns); got != tt.want {
				t.Errorf("Dur(%d) = %q, want %q", tt.ns, got, tt.want)
			}
		})
	}
}

func TestPct(t *testing.T) {
	tests := []struct {
		name        string
		part, whole int64
		want        string
	}{
		{"quarter", 1, 4, "25%"},
		{"zero whole does not divide by zero", 0, 0, "0%"},
		{"all", 3, 3, "100%"},
		{"rounds", 1, 3, "33%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Pct(tt.part, tt.whole); got != tt.want {
				t.Errorf("Pct(%d, %d) = %q, want %q", tt.part, tt.whole, got, tt.want)
			}
		})
	}
}
