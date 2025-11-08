// internal/config/config-edge.go
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/logging"
)

/* =========================
   Types (devices keyed by busId)
   ========================= */

type EdgeConfig struct {
	Buses             []BusConfig                  `json:"buses"`
	Catalog           map[string]CatalogDeviceSpec `json:"catalog"`
	Devices           map[string][]DeviceConfig    `json:"devices"`           // key = busId
	PollIntervalMs    int                          `json:"pollIntervalMs"`    // global poll cadence
	HeartbeatInterval int                          `json:"heartbeatInterval"` // global heartbeat cadence
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
	PollIntervalMs        int    `json:"pollIntervalMs"` // global poll cadence
	Debug                 bool   `json:"debug"`
}

type Range struct {
	Start uint16 `json:"start"`
	Count uint16 `json:"count"`
}

type CatalogLimits struct {
	MaxCoilsPerRead     int `json:"maxCoilsPerRead"`
	MaxInputsPerRead    int `json:"maxInputsPerRead"`
	MaxRegistersPerRead int `json:"maxRegistersPerRead"`
}

type CatalogTimings struct {
	TimeoutMs             int `json:"timeoutMs"`
	SettleBeforeRequestMs int `json:"settleBeforeRequestMs"`
	SettleAfterWriteMs    int `json:"settleAfterWriteMs"`
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
}

type DeviceConfig struct {
	Name       string `json:"name"`
	UnitId     uint8  `json:"unitId"`
	Type       string `json:"type"` // key in Catalog
	Debug      bool   `json:"debug"`
	RetryCount int    `json:"retryCount,omitempty"`
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	clean := stripJSONComments(raw)

	dec := json.NewDecoder(strings.NewReader(string(clean)))
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

func (c *EdgeConfig) Validate() error {
	var errs multiErr

	/* Buses */
	if len(c.Buses) == 0 {
		errs.add("buses cannot be empty")
	} else {
		seen := map[string]int{}
		for i := range c.Buses {
			b := &c.Buses[i]
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
		}
	}

	/* Poll */
	if c.PollIntervalMs <= 0 {
		errs.add("pollIntervalMs must be > 0 (e.g., 100)")
	}
	if c.HeartbeatInterval < 0 {
		c.HeartbeatInterval = 60 // default 60s
	}
	if c.HeartbeatInterval == 0 {
		logging.Warn("heartbeatInterval=0 configured, heartbeats disabled")
	}
	/* Catalog */
	if len(c.Catalog) == 0 {
		errs.add("catalog cannot be empty")
	} else {
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
			lim := spec.Limits
			if lim.MaxCoilsPerRead <= 0 || lim.MaxCoilsPerRead > 2000 {
				errs.addf("catalog[%s].limits.maxCoilsPerRead must be 1..2000", key)
			}
			if lim.MaxInputsPerRead <= 0 || lim.MaxInputsPerRead > 2000 {
				errs.addf("catalog[%s].limits.maxInputsPerRead must be 1..2000", key)
			}
			if lim.MaxRegistersPerRead <= 0 || lim.MaxRegistersPerRead > 125 {
				errs.addf("catalog[%s].limits.maxRegistersPerRead must be 1..125", key)
			}
			if spec.Timings.SettleBeforeRequestMs < 0 || spec.Timings.SettleAfterWriteMs < 0 {
				errs.addf("catalog[%s].settle timings values cannot be negative", key)
			}
		}
	}

	/* Devices (map keyed by busId) */
	if len(c.Devices) == 0 {
		errs.add("devices cannot be empty")
	} else {
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

	if len(errs) > 0 {
		return errs
	}
	return nil
}

/* =========================
   Comment stripping + utils
   ========================= */

var (
	lineComments  = regexp.MustCompile(`(?m)//[^\n\r]*`)
	blockComments = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func stripJSONComments(in []byte) []byte {
	text := string(in)
	text = blockComments.ReplaceAllString(text, "")
	text = lineComments.ReplaceAllString(text, "")
	return []byte(text)
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
	clean := stripJSONComments(raw)
	dec := json.NewDecoder(strings.NewReader(string(clean)))
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
