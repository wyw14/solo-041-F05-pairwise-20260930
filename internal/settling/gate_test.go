package settling_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wyw14/cry-97/internal/settling"
)

func TestDecantDelays(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	stable := now.Add(-settling.MinBlanketStable - time.Minute)
	recent := now.Add(-time.Minute)

	tests := []struct {
		name     string
		snapshot settling.Snapshot
		alarms   int
		want     []string
	}{
		{
			name:     "ready",
			snapshot: settling.Snapshot{CycleSamples: settling.MinCycleBlanketSamples, StableSince: &stable},
			want:     nil,
		},
		{
			name:     "interface high",
			snapshot: settling.Snapshot{BlanketLevel: 3.1, High: true, CycleSamples: settling.MinCycleBlanketSamples, StableSince: &stable},
			want:     []string{"high threshold"},
		},
		{
			name:     "sampling insufficient",
			snapshot: settling.Snapshot{CycleSamples: 1, StableSince: &stable},
			want:     []string{"samples this cycle 1/2"},
		},
		{
			name:     "no measurement this cycle skips stability",
			snapshot: settling.Snapshot{},
			want:     []string{"samples this cycle 0/2"},
		},
		{
			name:     "stability too short",
			snapshot: settling.Snapshot{CycleSamples: settling.MinCycleBlanketSamples, StableSince: &recent},
			want:     []string{"blanket stable for"},
		},
		{
			name:     "active alarms",
			snapshot: settling.Snapshot{CycleSamples: settling.MinCycleBlanketSamples, StableSince: &stable},
			alarms:   2,
			want:     []string{"2 active alarm(s)"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reasons := settling.DecantDelays(test.snapshot, test.alarms, now)
			if len(test.want) == 0 {
				if len(reasons) != 0 {
					t.Fatalf("expected no delays, got %v", reasons)
				}
				return
			}
			if len(reasons) != len(test.want) {
				t.Fatalf("expected %d delays, got %v", len(test.want), reasons)
			}
			for i, want := range test.want {
				if !strings.Contains(reasons[i], want) {
					t.Fatalf("reason %d should contain %q, got %q", i, want, reasons[i])
				}
			}
		})
	}
}
