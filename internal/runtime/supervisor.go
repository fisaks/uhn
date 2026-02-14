package runtime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
)

const (
	readyTimeout       = 20 * time.Second
	initialBackoff     = 500 * time.Millisecond
	maxBackoff         = 30 * time.Second
	shutdownGracePeriod = 5 * time.Second
	pidFileName        = "rule-runtime.pid"
)

// RuntimeSupervisor manages the lifecycle of a sandboxed rule-runtime process.
type RuntimeSupervisor struct {
	workspacePath   string
	sandboxPath     string
	nodePath        string
	runtimePath     string
	process         *exec.Cmd
	stdin           io.WriteCloser // kept open for IPC commands to the rule runtime
	running         bool
	runID           int
	restartAttempts int
	mu              sync.Mutex
}

func NewRuntimeSupervisor(workspacePath string) *RuntimeSupervisor {
	return &RuntimeSupervisor{
		workspacePath: workspacePath,
		sandboxPath:   getenvDefault("UHN_SANDBOX_PATH", "/usr/lib/uhn"),
		nodePath:      getenvDefault("UHN_NODE_PATH", "/opt/node"),
		runtimePath:   os.Getenv("UHN_RUNTIME_PATH"),
	}
}

func (s *RuntimeSupervisor) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		s.Stop()
		s.mu.Lock()
	}

	s.killOrphan()
	s.runID++
	s.restartAttempts = 0
	currentRunID := s.runID
	s.mu.Unlock()

	s.startProcess(currentRunID)
}

func (s *RuntimeSupervisor) Stop() {
	s.mu.Lock()
	s.runID++ // invalidate any restart watcher
	proc := s.process
	stdinPipe := s.stdin
	s.running = false
	s.process = nil
	s.stdin = nil
	s.mu.Unlock()

	if stdinPipe != nil {
		stdinPipe.Close()
	}
	if proc != nil && proc.Process != nil {
		s.terminateProcess(proc)
	}
	s.removePIDFile()
}

func (s *RuntimeSupervisor) Restart() {
	logging.Info("Restarting rule runtime")
	s.Stop()
	s.Start()
}

func (s *RuntimeSupervisor) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *RuntimeSupervisor) startProcess(forRunID int) {
	launcherPath := filepath.Join(s.sandboxPath, "uhn-sandbox-launch")
	if _, err := os.Stat(launcherPath); err != nil {
		logging.Error("Sandbox launcher not found, rule runtime will not start",
			"path", launcherPath, "error", err)
		return
	}

	cfg, err := s.buildSandboxConfig()
	if err != nil {
		logging.Error("Failed to build sandbox config", "error", err)
		return
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		logging.Error("Failed to marshal sandbox config", "error", err)
		return
	}

	cmd := exec.Command(launcherPath, "run", "--config", "-")
	cmd.Env = s.buildLauncherEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		logging.Error("Failed to get stdin pipe", "error", err)
		return
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		logging.Error("Failed to get stdout pipe", "error", err)
		return
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		logging.Error("Failed to get stderr pipe", "error", err)
		return
	}

	if err := cmd.Start(); err != nil {
		logging.Error("Failed to start rule runtime", "error", err)
		return
	}

	// Write sandbox config to stdin (newline-terminated so the JSON decoder finishes).
	// Keep stdin open — it's inherited by the sandboxed process for IPC commands.
	cfgJSON = append(cfgJSON, '\n')
	if _, err := stdinPipe.Write(cfgJSON); err != nil {
		logging.Error("Failed to write sandbox config to stdin", "error", err)
		stdinPipe.Close()
		cmd.Process.Kill()
		return
	}

	s.mu.Lock()
	s.process = cmd
	s.stdin = stdinPipe
	s.running = true
	s.mu.Unlock()

	s.writePIDFile(cmd.Process.Pid)

	logging.Info("Rule runtime process started", "pid", cmd.Process.Pid)

	// Log stderr lines
	go s.pipeStderr(stderrPipe)

	// Wait for ready signal from stdout
	readyCh := make(chan bool, 1)
	go s.scanStdout(stdoutPipe, readyCh)

	select {
	case ready := <-readyCh:
		if ready {
			logging.Info("Rule runtime ready", "pid", cmd.Process.Pid)
			s.mu.Lock()
			s.restartAttempts = 0
			s.mu.Unlock()
		} else {
			logging.Error("Rule runtime stdout closed before ready signal")
		}
	case <-time.After(readyTimeout):
		logging.Error("Rule runtime ready timeout, killing process", "pid", cmd.Process.Pid)
		s.terminateProcess(cmd)
		s.mu.Lock()
		s.running = false
		s.process = nil
		s.mu.Unlock()
		s.removePIDFile()
	}

	// Spawn restart watcher
	go s.watchForRestart(cmd, forRunID)
}

func (s *RuntimeSupervisor) scanStdout(pipe io.Reader, readyCh chan<- bool) {
	scanner := bufio.NewScanner(pipe)
	readySent := false
	for scanner.Scan() {
		line := scanner.Text()
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			kind, _ := msg["kind"].(string)
			cmd, _ := msg["cmd"].(string)
			if kind == "event" && cmd == "ready" && !readySent {
				readySent = true
				readyCh <- true
				continue
			}
			if kind == "log" {
				level, _ := msg["level"].(string)
				text, _ := msg["msg"].(string)
				switch level {
				case "error":
					logging.Error("Rule runtime: "+text)
				case "warn":
					logging.Warn("Rule runtime: "+text)
				default:
					logging.Info("Rule runtime: "+text)
				}
				continue
			}
		}
		// Non-JSON or unrecognized JSON — log as info
		logging.Info("Rule runtime stdout", "line", line)
	}
	if !readySent {
		readyCh <- false
	}
}

