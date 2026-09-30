package process_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wyw14/cry-97/internal/model"
	"github.com/wyw14/cry-97/internal/process"
	"github.com/wyw14/cry-97/internal/settling"
)

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time          { return c.now }
func (c *testClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestPlant(t *testing.T, clock *testClock) *process.Plant {
	t.Helper()
	plant, err := process.NewPlant(t.TempDir(), []model.LineID{"line-1"}, clock.Now)
	if err != nil {
		t.Fatalf("new plant: %v", err)
	}
	return plant
}

func hasReason(reasons []string, substr string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, substr) {
			return true
		}
	}
	return false
}

func lineStatus(t *testing.T, plant *process.Plant) process.SettlingLineStatus {
	t.Helper()
	for _, entry := range plant.SettlingStatus() {
		if entry.LineID == "line-1" {
			return entry
		}
	}
	t.Fatal("settling status for line-1 is not found")
	return process.SettlingLineStatus{}
}

func TestAdvanceSettlingDecantGate(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	plant := newTestPlant(t, clock)

	if _, err := plant.BeginSettling("line-1"); err != nil {
		t.Fatalf("begin settling: %v", err)
	}
	result, err := plant.AdvanceSettling("line-1")
	if err != nil || result.Blocked || result.Cycle.Phase != settling.PhaseSettle {
		t.Fatalf("advance fill to settle: %+v err=%v", result, err)
	}

	// No blanket evidence this cycle: decant is blocked and stays in settle.
	result, err = plant.AdvanceSettling("line-1")
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !result.Blocked || !hasReason(result.Reasons, "blanket samples this cycle 0/2") {
		t.Fatalf("expected sample shortage block, got %+v", result)
	}
	if result.Cycle.Phase != settling.PhaseSettle {
		t.Fatalf("blocked advance must stay in settle, got %s", result.Cycle.Phase)
	}

	// A high blanket reading raises an alarm; a later low reading clears the
	// interface but the alarm and stability evidence still block decant.
	if _, err := plant.UpdateBlanket("line-1", 3.1); err != nil {
		t.Fatalf("update blanket: %v", err)
	}
	if _, err := plant.UpdateBlanket("line-1", 2.0); err != nil {
		t.Fatalf("update blanket: %v", err)
	}
	result, err = plant.AdvanceSettling("line-1")
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !result.Blocked {
		t.Fatalf("expected blocked advance, got %+v", result)
	}
	if !hasReason(result.Reasons, "blanket stable for") || !hasReason(result.Reasons, "active alarm(s)") {
		t.Fatalf("expected stability and alarm reasons, got %v", result.Reasons)
	}
	if hasReason(result.Reasons, "samples this cycle") || hasReason(result.Reasons, "high threshold") {
		t.Fatalf("unexpected reasons: %v", result.Reasons)
	}

	// Recovering the alarm leaves stability as the only blocker.
	for _, alarm := range plant.Alarms() {
		if alarm.Active() {
			if _, err := plant.RecoverAlarm(alarm.ID); err != nil {
				t.Fatalf("recover alarm: %v", err)
			}
		}
	}
	result, err = plant.AdvanceSettling("line-1")
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !result.Blocked || len(result.Reasons) != 1 || !hasReason(result.Reasons, "blanket stable for") {
		t.Fatalf("expected stability-only block, got %+v", result)
	}

	// Once the interface has been stable long enough, decant is allowed.
	clock.Advance(settling.MinBlanketStable + time.Minute)
	result, err = plant.AdvanceSettling("line-1")
	if err != nil || result.Blocked || result.Cycle.Phase != settling.PhaseDecant {
		t.Fatalf("advance to decant: %+v err=%v", result, err)
	}
	result, err = plant.AdvanceSettling("line-1")
	if err != nil || result.Blocked || result.Cycle.Phase != settling.PhaseDone {
		t.Fatalf("advance to done: %+v err=%v", result, err)
	}
}

func TestBeginSettlingRestartClearsEvidence(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	plant := newTestPlant(t, clock)

	// Run a full cycle with low, stable blanket readings.
	if _, err := plant.BeginSettling("line-1"); err != nil {
		t.Fatalf("begin settling: %v", err)
	}
	if _, err := plant.AdvanceSettling("line-1"); err != nil {
		t.Fatalf("advance to settle: %v", err)
	}
	for i := 0; i < settling.MinCycleBlanketSamples; i++ {
		if _, err := plant.UpdateBlanket("line-1", 2.0); err != nil {
			t.Fatalf("update blanket: %v", err)
		}
	}
	clock.Advance(settling.MinBlanketStable + time.Minute)
	if result, err := plant.AdvanceSettling("line-1"); err != nil || result.Blocked {
		t.Fatalf("advance to decant: %+v err=%v", result, err)
	}
	if result, err := plant.AdvanceSettling("line-1"); err != nil || result.Cycle.Phase != settling.PhaseDone {
		t.Fatalf("advance to done: %+v err=%v", result, err)
	}

	// Restarting clears the old cycle's evidence but keeps the latest valid
	// interface measurement for the status page.
	if _, err := plant.BeginSettling("line-1"); err != nil {
		t.Fatalf("restart settling: %v", err)
	}
	status := lineStatus(t, plant)
	if status.Cycle == nil || status.Cycle.Phase != settling.PhaseFill {
		t.Fatalf("expected fresh fill cycle, got %+v", status.Cycle)
	}
	if status.Settling.CycleSamples != 0 || status.Settling.StableSince != nil {
		t.Fatalf("expected cleared cycle evidence, got %+v", status.Settling)
	}
	if status.Settling.MeasuredAt == nil || status.Settling.BlanketLevel != 2.0 {
		t.Fatalf("expected latest valid measurement to persist, got %+v", status.Settling)
	}

	// The new cycle must gather its own samples before decant.
	if _, err := plant.AdvanceSettling("line-1"); err != nil {
		t.Fatalf("advance to settle: %v", err)
	}
	result, err := plant.AdvanceSettling("line-1")
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !result.Blocked || !hasReason(result.Reasons, "blanket samples this cycle 0/2") {
		t.Fatalf("expected sample shortage after restart, got %+v", result)
	}
}

func TestBeginSettlingWhileActiveKeepsEvidence(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	plant := newTestPlant(t, clock)

	if _, err := plant.BeginSettling("line-1"); err != nil {
		t.Fatalf("begin settling: %v", err)
	}
	if _, err := plant.UpdateBlanket("line-1", 2.0); err != nil {
		t.Fatalf("update blanket: %v", err)
	}
	if _, err := plant.BeginSettling("line-1"); err == nil {
		t.Fatal("expected conflict when restarting an active cycle")
	}
	if status := lineStatus(t, plant); status.Settling.CycleSamples != 1 {
		t.Fatalf("failed restart must not clear evidence, got %+v", status.Settling)
	}
}
