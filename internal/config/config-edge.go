// internal/config/config-edge.go
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/logging"
)

/* =========================
   Types (devices keyed by busId)
   ========================= */

// IHC Controller types

type IHCHealthCheckConfig struct {
	Resources      []string `json:"resources"`
	IntervalSec    int      `json:"intervalSec"`
	MaxFailures    int      `json:"maxFailures,omitempty"` // consecutive failures before reconnect (0 = disable reconnect)
}

type IHCResourceConfig struct {
	ResourceID    string `json:"resourceId"`
	Type          string `json:"type"` // digitalOutput | digitalInput | analogOutput | analogInput
	ResourceIntID int    `json:"-"`    // runtime only: integer form of ResourceID (hex string)
}

type IHCControllerConfig struct {
	Name                 string                `json:"name"`
	Host                 string                `json:"host"`
	Port                 int                   `json:"port"`
	WaitTimeoutSec       int                   `json:"waitTimeoutSec"`
	MaxConsecutiveErrors int                   `json:"maxConsecutiveErrors"`
	HealthCheck          *IHCHealthCheckConfig `json:"healthCheck,omitempty"`
	Resources            []*IHCResourceConfig  `json:"resources"`

	// Runtime only (loaded from credentials file)
	Username string `json:"-"`
	Password string `json:"-"`
	// Runtime only: parsed health-check resource IDs
	HealthCheckResourceIDs []int `json:"-"`
}

type ihcCredentialsFile map[string]struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type EdgeSettings struct {
	Name        string `json:"name,omitempty"`
	Mqtt        string `json:"mqtt,omitempty"`
	LogLevel    string `json:"logLevel,omitempty"`    // initial boot default
	RuntimeMode string `json:"runtimeMode,omitempty"` // initial boot default
	DebugPort   int    `json:"debugPort,omitempty"`   // 0 = auto (base + offset from name)
	DebugHost   string `json:"debugHost,omitempty"`   // display-only host/IP for debug port
	LogFormat   string `json:"logFormat,omitempty"`    // "json" (default) or "text"
}

// Mi-Light types

// GatekeeperRef references a digital resource on the same edge that gates
// commands for a device. When the gatekeeper is OFF (e.g. mains relay),
// commands are suppressed. Must be a digitalOutput or digitalInput (bool
// state). Only works with drivers that publish state via
// UpdatePhysicalStateByAddress (IHC, Mi-Light). Modbus uses a different
// state path and would not populate the gatekeeper cache.
type GatekeeperRef struct {
	Device string `json:"device"` // any device name (e.g. "ihc2", "toilet_io8_1")
	Type   string `json:"type"`   // resource type (e.g. "digitalOutput")
	Pin    string `json:"pin"`    // numeric or hex ("0x9F045C", "0")
	PinInt int    `json:"-"`      // populated during validation
}

type MilightZoneConfig struct {
	Name       string         `json:"name"`                 // device name (e.g. "milight-z1")
	Zone       byte           `json:"zone"`                 // 1-4
	Model      string         `json:"model"`                // bulb model (e.g. "fut069")
	Gatekeeper *GatekeeperRef `json:"gatekeeper,omitempty"` // optional: mains power resource that gates commands
}

// Supported Mi-Light bulb models.
var milightSupportedModels = map[string]bool{
	"fut069": true,
}

type MilightConfig struct {
	Host             string               `json:"host"`
	Port             int                  `json:"port"`              // default 5987
	Zones            []*MilightZoneConfig `json:"zones"`
	CommandDelayMs   int                  `json:"commandDelayMs"`    // default 100
	CommandRetries   int                  `json:"commandRetries"`    // default 1
	CommandTimeoutMs int                  `json:"commandTimeoutMs"`  // default 500
}

