package zigbeesim

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"
)

// Z2MSimStore holds the simulated Z2M state.
type Z2MSimStore struct {
	mu sync.RWMutex

	// Raw bridge/devices JSON (published as-is to bridge/devices topic)
	bridgeDevicesJSON []byte

	// Device friendly names (non-coordinator, in order)
	deviceNames []string

	// Current state per device
	states map[string]map[string]any
}

// NewZ2MSimStore loads fixtures and creates the store.
func NewZ2MSimStore(devicesPath, statePath string) (*Z2MSimStore, error) {
	devicesJSON, err := os.ReadFile(devicesPath)
	if err != nil {
		return nil, fmt.Errorf("read devices fixture: %w", err)
	}

	// Parse device names from the fixture
	var rawDevices []struct {
		FriendlyName string `json:"friendly_name"`
		Type         string `json:"type"`
	}
	if err := json.Unmarshal(devicesJSON, &rawDevices); err != nil {
		return nil, fmt.Errorf("parse devices fixture: %w", err)
	}

	var names []string
	for _, d := range rawDevices {
		if d.Type != "Coordinator" {
			names = append(names, d.FriendlyName)
		}
	}

	// Load initial state
	states := make(map[string]map[string]any)
	stateJSON, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("read state fixture: %w", err)
	}
	if err := json.Unmarshal(stateJSON, &states); err != nil {
		return nil, fmt.Errorf("parse state fixture: %w", err)
	}

	// Ensure all devices have an entry
	for _, name := range names {
		if states[name] == nil {
			states[name] = make(map[string]any)
		}
	}

	return &Z2MSimStore{
		bridgeDevicesJSON: devicesJSON,
		deviceNames:       names,
		states:            states,
	}, nil
}

// BridgeDevicesJSON returns the raw bridge/devices JSON for publishing.
func (s *Z2MSimStore) BridgeDevicesJSON() []byte {
	return s.bridgeDevicesJSON
}

// DeviceNames returns the list of simulated device names.
func (s *Z2MSimStore) DeviceNames() []string {
	return s.deviceNames
}

// GetState returns the current state for a device as JSON bytes.
func (s *Z2MSimStore) GetState(device string) ([]byte, error) {
	s.mu.RLock()
	state, ok := s.states[device]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown device: %s", device)
	}
	return json.Marshal(state)
}

// GetStateMap returns the current state map for a device.
func (s *Z2MSimStore) GetStateMap(device string) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.states[device]
	if state == nil {
		return nil
	}
	// Return a copy
	copy := make(map[string]any, len(state))
	for k, v := range state {
		copy[k] = v
	}
	return copy
}

// SetProperty sets a single property on a device and returns the full state JSON.
func (s *Z2MSimStore) SetProperty(device, property string, value any) ([]byte, error) {
	s.mu.Lock()
	state, ok := s.states[device]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown device: %s", device)
	}
	state[property] = value
	data, err := json.Marshal(state)
	s.mu.Unlock()
	return data, err
}

// ToggleState toggles the "state" property between ON/OFF.
func (s *Z2MSimStore) ToggleState(device string) ([]byte, error) {
	s.mu.Lock()
	state, ok := s.states[device]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown device: %s", device)
	}
	current, _ := state["state"].(string)
	if current == "ON" {
		state["state"] = "OFF"
	} else {
		state["state"] = "ON"
	}
	data, err := json.Marshal(state)
	s.mu.Unlock()
	return data, err
}

// EmitAction builds a state blob with the current device state plus the action field.
// The action value is transient — it is NOT persisted in the store.
func (s *Z2MSimStore) EmitAction(device, action string) ([]byte, error) {
	s.mu.RLock()
	state, ok := s.states[device]
	if !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("unknown device: %s", device)
	}
	// Build a copy with the action field included
	blob := make(map[string]any, len(state)+1)
	for k, v := range state {
		blob[k] = v
	}
	blob["action"] = action
	data, err := json.Marshal(blob)
	s.mu.RUnlock()
	return data, err
}

// SimulateTick generates small random variations in sensor values.
// Returns a map of device → updated state JSON for devices that changed.
func (s *Z2MSimStore) SimulateTick() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := make(map[string][]byte)
	for name, state := range s.states {
		modified := false

		// Temperature drift ±0.1
		if temp, ok := state["temperature"].(float64); ok {
			state["temperature"] = math.Round((temp+rand.Float64()*0.2-0.1)*10) / 10
			modified = true
		}
		// Humidity drift ±0.5
		if hum, ok := state["humidity"].(float64); ok {
			state["humidity"] = math.Round((hum+rand.Float64()*1.0-0.5)*10) / 10
			modified = true
		}
		// Power fluctuation ±0.5 (only when on)
		if power, ok := state["power"].(float64); ok && power > 0 {
			state["power"] = math.Round((power+rand.Float64()*1.0-0.5)*100) / 100
			modified = true
		}
		// Current fluctuation ±0.01
		if current, ok := state["current"].(float64); ok && current > 0 {
			state["current"] = math.Round((current+rand.Float64()*0.02-0.01)*1000) / 1000
			modified = true
		}
		// Voltage fluctuation ±0.5
		if voltage, ok := state["voltage"].(float64); ok && voltage > 100 {
			state["voltage"] = math.Round((voltage+rand.Float64()*1.0-0.5)*100) / 100
			modified = true
		}

		if modified {
			if data, err := json.Marshal(state); err == nil {
				changed[name] = data
			}
		}
	}
	return changed
}
