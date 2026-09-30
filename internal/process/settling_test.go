package process

import (
	"errors"
	"testing"
	"time"

	"github.com/wyw14/cry-97/internal/model"
	"github.com/wyw14/cry-97/internal/settling"
)

func newTestPlant(t *testing.T) (*Plant, *time.Time) {
	t.Helper()
	current := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return current }
	plant, err := NewPlant(t.TempDir(), []model.LineID{"line-1"}, clock)
	if err != nil {
		t.Fatalf("new plant: %v", err)
	}
	return plant, &current
}

func TestSettlingCycleGuardedByBlanketEvidence(t *testing.T) {
	plant, now := newTestPlant(t)
	const line = model.LineID("line-1")

	cycle, err := plant.BeginSettling(line)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if cycle.Phase != settling.PhaseFill {
		t.Fatalf("phase = %s, want fill", cycle.Phase)
	}
	if status, _ := plant.Status(line); status.Settling.SampleCount != 0 || status.Settling.HasValidReading() {
		t.Fatalf("new cycle must start without blanket evidence: %+v", status.Settling)
	}

	// fill 阶段证据不足：落到 settle 而不是直接排水，并给出延迟原因。
	cycle, err = plant.AdvanceSettling(line)
	if err != nil {
		t.Fatalf("fill->settle should not error: %v", err)
	}
	if cycle.Phase != settling.PhaseSettle || len(cycle.HoldReasons) == 0 {
		t.Fatalf("phase = %s reasons = %v, want settle with reasons", cycle.Phase, cycle.HoldReasons)
	}

	// 三次有效界面测量，但稳定时长仍不足，排水必须被阻止。
	for range 3 {
		if _, err := plant.UpdateBlanket(line, 1.8); err != nil {
			t.Fatalf("update blanket: %v", err)
		}
	}
	_, err = plant.AdvanceSettling(line)
	var hold *settling.HoldError
	if !errors.As(err, &hold) {
		t.Fatalf("expected hold due to short stability, got %v", err)
	}

	// 沉淀足够时间且补一次新鲜测量后，允许进入 decant。
	*now = now.Add(11 * time.Minute)
	if _, err := plant.UpdateBlanket(line, 1.9); err != nil {
		t.Fatalf("update blanket: %v", err)
	}
	cycle, err = plant.AdvanceSettling(line)
	if err != nil {
		t.Fatalf("settle->decant: %v", err)
	}
	if cycle.Phase != settling.PhaseDecant {
		t.Fatalf("phase = %s, want decant", cycle.Phase)
	}
	status, _ := plant.Status(line)
	if status.Cycle == nil || status.Settling.SampleCount != 4 || status.Settling.LastReadingAt == nil {
		t.Fatalf("status missing cycle or latest reading: %+v", status.Settling)
	}
}

func TestRestartSettlingClearsOldEvidence(t *testing.T) {
	plant, now := newTestPlant(t)
	const line = model.LineID("line-1")

	_, _ = plant.BeginSettling(line)
	_, _ = plant.UpdateBlanket(line, 1.5)
	_, _ = plant.UpdateBlanket(line, 1.6)
	cycle, _ := plant.AdvanceSettling(line)
	if cycle.Phase != settling.PhaseSettle {
		t.Fatalf("setup phase = %s", cycle.Phase)
	}

	// 把旧周期走完，再重新开始。
	*now = now.Add(30 * time.Minute)
	_, _ = plant.UpdateBlanket(line, 1.4)
	*now = now.Add(11 * time.Minute)
	_, _ = plant.UpdateBlanket(line, 1.4)
	if cycle, err := plant.AdvanceSettling(line); err != nil || cycle.Phase != settling.PhaseDecant {
		t.Fatalf("setup decant phase = %s err = %v", cycle.Phase, err)
	}
	if cycle, err := plant.AdvanceSettling(line); err != nil || cycle.Phase != settling.PhaseDone {
		t.Fatalf("setup done phase = %s err = %v", cycle.Phase, err)
	}

	fresh, err := plant.BeginSettling(line)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if fresh.Phase != settling.PhaseFill || len(fresh.HoldReasons) != 0 {
		t.Fatalf("restarted cycle = %+v, want clean fill", fresh)
	}
	if state := plant.settling[line].Snapshot(); state.SampleCount != 0 || state.High || state.LastReadingAt != nil {
		t.Fatalf("old blanket evidence leaked into new cycle: %+v", state)
	}
}

func TestActiveAlarmBlocksDecantUntilRecovered(t *testing.T) {
	plant, now := newTestPlant(t)
	const line = model.LineID("line-1")

	_, _ = plant.BeginSettling(line)
	// 一次高位测量触发 SETTLING_BLANKET_HIGH 活动告警。
	if _, err := plant.UpdateBlanket(line, 3.2); err != nil {
		t.Fatalf("high blanket: %v", err)
	}
	active := plant.Alarms()
	if len(active) != 1 || !active[0].Active() {
		t.Fatalf("expected one active alarm, got %+v", active)
	}

	// 即使界面回落、采样与稳定时间满足，未恢复告警仍阻止直接排水。
	*now = now.Add(11 * time.Minute)
	for range 3 {
		_, _ = plant.UpdateBlanket(line, 1.5)
	}
	cycle, err := plant.AdvanceSettling(line)
	if err != nil {
		t.Fatalf("fill should fall through to settle: %v", err)
	}
	if cycle.Phase != settling.PhaseSettle {
		t.Fatalf("phase = %s, want settle", cycle.Phase)
	}

	// 再等足稳定时间并补新鲜测量，活动告警仍是唯一阻碍。
	*now = now.Add(11 * time.Minute)
	if _, err := plant.UpdateBlanket(line, 1.6); err != nil {
		t.Fatalf("fresh blanket: %v", err)
	}
	if _, err := plant.AdvanceSettling(line); !errors.As(err, new(*settling.HoldError)) {
		t.Fatalf("expected HoldError while alarm active, got %v", err)
	}

	// 恢复告警后允许排水。
	if _, err := plant.RecoverAlarm(active[0].ID); err != nil {
		t.Fatalf("recover alarm: %v", err)
	}
	cycle, err = plant.AdvanceSettling(line)
	if err != nil {
		t.Fatalf("advance after recovery: %v", err)
	}
	if cycle.Phase != settling.PhaseDecant {
		t.Fatalf("phase = %s, want decant after alarm recovery", cycle.Phase)
	}
}