type EdgeConfig struct {
	Edge              *EdgeSettings                 `json:"edge,omitempty"`
	Buses             []*BusConfig                  `json:"buses"`
	Catalog           map[string]*CatalogDeviceSpec `json:"catalog"`
	Devices           map[string][]*DeviceConfig    `json:"devices"`                     // key = busId
	PollIntervalMs    int                           `json:"pollIntervalMs"`              // global poll cadence
	HeartbeatInterval int                           `json:"heartbeatInterval,omitempty"` // global heartbeat cadence
	CommandBufferSize int                           `json:"commandBufferSize,omitempty"`
	DevicesByName     map[string]*DeviceConfig      `json:"-"`                           // runtime only, built by linkGraph

	// IHC controllers (optional, can coexist with Modbus)
	IHCCredentialsFile string                 `json:"ihcCredentialsFile,omitempty"`
	IHCControllers     []*IHCControllerConfig `json:"ihcControllers,omitempty"`

	// Mi-Light gateways (optional, can coexist with Modbus and IHC)
	Milights []*MilightConfig `json:"milights,omitempty"`

	// Zigbee adapters via Zigbee2MQTT (optional, can coexist with all other protocols)
	Zigbee []*ZigbeeAdapterConfig `json:"zigbee,omitempty"`
}

// ZigbeeAdapterConfig configures a Zigbee2MQTT adapter connection.
type ZigbeeAdapterConfig struct {
	Name      string                `json:"name"`                // adapter name, e.g. "zigbee_1"
	BaseTopic string                `json:"baseTopic,omitempty"` // Z2M base topic, default "zigbee2mqtt"
	Devices   []*ZigbeeDeviceConfig `json:"devices"`             // devices to expose to blueprints
}

// ZigbeeDeviceConfig configures a single Z2M device exposed to blueprints.
type ZigbeeDeviceConfig struct {
	Name       string `json:"name"`                 // Z2M friendly name, e.g. "kitchen_temperature_display"
	Optimistic *bool  `json:"optimistic,omitempty"` // override Z2M optimistic setting (nil = don't change)
}

type BusConfig struct {
	BusId                 string `json:"busId"`
	Type                  string `json:"type"` // "rtu" | "tcp"
	TCPAddr               string `json:"tcpAddr"`
	Port                  string `json:"port"`
	Baud                  int    `json:"baud"`
	DataBits              int    `json:"dataBits"`
	StopBits              int    `json:"stopBits"`
	Parity                string `json:"parity"`
	TimeoutMs             int    `json:"timeoutMs"`
	SettleBeforeRequestMs int    `json:"settleBeforeRequestMs"`
	SettleAfterWriteMs    int    `json:"settleAfterWriteMs"`
	PollIntervalMs        int    `json:"pollIntervalMs"`

	CommandBufferSize int  `json:"commandBufferSize,omitempty"`
	Debug             bool `json:"debug"`

	Devices []*DeviceConfig // runtime only
}

type Range struct {
	Start uint16 `json:"start"`
	Count uint16 `json:"count"`
	Type  string `json:"type,omitempty"`
}

var validRegisterTypes = map[string]bool{
	"": true, "uint16": true, "int16": true, "float32": true, "uint32": true, "int32": true,
}

// RegisterWidth returns the number of 16-bit registers consumed per value.
// 2-register types: float32, uint32, int32. Everything else: 1.
func (r *Range) RegisterWidth() int {
	switch r.Type {
	case "float32", "uint32", "int32":
		return 2
	default:
		return 1
	}
}

type CatalogLimits struct {
	MaxDigitalChunkSize uint16 `json:"maxDigitalChunkSize"`
	MaxAnalogChunkSize  uint16 `json:"maxAnalogChunkSize"`
}

type CatalogTimings struct {
	TimeoutMs             uint16 `json:"timeoutMs"`
	SettleBeforeRequestMs uint16 `json:"settleBeforeRequestMs"`
	SettleAfterWriteMs    uint16 `json:"settleAfterWriteMs"`
}

