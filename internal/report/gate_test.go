package report

import (
	"strings"
	"testing"

	"github.com/qwer9052/longpole/internal/model"
)

func TestGate(t *testing.T) {
	tests := []struct {
		name              string
		beforeNs, afterNs int64
		limit             float64
		wantFail          bool
	}{
		{"fails past the limit", 10_000_000_000, 13_000_000_000, 20, true},
		{"passes at the limit", 10_000_000_000, 12_000_000_000, 20, false},
		{"passes when faster", 10_000_000_000, 5_000_000_000, 20, false},
		{"ignores a tiny build's noise", 300_000_000, 900_000_000, 20, false},
		{"fails from an all-cached baseline", 0, 5_000_000_000, 20, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, failed := Gate(GateInput{BeforeWork: tt.beforeNs, AfterWork: tt.afterNs, LimitPct: tt.limit})
			if failed != tt.wantFail {
				t.Errorf("failed = %v, want %v", failed, tt.wantFail)
			}
		})
	}
}

func TestGateListsActionsThatGotSlower(t *testing.T) {
	before := []model.Action{
		{Package: "slow", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000},
		{Package: "same", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000},
		{Package: "cachedbefore", Mode: "build", Kind: model.KindCompile, Cached: true},
	}
	after := []model.Action{
		{Package: "slow", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 4_000_000_000},
		{Package: "same", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 1_000_000_000},
		{Package: "cachedbefore", Mode: "build", Kind: model.KindCompile, Ran: true, WorkNs: 2_000_000_000},
	}
	out, failed := Gate(GateInput{BeforeWork: 2_000_000_000, AfterWork: 7_000_000_000, Before: before, After: after, LimitPct: 20, TopN: 5})
	if !failed {
		t.Fatalf("gate should fail: %s", out)
	}
	if !strings.Contains(out, "slow  (1.00s -> 4.00s)") {
		t.Errorf("slower action should be listed with both times: %s", out)
	}
	if strings.Contains(out, "same") || strings.Contains(out, "cachedbefore") {
		t.Errorf("unchanged and newly run actions do not belong in the slower list: %s", out)
	}
}
