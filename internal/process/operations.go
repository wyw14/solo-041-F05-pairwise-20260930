package process

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/aeration"
	"github.com/wyw14/cry-97/internal/dosing"
	"github.com/wyw14/cry-97/internal/model"
	"github.com/wyw14/cry-97/internal/settling"
)

func (p *Plant) SubmitAeration(ctx context.Context, lineID model.LineID, basinID string, payload []byte) (aeration.Window, *model.DeviceCommand, error) {
	p.mu.RLock()
	state, ok := p.lines[lineID]
	p.mu.RUnlock()
	if !ok || state.Emergency {
		return aeration.Window{}, nil, errors.New("process line cannot accept aeration samples")
	}
	return p.aeration.SubmitWindow(ctx, lineID, basinID, payload, state.Generation, p.now())
}

func (p *Plant) ChangeDose(ctx context.Context, lineID model.LineID, batchID uuid.UUID, chemical string, rate float64) (model.DeviceCommand, error) {
	p.mu.RLock()
	state, ok := p.lines[lineID]
	p.mu.RUnlock()
	if !ok || state.Emergency {
		return model.DeviceCommand{}, errors.New("process line cannot change dosing")
	}
	change, err := dosing.NewChange(lineID, batchID, chemical, rate, state.Generation, p.now())
	if err != nil {
		return model.DeviceCommand{}, err
	}
	return p.dosing.ChangeRate(ctx, change, p.now())
}

func (p *Plant) UpdateBlanket(lineID model.LineID, level float64) (settling.Snapshot, error) {
	state, ok := p.settling[lineID]
	if !ok {
		return settling.Snapshot{}, errors.New("settling line is not found")
	}
	return state.UpdateBlanket(level, settling.BlanketHighThreshold, p.now()), nil
}

func (p *Plant) ReceiveSample(ctx context.Context, sample model.Sample) error {
	if err := p.receiver.Receive(ctx, sample, p.now()); err != nil {
		return err
	}
	return p.sampler.Publish(sample)
}

func (p *Plant) BeginSettling(lineID model.LineID) (settling.Cycle, error) {
	state, ok := p.settling[lineID]
	if !ok {
		return settling.Cycle{}, errors.New("settling line is not found")
	}
	cycle, err := p.cycles.Start(lineID, p.now())
	if err != nil {
		return settling.Cycle{}, err
	}
	// A fresh cycle must not inherit the previous cycle's phase evidence:
	// reset the per-cycle sample count and stability anchor.
	state.BeginCycle(p.now())
	return cycle, nil
}

type SettlingAdvance struct {
	Cycle   settling.Cycle `json:"cycle"`
	Blocked bool           `json:"blocked"`
	Reasons []string       `json:"reasons,omitempty"`
}

func (p *Plant) AdvanceSettling(lineID model.LineID) (SettlingAdvance, error) {
	cycle, ok := p.cycles.Current(lineID)
	if !ok {
		return SettlingAdvance{}, errors.New("settling cycle is not active")
	}
	if cycle.Phase == settling.PhaseSettle {
		state, ok := p.settling[lineID]
		if !ok {
			return SettlingAdvance{}, errors.New("settling line is not found")
		}
		if reasons := settling.DecantDelays(state.Snapshot(), len(p.alarms.Active(lineID)), p.now()); len(reasons) > 0 {
			return SettlingAdvance{Cycle: cycle, Blocked: true, Reasons: reasons}, nil
		}
	}
	advanced, err := p.cycles.Advance(lineID, p.now())
	if err != nil {
		return SettlingAdvance{}, err
	}
	return SettlingAdvance{Cycle: advanced}, nil
}

func (p *Plant) Alarms() []model.Alarm {
	return p.alarms.All()
}

func (p *Plant) StatusBlowers() []aeration.Blower {
	return p.aeration.Blowers()
}

func (p *Plant) StatusAerationWindows() []aeration.Window {
	return p.aeration.Windows()
}

func (p *Plant) AcknowledgeAlarm(id uuid.UUID) (model.Alarm, error) {
	return p.alarms.Acknowledge(id)
}

func (p *Plant) RecoverAlarm(id uuid.UUID) (model.Alarm, error) {
	return p.alarms.Recover(id, p.now())
}

func (p *Plant) Clock() time.Time {
	return p.now().UTC()
}