type Capabilities struct {
	ToggleWord uint16 `json:"toggleWord,omitempty"`
}
type CatalogDeviceSpec struct {
	Vendor         string         `json:"vendor"`
	Model          string         `json:"model"`
	DigitalOutputs *Range         `json:"digitalOutputs"`
	DigitalInputs  *Range         `json:"digitalInputs"`
	AnalogOutputs  *Range         `json:"analogOutputs"`
	AnalogInputs   *Range         `json:"analogInputs"`
	Limits         CatalogLimits  `json:"limits"`
	Timings        CatalogTimings `json:"timings"`
	Debug          bool           `json:"debug"`
	Capabilities   Capabilities   `json:"capabilities,omitempty"`
}

type DeviceConfig struct {
	Name        string             `json:"name"`
	UnitId      uint8              `json:"unitId"`
	Type        string             `json:"type"` // key in Catalog
	Debug       bool               `json:"debug"`
	RetryCount  uint8              `json:"retryCount,omitempty"`
	CatalogSpec *CatalogDeviceSpec // runtime only
	Bus         *BusConfig         // runtime only
}

/* =========================
   Helpers
   ========================= */

func (b BusConfig) Timeout() time.Duration { return time.Duration(b.TimeoutMs) * time.Millisecond }
func (b BusConfig) SettleBeforeRequest() time.Duration {
	return time.Duration(b.SettleBeforeRequestMs) * time.Millisecond
}
func (b BusConfig) SettleAfterWrite() time.Duration {
	return time.Duration(b.SettleAfterWriteMs) * time.Millisecond
}

func (t CatalogTimings) Timeout() time.Duration {
	return time.Duration(t.TimeoutMs) * time.Millisecond
}
func (t CatalogTimings) SettleBeforeRequest() time.Duration {
	return time.Duration(t.SettleBeforeRequestMs) * time.Millisecond
}
func (t CatalogTimings) SettleAfterWrite() time.Duration {
	return time.Duration(t.SettleAfterWriteMs) * time.Millisecond
}

/* =========================
   Strict load + validate
   ========================= */

func LoadEdgeConfig(path string) (*EdgeConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	defer f.Close()
	return LoadEdgeConfigFromReader(f)
}

var validLogLevels = map[string]bool{"trace": true, "debug": true, "info": true, "warn": true, "error": true}
var validRuntimeModes = map[string]bool{"normal": true, "debug": true}

