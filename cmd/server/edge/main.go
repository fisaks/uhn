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
	mqttURL := util.GetEnvDefault("MQTT_URL", "tcp://localhost:1883")
	path := util.GetEnvDefault("EDGE_CONFIG_PATH", "/etc/uhn/edge-config.json")
	edgeName := util.GetEnvDefault("EDGE_NAME", "edge1")
	topicPrefix := "uhn/" + edgeName

	logging.Init()
	cfg, err := config.LoadEdgeConfig(path)
	if err != nil {
		logging.Fatal("Edge config error", "error", err)
	}

	logging.Info("Loaded config",
		"buses", len(cfg.Buses),
		"pollMs", cfg.PollIntervalMs,
	)
	catalog := catalog.NewEdgeCatalog(cfg)
	keyPair,err := encrypt.NewEdgeKeyPair(edgeName, path)
	if err != nil {
		logging.Fatal("Failed to load or create edge key pair", "error", err)
	}
	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	edgeBroker := messaging.NewEdgeBroker(messaging.BrokerConfig{
		BrokerURL:        mqttURL,
		ClientName:       edgeName,
		TopicPrefix:      topicPrefix,
		ConnectTimeout:   10 * time.Second,
		PublishTimeout:   5 * time.Second,
		SubscribeTimeout: 5 * time.Second,
	}, catalog, time.Duration(cfg.HeartbeatInterval)*time.Second)

	edgeBroker.AddOnConnectPublisher("identity", keyPair)
	edgeBroker.Connect(ctx)
	defer edgeBroker.Close(ctx)

	workspacePath := util.GetEnvDefault("UHN_WORKSPACE_PATH", "")

	if workspacePath != "" {
		// Create IPC bridge and signal tracker
		ipcBridge := runtime.NewIPCBridge(edgeName, cfg.DevicesByName)
		ipcBridge.SetBroker(edgeBroker)
		signalTracker := runtime.NewSignalTracker()

		// Create supervisor with IPC bridge
		supervisor := runtime.NewRuntimeSupervisor(workspacePath, edgeName, ipcBridge)
		supervisor.SetBroker(edgeBroker)
		defer supervisor.Stop(ctx)

		// Wire blueprint downloader (same as before)
		bpDownloader := blueprint.NewBlueprintDownloader(edgeName, keyPair, workspacePath)
		bpDownloader.SetBroker(edgeBroker)
		edgeBroker.AddOnConnectPublisher("blueprint", bpDownloader)
		bpDownloader.OnBlueprintReady = func() { supervisor.Restart(ctx) }
		bpDownloader.OnBlueprintDeactivated = func() { supervisor.Stop(ctx) }

		edgeBroker.SubscribeMaster(ctx, "identity", messaging.AtLeastOnce, bpDownloader.IdentitySubscriber())
		edgeBroker.SubscribeMaster(ctx, "blueprint/activated", messaging.AtLeastOnce, bpDownloader.BlueprintSubscriber())

		// Subscribe to signal/state/+ for incoming signals from master
		signalSub := runtime.NewSignalSubscriber(ipcBridge, signalTracker)
		edgeBroker.Subscribe(ctx, "signal/state/+", messaging.AtLeastOnce, signalSub)

		// Timer MQTT: publish timer state and receive timer commands from master
		timerPublisher := runtime.NewTimerPublisher(edgeBroker, signalTracker)
		ipcBridge.SetTimerPublisher(timerPublisher)

		timerCmdSub := runtime.NewTimerCmdSubscriber(ipcBridge, signalTracker)
		edgeBroker.Subscribe(ctx, "timer/cmd/+", messaging.AtLeastOnce, timerCmdSub)

		muteCmdSub := runtime.NewMuteCmdSubscriber(ipcBridge)
		edgeBroker.Subscribe(ctx, "mute/cmd", messaging.AtLeastOnce, muteCmdSub)

		// Subscribe to timer/state/+ to capture retained timer state for restoration on restart
		timerStateSub := runtime.NewTimerStateSubscriber(signalTracker, edgeBroker)
		timerStateSub.Subscribe(ctx)
		ipcBridge.SetTimerStateSubscriber(timerStateSub)

		// Wrap publisher so pollers feed state to both IPC bridge and MQTT
		bridgedPublisher := runtime.NewBridgedPublisher(edgeBroker, ipcBridge)

		// Create pollers with bridged publisher (must happen before action handler)
		pollers, err := poller.NewBusPollers(cfg, bridgedPublisher)
		if err != nil {
			logging.Fatal("poller init", "error", err)
		}

		// Create and set action handler (needs pollers)
		actionHandler := runtime.NewEdgeActionHandler(edgeName, pollers, edgeBroker, ipcBridge, signalTracker)
		ipcBridge.SetActionHandler(actionHandler)

		// Wrap pollers with system command handler so cmd topic reaches both
		sysHandler := runtime.NewSystemCommandHandler(supervisor, edgeBroker, pollers)
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
		logging.Info("UHN_WORKSPACE_PATH not set, blueprint downloader and rule runtime not activated")
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
