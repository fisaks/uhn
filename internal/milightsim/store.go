package milightsim

import (
	"log"
	"sync"

	"github.com/fisaks/uhn/internal/config"
)

// ZoneState holds the current state of one Mi-Light zone.
type ZoneState struct {
	Zone       byte   `json:"zone"`
	DeviceName string `json:"deviceName"`
	Power      bool   `json:"power"`
	Brightness int    `json:"brightness"` // 0-100
	ColorTemp  int    `json:"colorTemp"`  // 0-100 (warm→cool)
	Hue        int    `json:"hue"`        // 0-255 (protocol level)
	Saturation int    `json:"saturation"` // 0-100
	Mode       int    `json:"mode"`       // 1-9
	ModeSpeed  int    `json:"modeSpeed"`  // 1-100
}

// MilightSimStore is the central state for all simulated Mi-Light zones.
type MilightSimStore struct {
	mu    sync.Mutex
	zones map[byte]*ZoneState // zone number => state
	port  int
}

// SetupFromConfig creates a MilightSimStore from the edge config.
// Uses the first milight entry (the sim only runs one gateway).
func SetupFromConfig(cfg *config.EdgeConfig) (*MilightSimStore, error) {
	store := &MilightSimStore{
		zones: make(map[byte]*ZoneState),
	}

	if len(cfg.Milights) == 0 {
		return store, nil
	}

	ml := cfg.Milights[0]
	store.port = ml.Port

	for _, zone := range ml.Zones {
		store.zones[zone.Zone] = &ZoneState{
			Zone:       zone.Zone,
			DeviceName: zone.Name,
		}
		log.Printf("Mi-Light Sim: zone %d (%s) configured", zone.Zone, zone.Name)
	}

	return store, nil
}

// Port returns the configured UDP port.
func (s *MilightSimStore) Port() int {
	if s.port <= 0 {
		return 5987
	}
	return s.port
}

// GetZone returns a copy of the zone state.
func (s *MilightSimStore) GetZone(zone byte) (*ZoneState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return nil, false
	}
	copy := *z
	return &copy, true
}

// GetAllZones returns a copy of all zone states.
func (s *MilightSimStore) GetAllZones() []*ZoneState {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*ZoneState, 0, len(s.zones))
	for _, z := range s.zones {
		copy := *z
		result = append(result, &copy)
	}
	return result
}

// SetPower sets the power state for a zone.
func (s *MilightSimStore) SetPower(zone byte, on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	z.Power = on
	return true
}

// SetBrightness sets the brightness for a zone.
func (s *MilightSimStore) SetBrightness(zone byte, val int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	z.Brightness = val
	return true
}

// SetColorTemp sets the color temperature for a zone.
func (s *MilightSimStore) SetColorTemp(zone byte, val int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	z.ColorTemp = val
	return true
}

// SetHue sets the hue for a zone (0-255 protocol level).
func (s *MilightSimStore) SetHue(zone byte, val int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	z.Hue = val
	return true
}

// SetSaturation sets the saturation for a zone.
func (s *MilightSimStore) SetSaturation(zone byte, val int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	z.Saturation = val
	return true
}

// SetMode sets the effect mode for a zone.
func (s *MilightSimStore) SetMode(zone byte, val int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	z.Mode = val
	return true
}

// IncModeSpeed increases the effect speed for a zone.
func (s *MilightSimStore) IncModeSpeed(zone byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	if z.ModeSpeed < 100 {
		z.ModeSpeed++
	}
	return true
}

// DecModeSpeed decreases the effect speed for a zone.
func (s *MilightSimStore) DecModeSpeed(zone byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false
	}
	if z.ModeSpeed > 0 {
		z.ModeSpeed--
	}
	return true
}

// TogglePower toggles the power state for a zone. Returns the new state.
func (s *MilightSimStore) TogglePower(zone byte) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zone]
	if !ok {
		return false, false
	}
	z.Power = !z.Power
	return z.Power, true
}