func (c *EdgeConfig) Validate() error {
	var errs multiErr

	/* Edge settings */
	if c.Edge == nil {
		c.Edge = &EdgeSettings{}
	}
	if c.Edge.LogLevel != "" && !validLogLevels[strings.ToLower(c.Edge.LogLevel)] {
		errs.addf("edge.logLevel: must be one of trace, debug, info, warn, error (got %q)", c.Edge.LogLevel)
	}
	if c.Edge.RuntimeMode != "" && !validRuntimeModes[strings.ToLower(c.Edge.RuntimeMode)] {
		errs.addf("edge.runtimeMode: must be one of normal, debug (got %q)", c.Edge.RuntimeMode)
	}
	if c.Edge.DebugPort != 0 && (c.Edge.DebugPort < 1024 || c.Edge.DebugPort > 65535) {
		errs.addf("edge.debugPort: must be in range 1024-65535 (got %d)", c.Edge.DebugPort)
	}
	if c.Edge.LogFormat != "" && c.Edge.LogFormat != "json" && c.Edge.LogFormat != "text" {
		errs.addf("edge.logFormat: must be json or text (got %q)", c.Edge.LogFormat)
	}

	/* Poll */
	if c.PollIntervalMs <= 0 {
		c.PollIntervalMs = 500 // default 500ms
	}
	if c.HeartbeatInterval < 0 {
		c.HeartbeatInterval = 60 // default 60s
	}
	if c.HeartbeatInterval == 0 {
		logging.Warn("heartbeatInterval=0 configured, heartbeats disabled")
	}

	if c.CommandBufferSize <= 0 {
		c.CommandBufferSize = 64 // default buffer size
	}

	hasModbus := len(c.Buses) > 0 || len(c.Catalog) > 0 || len(c.Devices) > 0
	hasIHC := len(c.IHCControllers) > 0
	hasMilight := len(c.Milights) > 0
	hasZigbee := len(c.Zigbee) > 0

	if !hasModbus && !hasIHC && !hasMilight && !hasZigbee {
		errs.add("at least one device source required (buses/catalog/devices for Modbus, ihcControllers for IHC, milights for Mi-Light, or zigbee for Zigbee2MQTT)")
	}

	/* Buses */
	if len(c.Buses) == 0 && hasModbus && (len(c.Catalog) > 0 || len(c.Devices) > 0) {
		errs.add("buses cannot be empty when catalog or devices are defined")
	} else if len(c.Buses) > 0 {
		seen := map[string]int{}
		for i := range c.Buses {
			b := c.Buses[i]
			if strings.TrimSpace(b.BusId) == "" {
				errs.addf("buses[%d]: busId is required", i)
			} else if j, ok := seen[b.BusId]; ok {
				errs.addf("buses[%d]: duplicate busId %q (also at buses[%d])", i, b.BusId, j)
			} else {
				seen[b.BusId] = i
			}

			switch strings.ToLower(b.Type) {
			case "tcp":
				if strings.TrimSpace(b.TCPAddr) == "" {
					errs.addf("buses[%d/%s]: tcpAddr is required for type=tcp", i, b.BusId)
				}
			case "rtu":
				if strings.TrimSpace(b.Port) == "" {
					errs.addf("buses[%d/%s]: port is required for type=rtu", i, b.BusId)
				}
				if b.Baud <= 0 {
					errs.addf("buses[%d/%s]: baud must be > 0 for type=rtu", i, b.BusId)
				}
				if b.DataBits == 0 {
					b.DataBits = 8
				}
				if b.StopBits == 0 {
					b.StopBits = 1
				}
				if b.Parity == "" {
					b.Parity = "N"
				}
				if !slices.Contains([]string{"N", "E", "O"}, strings.ToUpper(b.Parity)) {
					errs.addf("buses[%d/%s]: parity must be one of N,E,O", i, b.BusId)
				}
			default:
				errs.addf("buses[%d/%s]: type must be 'rtu' or 'tcp'", i, b.BusId)
			}

			if b.TimeoutMs <= 0 {
				b.TimeoutMs = 150
			}
			if b.SettleBeforeRequestMs < 0 || b.SettleAfterWriteMs < 0 {
				errs.addf("buses[%d/%s]: settle timings cannot be negative", i, b.BusId)
			}
			if b.PollIntervalMs <= 0 {
				b.PollIntervalMs = c.PollIntervalMs
			}
			if b.CommandBufferSize <= 0 {
				b.CommandBufferSize = c.CommandBufferSize
			}

			c.Buses[i] = b
		}
	}

	/* Catalog */
	if len(c.Catalog) == 0 && hasModbus && (len(c.Buses) > 0 || len(c.Devices) > 0) {
		errs.add("catalog cannot be empty when buses or devices are defined")
	} else if len(c.Catalog) > 0 {
		for key, spec := range c.Catalog {
			if spec.Vendor == "" || spec.Model == "" {
				errs.addf("catalog[%s]: vendor and model are required", key)
			}
			if spec.DigitalOutputs != nil && spec.DigitalOutputs.Count == 0 {
				errs.addf("catalog[%s].digitalOutputs.count must be > 0", key)
			}
			if spec.DigitalInputs != nil && spec.DigitalInputs.Count == 0 {
				errs.addf("catalog[%s].digitalInputs.count must be > 0", key)
			}
			if spec.AnalogOutputs != nil && spec.AnalogOutputs.Count == 0 {
				errs.addf("catalog[%s].analogOutputs.count must be > 0", key)
			}
			if spec.AnalogInputs != nil && spec.AnalogInputs.Count == 0 {
				errs.addf("catalog[%s].analogInputs.count must be > 0", key)
			}

			// Validate register type on analog ranges
			for _, entry := range []struct {
				label string
				r     *Range
			}{
				{"analogOutputs", spec.AnalogOutputs},
				{"analogInputs", spec.AnalogInputs},
			} {
				if entry.r == nil {
					continue
				}
				if !validRegisterTypes[entry.r.Type] {
					errs.addf("catalog[%s].%s.type: must be one of uint16, int16, float32, uint32, int32 (got %q)", key, entry.label, entry.r.Type)
				}
				if w := entry.r.RegisterWidth(); w > 1 && entry.r.Count%uint16(w) != 0 {
					errs.addf("catalog[%s].%s.count: must be a multiple of %d for type %q (got %d)", key, entry.label, w, entry.r.Type, entry.r.Count)
				}
			}

			if spec.Limits.MaxDigitalChunkSize <= 0 || spec.Limits.MaxDigitalChunkSize > 2000 {
				spec.Limits.MaxDigitalChunkSize = 2000
			}
			if spec.Limits.MaxAnalogChunkSize <= 0 || spec.Limits.MaxAnalogChunkSize > 125 {
				spec.Limits.MaxAnalogChunkSize = 125
			}

			c.Catalog[key] = spec
		}
	}

	/* Devices (map keyed by busId) */
	if len(c.Devices) == 0 && hasModbus && (len(c.Buses) > 0 || len(c.Catalog) > 0) {
		errs.add("devices cannot be empty when buses or catalog are defined")
	} else if len(c.Devices) > 0 {
		// Known buses
		busSet := map[string]struct{}{}
		for _, b := range c.Buses {
			busSet[b.BusId] = struct{}{}
		}

		// Ensure all keys correspond to known buses
		for busID, list := range c.Devices {
			if _, ok := busSet[busID]; !ok {
				errs.addf("devices[%s]: busId not defined in buses[*].busId", busID)
			}
			// enforce global unique device names
			// (use one map outside loop)
			_ = list
		}

		// Unique device name across ALL buses
		seenNames := map[string]string{} // name -> busId
		for busID, list := range c.Devices {
			for i, d := range list {
				if strings.TrimSpace(d.Name) == "" {
					errs.addf("devices[%s][%d]: name is required", busID, i)
				} else if otherBus, clash := seenNames[d.Name]; clash {

					errs.addf("devices[%s][%d/%s]: duplicate device name (already in bus %s)", busID, i, d.Name, otherBus)
				} else {
					seenNames[d.Name] = busID
				}

				if d.UnitId == 0 || d.UnitId > 247 {
					errs.addf("devices[%s][%d/%s]: unitId must be 1..247", busID, i, d.Name)
				}
				if d.Type == "" {
					errs.addf("devices[%s][%d/%s]: type is required", busID, i, d.Name)
				} else if _, ok := c.Catalog[d.Type]; !ok {
					errs.addf("devices[%s][%d/%s]: unknown catalog type %q", busID, i, d.Name, d.Type)
				}

			}
		}
	}

	/* IHC Controllers */
	if hasIHC {
		c.validateIHC(&errs)
	}

	/* Mi-Light Bridges */
	if hasMilight {
		c.validateMilight(&errs)
	}

	/* Zigbee Adapters */
	if hasZigbee {
		c.validateZigbee(&errs)
	}

	if len(errs) > 0 {
		return errs
	}

	// Check for device name collisions across all device sources
	allNames := map[string]string{} // name -> source label
	for _, devs := range c.Devices {
		for _, d := range devs {
			allNames[d.Name] = "Modbus"
		}
	}
	for _, ctrl := range c.IHCControllers {
		if src, ok := allNames[ctrl.Name]; ok {
			return fmt.Errorf("IHC controller name %q collides with a %s device name", ctrl.Name, src)
		}
		allNames[ctrl.Name] = "IHC"
	}
	for _, ml := range c.Milights {
		for _, zone := range ml.Zones {
			if src, ok := allNames[zone.Name]; ok {
				return fmt.Errorf("Mi-Light zone name %q collides with a %s device name", zone.Name, src)
			}
			allNames[zone.Name] = "Mi-Light"
		}
	}

	for _, z := range c.Zigbee {
		for _, dev := range z.Devices {
			if src, ok := allNames[dev.Name]; ok {
				return fmt.Errorf("Zigbee device name %q collides with a %s device name", dev.Name, src)
			}
			allNames[dev.Name] = "Zigbee"
		}
	}

	// Cross-validate gatekeeper device references exist in allNames
	for _, ml := range c.Milights {
		for _, zone := range ml.Zones {
			if zone.Gatekeeper != nil && strings.TrimSpace(zone.Gatekeeper.Device) != "" {
				if _, ok := allNames[zone.Gatekeeper.Device]; !ok {
					return fmt.Errorf("Mi-Light zone %q gatekeeper references unknown device %q", zone.Name, zone.Gatekeeper.Device)
				}
			}
		}
	}

	return c.linkGraph()
}
func (c *EdgeConfig) linkGraph() error {
	c.DevicesByName = make(map[string]*DeviceConfig)

	// Skip Modbus linking if no buses defined (IHC-only config)
	if len(c.Buses) == 0 {
		return nil
	}

	// Map for quick bus lookup
	busMap := map[string]*BusConfig{}
	for _, b := range c.Buses {
		busMap[b.BusId] = b
		b.Devices = nil // ensure empty
	}
	// Link devices to catalog, and buses to devices
	for busID, devs := range c.Devices {
		bus, ok := busMap[busID]
		if !ok {
			return fmt.Errorf("bus %q not found", busID)
		}
		for _, dev := range devs {
			// Wire device <-> catalog
			spec := c.Catalog[dev.Type]
			if spec == nil {
				return fmt.Errorf("unknown device type %q for device %q", dev.Type, dev.Name)
			}
			dev.CatalogSpec = spec
			dev.Bus = bus
			// Wire bus <-> device
			bus.Devices = append(bus.Devices, dev)
			c.DevicesByName[dev.Name] = dev
		}
	}
	return nil
}

