package settling

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wyw14/cry-97/internal/model"
)

const line = model.LineID("line-1")

// readyEvidence 构造一份满足 DefaultDecantRule 的排水证据。
func readyEvidence(now time.Time) AdvanceEvidence {
	stable := now.Add(-11 * time.Minute)
	reading := now.Add(-time.Minute)
	return AdvanceEvidence{
		Blanket: Snapshot{
			LineID: line, BasinID: "settler-line-1",
			BlanketLevel: 2.0, High: false, SampleCount: 3,
			LastReadingAt: &reading, stableSince: &stable,
		},
	}
}

func TestAdvanceWithoutCycle(t *testing.T) {
	book := NewCycleBook()
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	if _, err := book.Advance(line, readyEvidence(now), DefaultDecantRule, now); err == nil {
		t.Fatal("advance without active cycle should fail")
	}
}

func TestFillAdvancesDirectlyToDecantWhenReady(t *testing.T) {
	book := NewCycleBook()
	start := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	if _, err := book.Start(line, start); err != nil {
		t.Fatalf("start: %v", err)
	}
	now := start.Add(20 * time.Minute)
	cycle, err := book.Advance(line, readyEvidence(now), DefaultDecantRule, now)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if cycle.Phase != PhaseDecant {
		t.Fatalf("phase = %s, want decant (direct skip)", cycle.Phase)
	}
	if cycle.SettledAt == nil || cycle.DecantAt == nil {
		t.Fatal("settled_at and decant_at should both be recorded")
	}
	if len(cycle.HoldReasons) != 0 {
		t.Fatalf("unexpected hold reasons: %v", cycle.HoldReasons)
	}
}

func TestFillDropsToSettleWithHoldReasonsWhenSamplesInsufficient(t *testing.T) {
	book := NewCycleBook()
	start := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	_, _ = book.Start(line, start)
	now := start.Add(20 * time.Minute)

	evidence := readyEvidence(now)
	evidence.Blanket.SampleCount = 1
	cycle, err := book.Advance(line, evidence, DefaultDecantRule, now)
	if err != nil {
		t.Fatalf("fill should still advance to settle, got error %v", err)
	}
	if cycle.Phase != PhaseSettle {
		t.Fatalf("phase = %s, want settle", cycle.Phase)
	}
	if cycle.SettledAt == nil {
		t.Fatal("settled_at should be recorded when entering settle")
	}
	if len(cycle.HoldReasons) == 0 {
		t.Fatal("expected hold reasons explaining the delay")
	}
}

func TestSettleBlockedFromDecantWhileBlanketHigh(t *testing.T) {
	book := NewCycleBook()
	start := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	_, _ = book.Start(line, start)

	enter := start.Add(20 * time.Minute)
	evidence := readyEvidence(enter)
	evidence.Blanket.SampleCount = 0
	if cycle, err := book.Advance(line, evidence, DefaultDecantRule, enter); err != nil || cycle.Phase != PhaseSettle {
		t.Fatalf("setup phase = %s err = %v, want settle", cycle.Phase, err)
	}

	now := enter.Add(15 * time.Minute)
	blocked := readyEvidence(now)
	blocked.Blanket.BlanketLevel = 3.1
	blocked.Blanket.High = true
	blocked.Blanket.stableSince = nil
	_, err := book.Advance(line, blocked, DefaultDecantRule, now)
	var hold *HoldError
	if !errors.As(err, &hold) {
		t.Fatalf("expected HoldError, got %v", err)
	}
	found := false
	for _, reason := range hold.HoldReasons() {
		found = found || strings.Contains(reason, "界面超高")
	}
	if !found {
		t.Fatalf("high blanket reason missing: %v", hold.HoldReasons())
	}
	current, _ := book.Current(line)
	if current.Phase != PhaseSettle {
		t.Fatalf("phase = %s, must stay settle when blocked", current.Phase)
	}
	if len(current.HoldReasons) == 0 {
		t.Fatal("current cycle should persist the latest hold reasons")
	}
}

func TestSettleAdvancesToDecantOnceReady(t *testing.T) {
	book := NewCycleBook()
	start := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	_, _ = book.Start(line, start)

	enter := start.Add(20 * time.Minute)
	notReady := readyEvidence(enter)
	notReady.Blanket.SampleCount = 0
	if cycle, err := book.Advance(line, notReady, DefaultDecantRule, enter); err != nil || cycle.Phase != PhaseSettle {
		t.Fatalf("setup phase = %s err = %v", cycle.Phase, err)
	}

	now := enter.Add(15 * time.Minute)
	cycle, err := book.Advance(line, readyEvidence(now), DefaultDecantRule, now)
	if err != nil {
		t.Fatalf("advance to decant: %v", err)
	}
	if cycle.Phase != PhaseDecant {
		t.Fatalf("phase = %s, want decant", cycle.Phase)
	}
	if len(cycle.HoldReasons) != 0 {
		t.Fatalf("hold reasons should clear, got %v", cycle.HoldReasons)
	}

	cycle, err = book.Advance(line, readyEvidence(now), DefaultDecantRule, now)
	if err != nil {
		t.Fatalf("advance to done: %v", err)
	}
	if cycle.Phase != PhaseDone {
		t.Fatalf("phase = %s, want done", cycle.Phase)
	}
}

func TestStaleReadingBlocksDecant(t *testing.T) {
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	evidence := readyEvidence(now)
	stale := now.Add(-16 * time.Minute)
	evidence.Blanket.LastReadingAt = &stale
	ready, reasons := DefaultDecantRule.evaluate(evidence, now)
	if ready {
		t.Fatal("stale reading should block decant")
	}
	if !reasonMentions(reasons, "已过期") {
		t.Fatalf("stale reason missing: %v", reasons)
	}
}

func TestActiveAlarmBlocksDecant(t *testing.T) {
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	evidence := readyEvidence(now)
	evidence.ActiveAlarmCodes = []string{"SETTLING_BLANKET_HIGH"}
	ready, reasons := DefaultDecantRule.evaluate(evidence, now)
	if ready {
		t.Fatal("active alarm should block decant")
	}
	if !reasonMentions(reasons, "未恢复告警") {
		t.Fatalf("alarm reason missing: %v", reasons)
	}
}

func TestInsufficientStableDurationBlocksDecant(t *testing.T) {
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	evidence := readyEvidence(now)
	recent := now.Add(-5 * time.Minute)
	evidence.Blanket.stableSince = &recent
	ready, reasons := DefaultDecantRule.evaluate(evidence, now)
	if ready {
		t.Fatal("short stability window should block decant")
	}
	if !reasonMentions(reasons, "稳定持续时间不足") {
		t.Fatalf("stability reason missing: %v", reasons)
	}
}

func TestRestartRequiresDoneCycle(t *testing.T) {
	book := NewCycleBook()
	start := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	_, _ = book.Start(line, start)
	if _, err := book.Start(line, start.Add(time.Minute)); err == nil {
		t.Fatal("starting a second active cycle should fail")
	}
}

func reasonMentions(reasons []string, fragment string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, fragment) {
			return true
		}
	}
	return false
}
