package settling

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/wyw14/cry-97/internal/model"
)

// BlanketHighLevel 是污泥界面高位报警阈值（米），与排水准入共用。
const BlanketHighLevel = 2.8

type Snapshot struct {
	LineID        model.LineID `json:"line_id"`
	BasinID       string       `json:"basin_id"`
	BlanketLevel  float64      `json:"blanket_level"`
	High          bool         `json:"high"`
	SampleCount   int          `json:"sample_count"`
	Cycle         uint64       `json:"cycle"`
	LastReadingAt *time.Time   `json:"last_reading_at,omitempty"`
	UpdatedAt     time.Time    `json:"updated_at"`

	// stableSince 记录界面最近一次转为非高位的时刻；高位时为 nil。
	stableSince *time.Time
}

// HasValidReading 报告当前周期是否已经收到过有效的界面测量。
func (s Snapshot) HasValidReading() bool {
	return s.SampleCount > 0
}

// StableFor 返回截至 now 界面持续处于非高位的时长；无法确认时为 0。
func (s Snapshot) StableFor(now time.Time) time.Duration {
	if s.stableSince == nil {
		return 0
	}
	duration := now.Sub(s.stableSince.UTC())
	if duration < 0 {
		return 0
	}
	return duration
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

// UpdateBlanket 记录一次界面测量。有限且非负的读数才是有效测量，
// 会计入当前周期的采样次数；无效读数被忽略，不产生阶段推进证据。
func (s *State) UpdateBlanket(level, highThreshold float64, now time.Time) Snapshot {
	if math.IsNaN(level) || math.IsInf(level, 0) || level < 0 {
		s.mu.RLock()
		current := s.value
		s.mu.RUnlock()
		return current
	}
	s.mu.Lock()
	before := s.value
	stamp := now.UTC()
	high := level >= highThreshold
	s.value.BlanketLevel = level
	s.value.High = high
	s.value.SampleCount++
	s.value.LastReadingAt = &stamp
	if high {
		// 界面超高会打断稳定计时，回落后方可重新累计。
		s.value.stableSince = nil
	} else if s.value.stableSince == nil {
		s.value.stableSince = &stamp
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

// BeginCycle 开始一个新的沉淀周期，并清除上一个周期遗留的界面证据，
// 避免旧读数被误用于新阶段的排水准入判断。
func (s *State) BeginCycle(now time.Time) Snapshot {
	s.mu.Lock()
	s.value.Cycle++
	s.value.BlanketLevel = 0
	s.value.High = false
	s.value.SampleCount = 0
	s.value.LastReadingAt = nil
	s.value.stableSince = nil
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