/* =========================
   IHC validation + helpers
   ========================= */

var validResourceTypes = map[string]bool{
	"digitalOutput": true,
	"digitalInput":  true,
	"analogOutput":  true,
	"analogInput":   true,
}

func (c *EdgeConfig) validateIHC(errs *multiErr) {
	// Load credentials file if specified
	var creds ihcCredentialsFile
	if c.IHCCredentialsFile != "" {
		var err error
		creds, err = loadIHCCredentials(c.IHCCredentialsFile)
		if err != nil {
			errs.addf("ihcCredentialsFile: %v", err)
			return
		}
	}

	seenNames := map[string]bool{}
	for i, ctrl := range c.IHCControllers {
		prefix := fmt.Sprintf("ihcControllers[%d/%s]", i, ctrl.Name)

		if strings.TrimSpace(ctrl.Name) == "" {
			errs.addf("ihcControllers[%d]: name is required", i)
		} else if seenNames[ctrl.Name] {
			errs.addf("%s: duplicate controller name", prefix)
		} else {
			seenNames[ctrl.Name] = true
		}

		if strings.TrimSpace(ctrl.Host) == "" {
			errs.addf("%s: host is required", prefix)
		}
		if ctrl.Port <= 0 {
			ctrl.Port = 80
		}
		if ctrl.WaitTimeoutSec <= 0 {
			ctrl.WaitTimeoutSec = 10
		}
		if ctrl.MaxConsecutiveErrors <= 0 {
			ctrl.MaxConsecutiveErrors = 4
		}

		// Credentials
		if creds == nil {
			errs.addf("%s: ihcCredentialsFile is required when ihcControllers are defined", prefix)
		} else if entry, ok := creds[ctrl.Name]; !ok {
			errs.addf("%s: no credentials found in credentials file", prefix)
		} else {
			ctrl.Username = entry.Username
			ctrl.Password = entry.Password
			if ctrl.Username == "" || ctrl.Password == "" {
				errs.addf("%s: username and password are required in credentials file", prefix)
			}
		}

		// Resources
		if len(ctrl.Resources) == 0 {
			errs.addf("%s: resources cannot be empty", prefix)
		}
		seenIDs := map[int]bool{}
		for j, res := range ctrl.Resources {
			resPrefix := fmt.Sprintf("%s.resources[%d]", prefix, j)
			if !validResourceTypes[res.Type] {
				errs.addf("%s: type must be digitalOutput|digitalInput|analogOutput|analogInput (got %q)", resPrefix, res.Type)
			}
			parsed, err := parseResourcePin(res.ResourceID)
			if err != nil {
				errs.addf("%s: invalid resourceId %q: %v", resPrefix, res.ResourceID, err)
			} else {
				if seenIDs[parsed] {
					errs.addf("%s: duplicate resourceId 0x%X", resPrefix, parsed)
				}
				seenIDs[parsed] = true
				res.ResourceIntID = parsed
			}
		}

		// Health check
		if ctrl.HealthCheck != nil {
			if len(ctrl.HealthCheck.Resources) == 0 {
				errs.addf("%s.healthCheck: resources cannot be empty", prefix)
			}
			if ctrl.HealthCheck.IntervalSec <= 0 {
				ctrl.HealthCheck.IntervalSec = 60
			}
			for j, raw := range ctrl.HealthCheck.Resources {
				parsed, err := parseResourcePin(raw)
				if err != nil {
					errs.addf("%s.healthCheck.resources[%d]: invalid resourceId %q: %v", prefix, j, raw, err)
				} else {
					ctrl.HealthCheckResourceIDs = append(ctrl.HealthCheckResourceIDs, parsed)
				}
			}
		}

		c.IHCControllers[i] = ctrl
	}
}

