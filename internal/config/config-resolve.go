package config

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ResolvedEdgeConfig holds the final resolved configuration after merging
// env vars, config file settings, and defaults. Priority: env var > config file > default.
type ResolvedEdgeConfig struct {
	Name          string
	MqttURL       string
	ConfigPath    string
	WorkspacePath string
	SandboxPath   string
	NodePath      string
	RuntimePath   string
	LogLevel      string
	LogFormat     string
	RuntimeMode   string
	DebugPort     int
	DebugHost     string
	TZ            string
}

// ResolveEdgeConfig merges env vars, config file edge settings, and defaults.
func ResolveEdgeConfig(cfgPath string, edge *EdgeSettings) *ResolvedEdgeConfig {
	if edge == nil {
		edge = &EdgeSettings{}
	}

	r := &ResolvedEdgeConfig{
		ConfigPath: cfgPath,
	}

	// Identity & connectivity: env > config (name has no default — must be set)
	r.Name = resolve(os.Getenv("UHN_EDGE_NAME"), edge.Name, "")
	r.MqttURL = resolve(os.Getenv("UHN_MQTT_URL"), edge.Mqtt, "tcp://localhost:1883")

	// Runtime behavior: env > config > default
	r.LogLevel = resolve(strings.ToLower(os.Getenv("UHN_LOG_LEVEL")), strings.ToLower(edge.LogLevel), "info")
	r.LogFormat = resolve(strings.ToLower(os.Getenv("UHN_LOG_FORMAT")), strings.ToLower(edge.LogFormat), "json")
	r.RuntimeMode = resolve(strings.ToLower(os.Getenv("UHN_RUNTIME_MODE")), strings.ToLower(edge.RuntimeMode), "normal")

	// Infrastructure paths: env only
	r.WorkspacePath = envOrDefault("UHN_WORKSPACE_PATH", "")
	r.SandboxPath = envOrDefault("UHN_SANDBOX_PATH", "/usr/lib/uhn")
	r.NodePath = envOrDefault("UHN_NODE_PATH", "/opt/node")
	r.RuntimePath = os.Getenv("UHN_RUNTIME_PATH")
	r.TZ = envOrDefault("TZ", "UTC")

	// Debug host: env > config (display-only, NOT overridden by MQTT)
	r.DebugHost = resolve(os.Getenv("UHN_PUBLIC_HOST"), edge.DebugHost, "")

	// Debug port: env > config > auto
	if envPort := os.Getenv("UHN_DEBUG_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			r.DebugPort = p
		}
	}
	if r.DebugPort == 0 && edge.DebugPort > 0 {
		r.DebugPort = edge.DebugPort
	}
	if r.DebugPort == 0 {
		r.DebugPort = autoDebugPort(r.Name)
	}

	// Persisted runtime config (from MQTT commands) takes highest precedence
	if r.WorkspacePath != "" {
		if p, err := LoadPersistedRuntimeConfig(r.WorkspacePath); err == nil {
			if p.LogLevel != "" {
				r.LogLevel = p.LogLevel
			}
			if p.RuntimeMode != "" {
				r.RuntimeMode = p.RuntimeMode
			}
			if p.DebugPort > 0 {
				r.DebugPort = p.DebugPort
			}
		}
	}

	return r
}

const persistedConfigFile = "runtime-config.json"

type persistedRuntimeConfig struct {
	LogLevel    string `json:"logLevel,omitempty"`
	RuntimeMode string `json:"runtimeMode,omitempty"`
	DebugPort   int    `json:"debugPort,omitempty"`
}

// LoadPersistedRuntimeConfig reads the last MQTT-set logLevel/runtimeMode from disk.
func LoadPersistedRuntimeConfig(workspacePath string) (*persistedRuntimeConfig, error) {
	data, err := os.ReadFile(filepath.Join(workspacePath, persistedConfigFile))
	if err != nil {
		return nil, err
	}
	var p persistedRuntimeConfig
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// SavePersistedRuntimeConfig writes the current logLevel/runtimeMode/debugPort to disk
// so they survive edge restarts.
func SavePersistedRuntimeConfig(workspacePath, logLevel, runtimeMode string, debugPort int) error {
	p := persistedRuntimeConfig{LogLevel: logLevel, RuntimeMode: runtimeMode, DebugPort: debugPort}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspacePath, persistedConfigFile), data, 0644)
}

var validEdgeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateEdgeName checks that the edge name is set and contains only safe characters.
func ValidateEdgeName(name string) error {
	if name == "" {
		return fmt.Errorf("edge name not configured: set UHN_EDGE_NAME env var or edge.name in config file")
	}
	if !validEdgeName.MatchString(name) {
		return fmt.Errorf("edge name %q contains invalid characters: only a-z, A-Z, 0-9, hyphen, underscore, and dot are allowed", name)
	}
	return nil
}

// resolve returns the first non-empty value from env, config, or default.
func resolve(env, cfg, def string) string {
	if env != "" {
		return env
	}
	if cfg != "" {
		return cfg
	}
	return def
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// autoDebugPort derives a deterministic debug port from the edge name.
// Range: 9250–9299 (master uses 9250, so edges with auto port get 9251–9299).
func autoDebugPort(edgeName string) int {
	h := fnv.New32a()
	h.Write([]byte(edgeName))
	return 9251 + int(h.Sum32()%49)
}
