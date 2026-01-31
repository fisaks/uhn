package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type NetworkMode string

const (
	NetworkNone        NetworkMode = "none"
	NetworkLoopback    NetworkMode = "lo"
	NetworkDebugAttach NetworkMode = "debug-attach"
	NetworkFull        NetworkMode = "full"
)

type Limits struct {
	MemoryBytes int64 `json:"memoryBytes"`
	MaxPids     int64 `json:"maxPids"`
}

type SandboxConfig struct {
	Command     string      `json:"command"`
	Args        []string    `json:"args"`
	Cwd         string      `json:"cwd,omitempty"`
	Env         []string    `json:"env,omitempty"`
	Limits      *Limits     `json:"limits,omitempty"`
	RunAsUser   string      `json:"runAsUser"`
	Network     NetworkMode `json:"network,omitempty"`
	DebugListen string      `json:"debugListen,omitempty"`
}

func LoadSandboxConfig(path string) (*SandboxConfig, error) {
	var raw []byte
	var err error

	switch path {
	case "-":
		raw, err = readConfigFromStdin()
	default:
		raw, err = os.ReadFile(path)
	}

	if err != nil {
		return nil, err
	}

	var cfg SandboxConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}

	if cfg.Command == "" {
		return nil, errors.New("command is required")
	}
	if cfg.RunAsUser == "" {
		return nil, errors.New("runAsUser is required")
	}

	return &cfg, nil
}
func readConfigFromStdin() ([]byte, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(os.Stdin, &buf)

	dec := json.NewDecoder(tee)
	var tmp any
	if err := dec.Decode(&tmp); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func PersistSandboxConfig(cfg *SandboxConfig) (string, error) {

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}

	workspace := os.Getenv("UHN_WORKSPACE_PATH")
	if workspace == "" {
		return "", errors.New("Environment variable UHN_WORKSPACE_PATH is not set")
	}

	dir := filepath.Join(workspace, "sandbox", "current")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	configPath := filepath.Join(dir, "uhn-sandbox.json")
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		return "", err
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return "", err
	}

	return configPath, nil
}

var baseEnv = []string{
	"PATH=/usr/bin:/bin",
	"HOME=/tmp",
	"LANG=C.UTF-8",
}

func BaseSandboxEnv(passthrough bool) []string {
	env := append([]string(nil), baseEnv...)

	if passthrough {
		// Allowlisted passthrough vars from host
		allow := []string{
			"UHN_SANDBOX_PATH",
			"UHN_WORKSPACE_PATH",
			"UHN_RUNTIME_PATH",
			"UHN_NODE_PATH",
		}

		for _, key := range allow {
			if val, ok := os.LookupEnv(key); ok {
				env = append(env, key+"="+val)
			}
		}
	}

	return env
}