// parseResourcePin parses a resource pin/ID from hex string ("0x9F1F3E", "_0x9F1F3E") or integer.
func parseResourcePin(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("empty resource ID")
	}
	// Strip leading underscore (IHC project file format)
	s = strings.TrimPrefix(s, "_")

	// Hex format: 0x... or 0X...
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := strconv.ParseInt(s[2:], 16, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid hex: %w", err)
		}
		return int(v), nil
	}

	// Plain integer
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid integer: %w", err)
	}
	return v, nil
}

// FormatHexID formats a device address/resource ID as hex for logging/display.
func FormatHexID(id int) string {
	return fmt.Sprintf("0x%X", id)
}

// FormatPin formats a pin of any type for logging.
// Numeric pins (float64/int): "decimal (0xHEX)". String pins: as-is.
func FormatPin(pin any) string {
	switch v := pin.(type) {
	case float64:
		return fmt.Sprintf("%d (0x%X)", int(v), int(v))
	case int:
		return fmt.Sprintf("%d (0x%X)", v, v)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func loadIHCCredentials(path string) (ihcCredentialsFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open credentials file: %w", err)
	}
	defer f.Close()

	var creds ihcCredentialsFile
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&creds); err != nil {
		return nil, fmt.Errorf("parse credentials file: %w", err)
	}
	return creds, nil
}

