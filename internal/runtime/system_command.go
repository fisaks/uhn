package runtime

import (
	"context"
	"encoding/json"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/uhn"
)

var systemActions = map[string]bool{
	"stopRuntime":    true,
	"startRuntime":   true,
	"restartRuntime": true,
	"setLogLevel":    true,
	"setRunMode":     true,
}

type systemConfig struct {
	LogLevel  string `json:"logLevel"`
	RunMode   string `json:"runMode"`
	DebugPort int    `json:"debugPort"`
}

// SystemCommandHandler intercepts system commands from the cmd topic and
// delegates everything else (resync, device commands) to the delegate subscriber.
type SystemCommandHandler struct {
	supervisor    *RuntimeSupervisor
	broker        messaging.Broker
	delegate      uhn.EdgeSubscriber
	runMode       string
	debugPort     int
	workspacePath string
}

func NewSystemCommandHandler(resolvedConfig *config.ResolvedEdgeConfig, supervisor *RuntimeSupervisor, broker messaging.Broker, delegate uhn.EdgeSubscriber) *SystemCommandHandler {
	return &SystemCommandHandler{
		supervisor:    supervisor,
		broker:        broker,
		delegate:      delegate,
		runMode:       resolvedConfig.RuntimeMode,
		debugPort:     resolvedConfig.DebugPort,
		workspacePath: resolvedConfig.WorkspacePath,
	}
}

// OnDeviceCommand delegates to the delegate subscriber.
func (h *SystemCommandHandler) OnDeviceCommand(ctx context.Context, command uhn.IncomingDeviceCommand) error {
	return h.delegate.OnDeviceCommand(ctx, command)
}

// OnCommand handles system commands or delegates to the delegate subscriber.
func (h *SystemCommandHandler) OnCommand(ctx context.Context, command uhn.IncomingCommand) error {
	if systemActions[command.Action] {
		return h.handleSystemCommand(ctx, command)
	}
	return h.delegate.OnCommand(ctx, command)
}

func (h *SystemCommandHandler) handleSystemCommand(ctx context.Context, cmd uhn.IncomingCommand) error {
	switch cmd.Action {
	case "stopRuntime":
		logging.Info("System command: stopping runtime")
		h.supervisor.Stop(ctx)
	case "startRuntime":
		logging.Info("System command: starting runtime")
		h.supervisor.Start(ctx)
	case "restartRuntime":
		logging.Info("System command: restarting runtime")
		h.supervisor.Restart(ctx)
	case "setLogLevel":
		return h.handleSetLogLevel(ctx, cmd.Payload)
	case "setRunMode":
		return h.handleSetRunMode(ctx, cmd.Payload)
	}
	return nil
}

func (h *SystemCommandHandler) handleSetLogLevel(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		LogLevel string `json:"logLevel"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		logging.Warn("Invalid setLogLevel payload", "error", err)
		return err
	}
	level, ok := logging.ParseLevel(p.LogLevel)
	if !ok {
		logging.Warn("Unknown log level", "level", p.LogLevel)
		return nil
	}
	logging.SetLevel(level)
	logging.Info("Log level changed", "level", p.LogLevel)
	h.persistConfig()
	h.PublishConfig(ctx)
	return nil
}

func (h *SystemCommandHandler) handleSetRunMode(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		RuntimeMode string `json:"runtimeMode"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		logging.Warn("Invalid setRunMode payload", "error", err)
		return err
	}
	if p.RuntimeMode != "normal" && p.RuntimeMode != "debug" {
		logging.Warn("Unknown run mode", "mode", p.RuntimeMode)
		return nil
	}
	if p.RuntimeMode == h.runMode {
		return nil
	}
	h.runMode = p.RuntimeMode
	h.supervisor.SetRuntimeMode(p.RuntimeMode)
	logging.Info("Run mode changed, restarting runtime", "mode", p.RuntimeMode)
	h.persistConfig()
	h.PublishConfig(ctx)
	h.supervisor.Restart(ctx)
	return nil
}

func (h *SystemCommandHandler) persistConfig() {
	if h.workspacePath == "" {
		return
	}
	if err := config.SavePersistedRuntimeConfig(h.workspacePath, logging.GetLevelName(), h.runMode); err != nil {
		logging.Warn("Failed to persist runtime config", "error", err)
	}
}

// PublishConfig publishes the current system config as a retained message.
func (h *SystemCommandHandler) PublishConfig(ctx context.Context) {
	cfg := systemConfig{
		LogLevel:  logging.GetLevelName(),
		RunMode:   h.runMode,
		DebugPort: h.debugPort,
	}
	if err := h.broker.PublishJSON(ctx, "system/config", messaging.AtLeastOnce, true, cfg); err != nil {
		logging.Error("Failed to publish system config", "error", err)
	}
}

// OnConnectPublish implements messaging.OnConnectPublisher to republish config on reconnect.
func (h *SystemCommandHandler) OnConnectPublish(ctx context.Context) (*messaging.ConnectMessage, error) {
	cfg := systemConfig{
		LogLevel:  logging.GetLevelName(),
		RunMode:   h.runMode,
		DebugPort: h.debugPort,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return &messaging.ConnectMessage{
		Topic:        "system/config",
		Qos:          messaging.AtLeastOnce,
		Retain:       true,
		PayloadBytes: data,
	}, nil
}