func (s *RuntimeSupervisor) pipeStderr(pipe io.Reader) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		logging.Error("Rule runtime stderr", "line", scanner.Text())
	}
}

func (s *RuntimeSupervisor) watchForRestart(cmd *exec.Cmd, forRunID int) {
	cmd.Wait()

	s.mu.Lock()
	if s.runID != forRunID {
		// Explicitly stopped or restarted — don't auto-restart
		s.mu.Unlock()
		return
	}
	s.running = false
	s.process = nil
	attempts := s.restartAttempts
	s.restartAttempts++
	currentRunID := s.runID
	s.mu.Unlock()

	s.removePIDFile()

	backoff := initialBackoff * (1 << attempts)
	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	logging.Warn("Rule runtime exited unexpectedly, restarting",
		"attempts", attempts+1, "backoff", backoff)

	time.Sleep(backoff)

	// Re-check runID after sleep — may have been stopped during backoff
	s.mu.Lock()
	if s.runID != forRunID {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	s.startProcess(currentRunID)
}

// buildSandboxConfig determines the runtime mode and constructs the SandboxConfig.
func (s *RuntimeSupervisor) buildSandboxConfig() (*config.SandboxConfig, error) {
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	mode := s.detectMode()
	logging.Info("Rule runtime mode detected", "mode", mode)

	cfg := &config.SandboxConfig{
		Cwd:       "/uhn-runtime/packages/uhn-rule-runtime",
		Env:       []string{"PATH=/uhn-node/bin:/usr/bin:/bin", "HOME=/tmp", "TZ=" + getenvDefault("TZ", "UTC")},
		Limits:    &config.Limits{MemoryBytes: 512 * 1024 * 1024, MaxPids: 254},
		RunAsUser: currentUser.Username,
		Network:   config.NetworkLoopback,
	}

	blueprintPath := "/uhn-workspace/blueprint/active"

	switch mode {
	case "debug":
		cfg.Command = "pnpm"
		cfg.Args = []string{
			"tsx",
			"--tsconfig", "/uhn-runtime/packages/uhn-rule-runtime/tsconfig.json",
			"--inspect=127.0.0.1:9250",
			"/uhn-runtime/packages/uhn-rule-runtime/src/rule-runtime.ts",
			blueprintPath, "edge",
		}
		cfg.Network = config.NetworkDebugAttach
		cfg.DebugListen = "0.0.0.0:9250"

	case "dev":
		cfg.Command = "pnpm"
		cfg.Args = []string{
			"tsx",
			"--tsconfig", "/uhn-runtime/packages/uhn-rule-runtime/tsconfig.json",
			"/uhn-runtime/packages/uhn-rule-runtime/src/rule-runtime.ts",
			blueprintPath, "edge",
		}

	default: // prod
		cfg.Command = "node"
		cfg.Args = []string{
			"/uhn-runtime/packages/uhn-rule-runtime/dist/rule-runtime.js",
			blueprintPath, "edge",
		}
	}

	return cfg, nil
}

func (s *RuntimeSupervisor) detectMode() string {
	if os.Getenv("UHN_RUNTIME_MODE") == "debug" {
		return "debug"
	}

	if s.runtimePath != "" {
		tsPath := filepath.Join(s.runtimePath, "packages", "uhn-rule-runtime", "src", "rule-runtime.ts")
		if _, err := os.Stat(tsPath); err == nil {
			return "dev"
		}
	}

	return "prod"
}

func (s *RuntimeSupervisor) buildLauncherEnv() []string {
	env := os.Environ()
	// Ensure critical paths are set
	ensureEnv := map[string]string{
		"UHN_WORKSPACE_PATH": s.workspacePath,
		"UHN_SANDBOX_PATH":   s.sandboxPath,
		"UHN_NODE_PATH":      s.nodePath,
	}
	if s.runtimePath != "" {
		ensureEnv["UHN_RUNTIME_PATH"] = s.runtimePath
	}

	for key, val := range ensureEnv {
		found := false
		prefix := key + "="
		for i, e := range env {
			if strings.HasPrefix(e, prefix) {
				env[i] = key + "=" + val
				found = true
				break
			}
		}
		if !found {
			env = append(env, key+"="+val)
		}
	}
	return env
}

func (s *RuntimeSupervisor) terminateProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}

	// Send SIGTERM to process group
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		logging.Debug("Rule runtime process terminated gracefully")
	case <-time.After(shutdownGracePeriod):
		logging.Warn("Rule runtime did not exit after SIGTERM, sending SIGKILL")
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}

func (s *RuntimeSupervisor) writePIDFile(pid int) {
	pidPath := filepath.Join(s.workspacePath, pidFileName)
	os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0644)
}

func (s *RuntimeSupervisor) removePIDFile() {
	pidPath := filepath.Join(s.workspacePath, pidFileName)
	os.Remove(pidPath)
}

func (s *RuntimeSupervisor) killOrphan() {
	pidPath := filepath.Join(s.workspacePath, pidFileName)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		os.Remove(pidPath)
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return
	}

	// Check if process is still alive
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pidPath)
		return
	}

	logging.Warn("Killing orphan rule runtime process", "pid", pid)
	syscall.Kill(-pid, syscall.SIGKILL)
	os.Remove(pidPath)
}

// HasActiveBlueprint checks if a previously downloaded blueprint exists.
func (s *RuntimeSupervisor) HasActiveBlueprint() bool {
	versionPath := filepath.Join(s.workspacePath, "blueprint", "version.json")
	activePath := filepath.Join(s.workspacePath, "blueprint", "active")

	if _, err := os.Stat(versionPath); err != nil {
		return false
	}
	if _, err := os.Stat(activePath); err != nil {
		return false
	}
	return true
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