/* =========================
   Mi-Light validation
   ========================= */

func (c *EdgeConfig) validateMilight(errs *multiErr) {
	seenZoneNames := map[string]bool{}
	for i, ml := range c.Milights {
		prefix := fmt.Sprintf("milights[%d]", i)

		if strings.TrimSpace(ml.Host) == "" {
			errs.addf("%s: host is required", prefix)
		}
		if ml.Port <= 0 {
			ml.Port = 5987
		}
		if ml.CommandDelayMs <= 0 {
			ml.CommandDelayMs = 100
		}
		if ml.CommandRetries <= 0 {
			ml.CommandRetries = 1
		}
		if ml.CommandTimeoutMs <= 0 {
			ml.CommandTimeoutMs = 500
		}

		if len(ml.Zones) == 0 {
			errs.addf("%s: zones cannot be empty", prefix)
		}

		seenZones := map[byte]bool{}
		for j, zone := range ml.Zones {
			zPrefix := fmt.Sprintf("%s.zones[%d]", prefix, j)

			if strings.TrimSpace(zone.Name) == "" {
				errs.addf("%s: name is required", zPrefix)
			} else if seenZoneNames[zone.Name] {
				errs.addf("%s: duplicate zone name %q", zPrefix, zone.Name)
			} else {
				seenZoneNames[zone.Name] = true
			}

			if seenZones[zone.Zone] {
				errs.addf("%s: duplicate zone number %d within gateway", zPrefix, zone.Zone)
			} else {
				seenZones[zone.Zone] = true
			}

			if zone.Model == "" {
				errs.addf("%s: model is required", zPrefix)
			} else if !milightSupportedModels[zone.Model] {
				errs.addf("%s: unsupported model %q", zPrefix, zone.Model)
			}
		}

		// Validate gatekeeper references on each zone
		for j, zone := range ml.Zones {
			if zone.Gatekeeper == nil {
				continue
			}
			gk := zone.Gatekeeper
			gkPrefix := fmt.Sprintf("%s.zones[%d/%s].gatekeeper", prefix, j, zone.Name)

			if strings.TrimSpace(gk.Device) == "" {
				errs.addf("%s: device is required", gkPrefix)
			}
			if gk.Type != "digitalOutput" && gk.Type != "digitalInput" {
				errs.addf("%s: type must be digitalOutput|digitalInput (got %q)", gkPrefix, gk.Type)
			}
			parsed, err := parseResourcePin(gk.Pin)
			if err != nil {
				errs.addf("%s: invalid pin %q: %v", gkPrefix, gk.Pin, err)
			} else {
				gk.PinInt = parsed
			}
		}

		c.Milights[i] = ml
	}
}

