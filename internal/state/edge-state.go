package state

import (
	"bytes"
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/uhn"
)

type EdgeStateStore interface {
	GetLast(deviceName string) (uhn.DeviceState, time.Time, bool)
	Update(deviceName string, state uhn.DeviceState)
	HasChanged(deviceName string, state uhn.DeviceState) bool
	Clear()
}

type edgeStateStore struct {
	store     map[string]uhn.DeviceState
	heartbeat map[string]time.Time
	mu        sync.RWMutex
}

func NewEdgeStateStore() EdgeStateStore {
	return &edgeStateStore{
		store:     make(map[string]uhn.DeviceState),
		heartbeat: make(map[string]time.Time),
	}
}
func (s *edgeStateStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = make(map[string]uhn.DeviceState)
	s.heartbeat = make(map[string]time.Time)
}

func (s *edgeStateStore) GetLast(deviceName string) (uhn.DeviceState, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.store[deviceName]
	heartbeat, ok2 := s.heartbeat[deviceName]
	return state, heartbeat, ok && ok2
}
func (s *edgeStateStore) Update(deviceName string, state uhn.DeviceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[deviceName] = state
	s.heartbeat[deviceName] = time.Now()
}
func (s *edgeStateStore) HasChanged(deviceName string, state uhn.DeviceState) bool {
	lastState, _, ok := s.GetLast(deviceName)
	if !ok {
		return true
	}
	return !deviceStateEqual(lastState, state)
}

func deviceStateEqual(a, b uhn.DeviceState) bool {
	return bytes.Equal(a.DigitalOutputs, b.DigitalOutputs) &&
		bytes.Equal(a.DigitalInputs, b.DigitalInputs) &&
		bytes.Equal(a.AnalogOutputs, b.AnalogOutputs) &&
		bytes.Equal(a.AnalogInputs, b.AnalogInputs) &&
		a.Status == b.Status
}
