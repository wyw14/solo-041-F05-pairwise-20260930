package settling

import (
	"math"
	"testing"
	"time"

	"github.com/wyw14/cry-97/internal/model"
)

func TestUpdateBlanketCountsValidReadings(t *testing.T) {
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	state, err := NewState(model.LineID("line-1"), "settler-line-1", nil)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	if got := state.Snapshot(); got.SampleCount != 0 || got.HasValidReading() {
		t.Fatalf("fresh state should have no readings, got %+v", got)
	}

	first := state.UpdateBlanket(1.2, BlanketHighLevel, base)
	if first.SampleCount != 1 || !first.HasValidReading() || first.High {
		t.Fatalf("first reading count=%d high=%v", first.SampleCount, first.High)
	}
	if first.LastReadingAt == nil || !first.LastReadingAt.Equal(base) {
		t.Fatalf("last reading at = %v, want %v", first.LastReadingAt, base)
	}
	second := state.UpdateBlanket(1.4, BlanketHighLevel, base.Add(time.Minute))
	if second.SampleCount != 2 {
		t.Fatalf("second reading count = %d, want 2", second.SampleCount)
	}
}

func TestUpdateBlanketRejectsInvalidReadings(t *testing.T) {
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	state, _ := NewState(model.LineID("line-1"), "settler-line-1", nil)
	state.UpdateBlanket(1.0, BlanketHighLevel, base)

	for _, level := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.1} {
		snapshot := state.UpdateBlanket(level, BlanketHighLevel, base.Add(time.Minute))
		if snapshot.SampleCount != 1 {
			t.Fatalf("invalid level %v counted, sample_count = %d", level, snapshot.SampleCount)
		}
		if snapshot.BlanketLevel != 1.0 {
			t.Fatalf("invalid level %v overwrote blanket to %v", level, snapshot.BlanketLevel)
		}
	}
}

func TestBlanketStabilityTimer(t *testing.T) {
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	state, _ := NewState(model.LineID("line-1"), "settler-line-1", nil)

	state.UpdateBlanket(1.0, BlanketHighLevel, base)
	if got := state.Snapshot().StableFor(base.Add(5 * time.Minute)); got != 5*time.Minute {
		t.Fatalf("stable duration = %s, want 5m", got)
	}

	// 界面超高会打断稳定计时。
	high := state.UpdateBlanket(BlanketHighLevel, BlanketHighLevel, base.Add(10*time.Minute))
	if !high.High {
		t.Fatal("level at threshold should be flagged high")
	}
	if got := high.StableFor(base.Add(11 * time.Minute)); got != 0 {
		t.Fatalf("high blanket stable duration = %s, want 0", got)
	}

	// 回落后重新累计。
	state.UpdateBlanket(2.0, BlanketHighLevel, base.Add(12*time.Minute))
	if got := state.Snapshot().StableFor(base.Add(20 * time.Minute)); got != 8*time.Minute {
		t.Fatalf("recovered stable duration = %s, want 8m", got)
	}
}

func TestBeginCycleClearsPriorEvidence(t *testing.T) {
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	state, _ := NewState(model.LineID("line-1"), "settler-line-1", nil)
	state.UpdateBlanket(1.0, BlanketHighLevel, base)
	state.UpdateBlanket(1.1, BlanketHighLevel, base.Add(time.Minute))

	fresh := state.BeginCycle(base.Add(2 * time.Minute))
	if fresh.Cycle != 1 {
		t.Fatalf("cycle = %d, want 1", fresh.Cycle)
	}
	if fresh.SampleCount != 0 || fresh.HasValidReading() || fresh.High || fresh.BlanketLevel != 0 {
		t.Fatalf("prior blanket evidence not cleared: %+v", fresh)
	}
	if fresh.LastReadingAt != nil {
		t.Fatalf("last reading at = %v, want nil", fresh.LastReadingAt)
	}
	if got := fresh.StableFor(base.Add(3 * time.Minute)); got != 0 {
		t.Fatalf("stability carried across cycles: %s", got)
	}
}