/* =========================
   Zigbee validation
   ========================= */

func (c *EdgeConfig) validateZigbee(errs *multiErr) {
	seenAdapterNames := map[string]bool{}
	seenDeviceNames := map[string]string{} // device name → adapter name (for cross-adapter uniqueness)
	for i, z := range c.Zigbee {
		prefix := fmt.Sprintf("zigbee[%d/%s]", i, z.Name)

		if strings.TrimSpace(z.Name) == "" {
			errs.addf("zigbee[%d]: name is required", i)
		} else if seenAdapterNames[z.Name] {
			errs.addf("%s: duplicate adapter name", prefix)
		} else {
			seenAdapterNames[z.Name] = true
		}

		// Default baseTopic
		if z.BaseTopic == "" {
			z.BaseTopic = "zigbee2mqtt"
		}

		// Validate devices
		seenInAdapter := map[string]bool{}
		for j, dev := range z.Devices {
			devPrefix := fmt.Sprintf("%s.devices[%d/%s]", prefix, j, dev.Name)
			if strings.TrimSpace(dev.Name) == "" {
				errs.addf("%s.devices[%d]: name is required", prefix, j)
			} else {
				if seenInAdapter[dev.Name] {
					errs.addf("%s: duplicate device name within adapter", devPrefix)
				} else {
					seenInAdapter[dev.Name] = true
				}
				if otherAdapter, clash := seenDeviceNames[dev.Name]; clash {
					errs.addf("%s: duplicate device name (already in adapter %s)", devPrefix, otherAdapter)
				} else {
					seenDeviceNames[dev.Name] = z.Name
				}
			}
		}

		c.Zigbee[i] = z
	}
}

// small multi-error
type multiErr []string

func (m *multiErr) add(s string)            { *m = append(*m, s) }
func (m *multiErr) addf(f string, a ...any) { *m = append(*m, fmt.Sprintf(f, a...)) }
func (m multiErr) Error() string            { return "validation errors: " + strings.Join(m, "; ") }

/* =========================
   Optional reader loader
   ========================= */

func LoadEdgeConfigFromReader(r io.Reader) (*EdgeConfig, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var cfg EdgeConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	return &cfg, nil
}
