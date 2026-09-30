package settling

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/wyw14/cry-97/internal/model"
)

const (
	// BlanketHighThreshold is the sludge blanket level (m) at or above which
	// the interface is considered too high to decant.
	BlanketHighThreshold = 2.8
	// MinCycleBlanketSamples is the minimum number of blanket readings a
	// cycle must collect before the decant phase may start.
	MinCycleBlanketSamples = 2
	// MinBlanketStable is how long the blanket level must hold steady before
	// the decant phase may start.
	MinBlanketStable = 20 * time.Minute
	// StableTolerance is the blanket level drift (m) ignored when tracking
	// how long the interface has been stable.
	StableTolerance = 0.05
)

type Snapshot struct {
	LineID       model.LineID `json:"line_id"`
	BasinID      string       `json:"basin_id"`
	BlanketLevel float64      `json:"blanket_level"`
	High         bool         `json:"high"`
	Cycle        uint64       `json:"cycle"`
	CycleSamples int          `json:"cycle_samples"`
	MeasuredAt   *time.Time   `json:"measured_at,omitempty"`
	StableSince  *time.Time   `json:"stable_since,omitempty"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type BlanketNotice struct {
	Before Snapshot
	After  Snapshot
}

type Listener interface {
	BlanketChanged(BlanketNotice)
}

type State struct {
	mu       sync.RWMutex
	value    Snapshot
	listener Listener
}

func NewState(lineID model.LineID, basinID string, listener Listener) (*State, error) {
	if lineID == "" || basinID == "" {
		return nil, errors.New("settling state identity is required")
	}
	return &State{value: Snapshot{LineID: lineID, BasinID: basinID}, listener: listener}, nil
}

func (s *State) UpdateBlanket(level, highThreshold float64, now time.Time) Snapshot {
	stamp := now.UTC()
	s.mu.Lock()
	before := s.value
	s.value.BlanketLevel = level
	s.value.High = level >= highThreshold
	s.value.MeasuredAt = &stamp
	s.value.CycleSamples++
	if s.value.StableSince == nil || math.Abs(level-before.BlanketLevel) > StableTolerance {
		stable := stamp
		s.value.StableSince = &stable
	}
	s.value.UpdatedAt = stamp
	after := s.value
	listener := s.listener
	s.mu.Unlock()
	if listener != nil && before.High != after.High {
		listener.BlanketChanged(BlanketNotice{Before: before, After: after})
	}
	return after
}

func (s *State) BeginCycle(now time.Time) Snapshot {
	s.mu.Lock()
	s.value.Cycle++
	s.value.CycleSamples = 0
	s.value.StableSince = nil
	s.value.UpdatedAt = now.UTC()
	value := s.value
	s.mu.Unlock()
	return value
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}
