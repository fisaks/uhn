package main

// cSpell:ignore mqtt modbusTCP mymqtt modbus
import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fisaks/uhn/internal/blueprint"
	"github.com/fisaks/uhn/internal/catalog"
	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/encrypt"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/poller"
	"github.com/fisaks/uhn/internal/runtime"
	"github.com/fisaks/uhn/internal/util"
)

func main() {
	cfgPath := util.GetEnvDefault("UHN_EDGE_CONFIG_PATH", "/etc/uhn/edge-config.json")

	// Minimal logger for config loading errors (re-initialized below with resolved config)
	logging.Init()

	cfg, err := config.LoadEdgeConfig(cfgPath)
	if err != nil {
		logging.Fatal("Edge config error", "error", err)
	}

	// Resolve final config: env var > config file > defaults
	resolvedConfig := config.ResolveEdgeConfig(cfgPath, cfg.Edge)

	if err := config.ValidateEdgeName(resolvedConfig.Name); err != nil {
		logging.Fatal("Invalid edge name", "error", err)
	}

	// Re-initialize logger with resolved log level and format
	logging.InitWithConfig(resolvedConfig.LogLevel, resolvedConfig.LogFormat)

	logging.Info("Loaded config",
		"edge", resolvedConfig.Name,
		"mqtt", resolvedConfig.MqttURL,
		"buses", len(cfg.Buses),
		"pollMs", cfg.PollIntervalMs,
		"runtimeMode", resolvedConfig.RuntimeMode,
		"debugPort", resolvedConfig.DebugPort,
	)

	topicPrefix := "uhn/" + resolvedConfig.Name
	catalog := catalog.NewEdgeCatalog(cfg)
	keyPair, err := encrypt.NewEdgeKeyPair(resolvedConfig.Name, cfgPath)
	if err != nil {
		logging.Fatal("Failed to load or create edge key pair", "error", err)
	}
	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	edgeBroker := messaging.NewEdgeBroker(messaging.BrokerConfig{
		BrokerURL:        resolvedConfig.MqttURL,
		ClientName:       resolvedConfig.Name,
		TopicPrefix:      topicPrefix,
		ConnectTimeout:   10 * time.Second,
		PublishTimeout:   5 * time.Second,
		SubscribeTimeout: 5 * time.Second,
	}, catalog, time.Duration(cfg.HeartbeatInterval)*time.Second)

	edgeBroker.AddOnConnectPublisher("identity", keyPair)
	edgeBroker.Connect(ctx)
	defer edgeBroker.Close(ctx)

	if resolvedConfig.WorkspacePath != "" && resolvedConfig.RuntimePath != "" {
		// Create IPC bridge and signal tracker
		ipcBridge := runtime.NewIPCBridge(resolvedConfig.Name, cfg.DevicesByName)
		ipcBridge.SetBroker(edgeBroker)
		signalTracker := runtime.NewSignalTracker()

		// Create supervisor with resolved config
		supervisor := runtime.NewRuntimeSupervisor(resolvedConfig, ipcBridge)
		supervisor.SetBroker(edgeBroker)
		defer supervisor.Stop(ctx)

		// Wire blueprint downloader (same as before)
		bpDownloader := blueprint.NewBlueprintDownloader(resolvedConfig.Name, keyPair, resolvedConfig.WorkspacePath)
		bpDownloader.SetBroker(edgeBroker)
		edgeBroker.AddOnConnectPublisher("blueprint", bpDownloader)
		bpDownloader.OnBlueprintReady = func() { supervisor.Restart(ctx) }
		bpDownloader.OnBlueprintDeactivated = func() { supervisor.Stop(ctx) }

		edgeBroker.SubscribeMaster(ctx, "identity", messaging.AtLeastOnce, bpDownloader.IdentitySubscriber())
		edgeBroker.SubscribeMaster(ctx, "blueprint/activated", messaging.AtLeastOnce, bpDownloader.BlueprintSubscriber())

		// Subscribe to resource/signal/+ for incoming signals from master
		resourceSignalSub := runtime.NewResourceSignalSubscriber(ipcBridge, signalTracker)
		edgeBroker.Subscribe(ctx, "resource/signal/+", messaging.AtLeastOnce, resourceSignalSub)

		// Logical resource MQTT: publish state and receive commands from master
		logicalResourceStatePublisher := runtime.NewLogicalResourceStatePublisher(edgeBroker, signalTracker)
		ipcBridge.SetLogicalResourceStatePublisher(logicalResourceStatePublisher)

		resourceCmdSub := runtime.NewResourceCmdSubscriber(ipcBridge, signalTracker)
		edgeBroker.Subscribe(ctx, "resource/cmd/+", messaging.AtLeastOnce, resourceCmdSub)

		muteCmdSub := runtime.NewMuteCmdSubscriber(ipcBridge)
		edgeBroker.Subscribe(ctx, "mute/cmd", messaging.AtLeastOnce, muteCmdSub)

		// Subscribe to resource/state/+ to capture retained state for restoration on restart
		logicalResourceStateSub := runtime.NewLogicalResourceStateSubscriber(signalTracker, edgeBroker)
		logicalResourceStateSub.Subscribe(ctx)
		ipcBridge.SetLogicalResourceStateSubscriber(logicalResourceStateSub)

		// Wrap publisher so pollers feed state to both IPC bridge and MQTT
		bridgedPublisher := runtime.NewBridgedPublisher(edgeBroker, ipcBridge)

		// Create pollers with bridged publisher (must happen before action handler)
		pollers, err := poller.NewBusPollers(cfg, bridgedPublisher)
		if err != nil {
			logging.Fatal("poller init", "error", err)
		}

		// Create and set action handler (needs pollers)
		actionHandler := runtime.NewEdgeActionHandler(resolvedConfig.Name, pollers, edgeBroker, ipcBridge, signalTracker)
		ipcBridge.SetActionHandler(actionHandler)

		// Wrap pollers with system command handler so cmd topic reaches both
		sysHandler := runtime.NewSystemCommandHandler(resolvedConfig, supervisor, edgeBroker, pollers)
		edgeBroker.AddOnConnectPublisher("system-config", sysHandler)
		sysHandler.PublishConfig(ctx)

		edgeBroker.StartEdgeSubscriber(ctx, sysHandler)

		// Start all bus pollers
		pollers.StartAllPollers(ctx)
		defer pollers.StopAllPollers()

		// Auto-start if a blueprint was previously downloaded
		if supervisor.HasActiveBlueprint() {
			logging.Info("Found existing active blueprint, starting rule runtime")
			supervisor.Start(ctx)
		}
	} else {
		logging.Info("UHN_WORKSPACE_PATH or UHN_RUNTIME_PATH not set, blueprint downloader and rule runtime not activated")
		edgeBroker.Publish(ctx, "runtime/status", messaging.AtLeastOnce, true, []byte("unconfigured"))
		edgeBroker.Publish(ctx, "runtime/rules", messaging.AtLeastOnce, true, []byte{})
		edgeBroker.Publish(ctx, "blueprint/activated", messaging.AtLeastOnce, true, []byte{})

		pollers, err := poller.NewBusPollers(cfg, edgeBroker)
		if err != nil {
			logging.Fatal("poller init", "error", err)
		}
		edgeBroker.StartEdgeSubscriber(ctx, pollers)

		// Start all bus pollers
		pollers.StartAllPollers(ctx)
		defer pollers.StopAllPollers()
	}

	// Wait for SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigCh
	logging.Info("Shutting down", "signal", s)

	// Give pollers a moment to exit cleanly (they honor ctx)
	cancel()
	time.Sleep(200 * time.Millisecond)
	logging.Info("bye")
}
