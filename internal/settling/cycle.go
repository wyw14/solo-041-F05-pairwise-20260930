package settling

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

type CyclePhase string

const (
	PhaseFill   CyclePhase = "fill"
	PhaseSettle CyclePhase = "settle"
	PhaseDecant CyclePhase = "decant"
	PhaseDone   CyclePhase = "done"
)

// DefaultDecantRule 是进入排水（decant）阶段前的默认准入条件。
var DefaultDecantRule = DecantRule{
	MinSamples:   3,
	MaxSampleAge: 15 * time.Minute,
	MinStableFor: 10 * time.Minute,
}

type Cycle struct {
	ID          uuid.UUID    `json:"id"`
	LineID      model.LineID `json:"line_id"`
	Phase       CyclePhase   `json:"phase"`
	StartedAt   time.Time    `json:"started_at"`
	SettledAt   *time.Time   `json:"settled_at,omitempty"`
	DecantAt    *time.Time   `json:"decant_at,omitempty"`
	UpdatedAt   time.Time    `json:"updated_at"`
	HoldReasons []string     `json:"hold_reasons,omitempty"`
}

// DecantRule 描述从 fill/settle 进入 decant 的准入条件。
type DecantRule struct {
	// MinSamples 是当前周期内要求的最少有效界面测量次数。
	MinSamples int
	// MaxSampleAge 限制最近一次有效测量距当前的最大时长。
	MaxSampleAge time.Duration
	// MinStableFor 要求界面持续处于非高位的最短时长。
	MinStableFor time.Duration
}

// AdvanceEvidence 是阶段推进时评估排水条件所需的现场证据。
type AdvanceEvidence struct {
	Blanket          Snapshot
	ActiveAlarmCodes []string
}

// HoldError 表示当前证据不满足排水准入，阶段被延迟，并携带具体原因。
type HoldError struct {
	Reasons []string
}

func (e *HoldError) Error() string {
	if len(e.Reasons) == 0 {
		return "decant conditions are not met"
	}
	return "decant held: " + strings.Join(e.Reasons, "; ")
}

// HoldReasons 供响应层统一提取延迟原因。
func (e *HoldError) HoldReasons() []string {
	return append([]string(nil), e.Reasons...)
}

type CycleBook struct {
	mu     sync.RWMutex
	cycles map[model.LineID]Cycle
}

func NewCycleBook() *CycleBook {
	return &CycleBook{cycles: make(map[model.LineID]Cycle)}
}

func (b *CycleBook) Start(lineID model.LineID, now time.Time) (Cycle, error) {
	if lineID == "" {
		return Cycle{}, errors.New("settling line is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if current, ok := b.cycles[lineID]; ok && current.Phase != PhaseDone {
		return Cycle{}, errors.New("settling cycle is already active")
	}
	cycle := Cycle{ID: uuid.New(), LineID: lineID, Phase: PhaseFill, StartedAt: now.UTC(), UpdatedAt: now.UTC()}
	b.cycles[lineID] = cycle
	return cycle, nil
}

// Advance 推进沉淀周期。
//
// fill 阶段：证据齐备时直接进入 decant（跳过 settle）；否则进入 settle，
// 并记录延迟原因。settle 阶段：条件不满足时返回 *HoldError 阻止进入 decant，
// 当前阶段保持不变。decant 阶段无条件进入 done。
func (b *CycleBook) Advance(lineID model.LineID, evidence AdvanceEvidence, rule DecantRule, now time.Time) (Cycle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cycle, ok := b.cycles[lineID]
	if !ok {
		return Cycle{}, errors.New("settling cycle is not active")
	}
	now = now.UTC()
	switch cycle.Phase {
	case PhaseFill:
		cycle.HoldReasons = nil
		if ready, reasons := rule.evaluate(evidence, now); ready {
			cycle.SettledAt = &now
			cycle.DecantAt = &now
			cycle.Phase = PhaseDecant
		} else {
			// 证据尚不足以直接排水，转入沉淀阶段并保留延迟原因。
			stamp := now
			cycle.SettledAt = &stamp
			cycle.Phase = PhaseSettle
			cycle.HoldReasons = reasons
		}
	case PhaseSettle:
		ready, reasons := rule.evaluate(evidence, now)
		if !ready {
			// 停留在沉淀阶段，但把本次的延迟原因留痕，供状态页展示。
			cycle.HoldReasons = reasons
			cycle.UpdatedAt = now
			b.cycles[lineID] = cycle
			return cycle, &HoldError{Reasons: reasons}
		}
		cycle.Phase = PhaseDecant
		cycle.DecantAt = &now
		cycle.HoldReasons = nil
	case PhaseDecant:
		cycle.Phase = PhaseDone
	default:
		return Cycle{}, errors.New("settling cycle is complete")
	}
	cycle.UpdatedAt = now
	b.cycles[lineID] = cycle
	return cycle, nil
}

func (b *CycleBook) Current(lineID model.LineID) (Cycle, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	cycle, ok := b.cycles[lineID]
	return cycle, ok
}

// evaluate 根据界面高度、有效采样、稳定持续时间与活动告警判断是否允许排水。
func (rule DecantRule) evaluate(evidence AdvanceEvidence, now time.Time) (bool, []string) {
	reasons := make([]string, 0)
	if evidence.Blanket.SampleCount < rule.MinSamples {
		reasons = append(reasons, fmt.Sprintf(
			"有效界面采样不足（当前 %d 次，要求至少 %d 次）",
			evidence.Blanket.SampleCount, rule.MinSamples))
	} else if evidence.Blanket.LastReadingAt == nil {
		reasons = append(reasons, "缺少有效的界面测量记录，请重新采样")
	} else if age := now.Sub(evidence.Blanket.LastReadingAt.UTC()); age > rule.MaxSampleAge {
		reasons = append(reasons, fmt.Sprintf(
			"最近界面测量已过期（%s 前，要求 %s 内），请重新采样",
			age.Round(time.Second), rule.MaxSampleAge))
	}
	if evidence.Blanket.High {
		reasons = append(reasons, fmt.Sprintf(
			"污泥界面超高（%.2f m，高于阈值 %.2f m），禁止排水",
			evidence.Blanket.BlanketLevel, BlanketHighLevel))
	}
	if stableFor := evidence.Blanket.StableFor(now); stableFor < rule.MinStableFor {
		reasons = append(reasons, fmt.Sprintf(
			"界面稳定持续时间不足（已稳定 %s，要求至少 %s）",
			stableFor.Round(time.Second), rule.MinStableFor))
	}
	if len(evidence.ActiveAlarmCodes) > 0 {
		reasons = append(reasons, "存在未恢复告警："+strings.Join(evidence.ActiveAlarmCodes, ", "))
	}
	return len(reasons) == 0, reasons
}
