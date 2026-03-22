package zigbee

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/uhn"
)

// ZigbeeTransport manages the connection to a Zigbee2MQTT instance.
// It subscribes to Z2M MQTT topics, discovers devices from bridge/devices,
// and publishes state updates to the UHN state model.
// Only publishes properties that exist in the current blueprint (ResourceMap).
// Implements uhn.DeviceTransport.
type ZigbeeTransport struct {
	cfg            *config.ZigbeeAdapterConfig
	stateUpdater   uhn.StateUpdater
	resourceLookup uhn.ResourceLookup
	broker         messaging.Broker

	// configDevices is the set of device names from the edge config.
	// Only these devices are processed — state messages from unlisted
	// devices are ignored. Individual properties are further filtered
	// by the ResourceMap (only blueprint-exported properties get published).
	configDevices map[string]*config.ZigbeeDeviceConfig

	// Discovered devices and their property-to-type mappings.
	// Written on bridge/devices message, read on per-device state messages.
	devicesMu sync.RWMutex
	devices   map[string]*z2mDevice // Z2M friendly name → device metadata

	// Last known values for change detection. Key: "device:property" → value.
	lastValues   map[string]any
	lastValuesMu sync.Mutex

	// Cached last raw state blob per device for replay after ResourceMap is built.
	lastBlobs   map[string][]byte // device name → raw JSON
	lastBlobsMu sync.Mutex

	// settingsOnce ensures device settings (optimistic, etc.) are only sent once.
	settingsOnce sync.Once

	// drivers is populated during discovery and registered externally
	driversMu sync.RWMutex
	drivers   map[string]*ZigbeeDriver

	// Callback when device list changes (driver registration + catalog republish)
	onDevicesDiscovered func()

	cancel context.CancelFunc
	done   chan struct{}
}

// z2mDevice holds metadata for a single Z2M device.
type z2mDevice struct {
	FriendlyName string
	IEEEAddress  string
	// propertyTypes maps Z2M property name → UHN resource type.
	// Only populated for properties we can map (binary, numeric, enum).
	propertyTypes map[string]string
	// writableProperties lists property names that accept /set commands.
	writableProperties map[string]bool
}

// NewZigbeeTransport creates a transport for one Z2M adapter.
func NewZigbeeTransport(
	cfg *config.ZigbeeAdapterConfig,
	stateUpdater uhn.StateUpdater,
	resourceLookup uhn.ResourceLookup,
	broker messaging.Broker,
) *ZigbeeTransport {
	cfgDevices := make(map[string]*config.ZigbeeDeviceConfig, len(cfg.Devices))
	for _, dev := range cfg.Devices {
		cfgDevices[dev.Name] = dev
	}

	return &ZigbeeTransport{
		cfg:            cfg,
		stateUpdater:   stateUpdater,
		resourceLookup: resourceLookup,
		broker:         broker,
		configDevices:  cfgDevices,
		devices:        make(map[string]*z2mDevice),
		lastValues:     make(map[string]any),
		lastBlobs:      make(map[string][]byte),
		drivers:        make(map[string]*ZigbeeDriver),
		done:           make(chan struct{}),
	}
}

// SetOnDevicesDiscovered sets the callback invoked after bridge/devices is
// processed and the device list has been updated. Used by main.go to register
// drivers (via GetDrivers()) and republish the catalog.
func (t *ZigbeeTransport) SetOnDevicesDiscovered(fn func()) {
	t.onDevicesDiscovered = fn
}

// Start begins the transport lifecycle. Blocks until ctx is cancelled.
func (t *ZigbeeTransport) Start(ctx context.Context) {
	ctx, t.cancel = context.WithCancel(ctx)
	defer close(t.done)

	base := t.cfg.BaseTopic

	// Subscribe to Z2M bridge topics using raw MQTT (Z2M publishes to its own
	// topic namespace, not under the uhn/{edge}/ prefix).
	t.broker.SubscribeRaw(ctx, base+"/bridge/devices", messaging.AtLeastOnce, &z2mSubscriber{
		handler: func(ctx context.Context, topic string, payload []byte) {
			t.handleZ2MBridgeDevices(ctx, payload)
		},
	})

	t.broker.SubscribeRaw(ctx, base+"/bridge/state", messaging.AtLeastOnce, &z2mSubscriber{
		handler: func(ctx context.Context, topic string, payload []byte) {
			t.handleZ2MBridgeState(payload)
		},
	})

	// Subscribe to all device state topics: {baseTopic}/+
	// This catches {baseTopic}/{friendlyName} messages.
	// We filter to known devices in the handler.
	t.broker.SubscribeRaw(ctx, base+"/+", messaging.AtLeastOnce, &z2mSubscriber{
		handler: func(ctx context.Context, topic string, payload []byte) {
			t.handleZ2MDeviceState(ctx, topic, payload)
		},
	})

	// Subscribe to availability: {baseTopic}/{friendlyName}/availability
	t.broker.SubscribeRaw(ctx, base+"/+/availability", messaging.AtLeastOnce, &z2mSubscriber{
		handler: func(ctx context.Context, topic string, payload []byte) {
			t.handleZ2MDeviceAvailability(ctx, topic, payload)
		},
	})

	logging.Info("Zigbee transport started", "adapter", t.cfg.Name, "baseTopic", base)

	<-ctx.Done()
}

// Stop signals the transport to shut down.
func (t *ZigbeeTransport) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	<-t.done
}

// GetDrivers returns the current set of discovered drivers.
func (t *ZigbeeTransport) GetDrivers() map[string]*ZigbeeDriver {
	t.driversMu.RLock()
	defer t.driversMu.RUnlock()
	result := make(map[string]*ZigbeeDriver, len(t.drivers))
	for k, v := range t.drivers {
		result[k] = v
	}
	return result
}

// GetDeviceInfo returns catalog-relevant information for all discovered devices.
type Z2MDeviceInfo struct {
	FriendlyName string
	IEEEAddress  string
	Properties   []Z2MPropertyInfo
}

type Z2MPropertyInfo struct {
	Name     string
	Type     string // UHN resource type: digitalOutput, digitalInput, analogOutput, analogInput
	Writable bool
}

func (t *ZigbeeTransport) GetDeviceInfos() []Z2MDeviceInfo {
	t.devicesMu.RLock()
	defer t.devicesMu.RUnlock()

	var infos []Z2MDeviceInfo
	for name, dev := range t.devices {
		// Only include devices listed in edge config
		if _, configured := t.configDevices[name]; !configured {
			continue
		}
		info := Z2MDeviceInfo{
			FriendlyName: dev.FriendlyName,
			IEEEAddress:  dev.IEEEAddress,
		}
		for prop, typ := range dev.propertyTypes {
			info.Properties = append(info.Properties, Z2MPropertyInfo{
				Name:     prop,
				Type:     typ,
				Writable: dev.writableProperties[prop],
			})
		}
		infos = append(infos, info)
	}
	return infos
}

// --- Bridge message handlers ---

// handleZ2MBridgeDevices processes the bridge/devices retained message.
// Discovers devices and builds property-to-type mappings from exposes.
func (t *ZigbeeTransport) handleZ2MBridgeDevices(ctx context.Context, payload []byte) {
	var rawDevices []json.RawMessage
	if err := json.Unmarshal(payload, &rawDevices); err != nil {
		logging.Error("Zigbee: failed to parse bridge/devices", "adapter", t.cfg.Name, "error", err)
		return
	}

	newDevices := make(map[string]*z2mDevice, len(t.configDevices))

	for _, raw := range rawDevices {
		var meta struct {
			FriendlyName string `json:"friendly_name"`
			IEEEAddress  string `json:"ieee_address"`
			Type         string `json:"type"`
			Definition   *struct {
				Exposes []json.RawMessage `json:"exposes"`
			} `json:"definition"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			logging.Warn("Zigbee: failed to parse device entry", "adapter", t.cfg.Name, "error", err)
			continue
		}

		// Skip the coordinator and devices not in edge config
		if meta.Type == "Coordinator" {
			continue
		}
		if _, configured := t.configDevices[meta.FriendlyName]; !configured {
			logging.Debug("Zigbee: skipping unconfigured device",
				"adapter", t.cfg.Name, "device", meta.FriendlyName)
			continue
		}

		dev := &z2mDevice{
			FriendlyName:       meta.FriendlyName,
			IEEEAddress:        meta.IEEEAddress,
			propertyTypes:      make(map[string]string),
			writableProperties: make(map[string]bool),
		}

		if meta.Definition != nil {
			processExposes(meta.Definition.Exposes, dev, "")
		}

		newDevices[meta.FriendlyName] = dev
		logging.Info("Zigbee: discovered device",
			"adapter", t.cfg.Name,
			"device", meta.FriendlyName,
			"ieee", meta.IEEEAddress,
			"properties", len(dev.propertyTypes))
	}

	t.devicesMu.Lock()
	t.devices = newDevices
	t.devicesMu.Unlock()

	// Create drivers for devices with writable properties
	t.driversMu.Lock()
	for name, dev := range newDevices {
		if len(dev.writableProperties) > 0 {
			driver := NewZigbeeDriver(t.broker, t.cfg.BaseTopic, name, dev.writableProperties)
			t.drivers[name] = driver
			logging.Info("Zigbee: driver registered",
				"adapter", t.cfg.Name,
				"device", name,
				"writableProperties", len(dev.writableProperties))
		}
	}
	t.driversMu.Unlock()

	// Apply per-device Z2M settings once (not on every bridge/devices update,
	// since changing settings triggers Z2M to re-publish bridge/devices → loop)
	t.settingsOnce.Do(func() {
		t.applyDeviceSettings(ctx)
	})

	// Notify catalog republish — fires for config-listed devices
	if t.onDevicesDiscovered != nil {
		t.onDevicesDiscovered()
	}
}

// ReplayCachedState replays cached device state blobs through the normal
// filtered processing path. Called after the ResourceMap is built so
// initial Z2M state (from retained messages) reaches the runtime.
func (t *ZigbeeTransport) ReplayCachedState(ctx context.Context) {
	t.lastBlobsMu.Lock()
	blobs := make(map[string][]byte, len(t.lastBlobs))
	for k, v := range t.lastBlobs {
		blobs[k] = v
	}
	t.lastBlobsMu.Unlock()

	if len(blobs) == 0 {
		return
	}

	replayed := 0
	for deviceName, payload := range blobs {
		t.devicesMu.RLock()
		dev, known := t.devices[deviceName]
		t.devicesMu.RUnlock()
		if !known {
			continue
		}

		var blob map[string]any
		if err := json.Unmarshal(payload, &blob); err != nil {
			continue
		}

		t.processStateBlob(ctx, deviceName, dev, blob, "", currentTimeMs())
		replayed++
	}

	if replayed > 0 {
		logging.Info("Zigbee: replayed cached state after ResourceMap built",
			"adapter", t.cfg.Name, "devices", replayed)
	}
}

// applyDeviceSettings sends per-device Z2M options from the edge config.
// Currently supports: optimistic.
func (t *ZigbeeTransport) applyDeviceSettings(ctx context.Context) {
	topic := t.cfg.BaseTopic + "/bridge/request/device/options"

	for _, devCfg := range t.cfg.Devices {
		options := make(map[string]any)

		if devCfg.Optimistic != nil {
			options["optimistic"] = *devCfg.Optimistic
		}

		if len(options) == 0 {
			continue
		}

		payload := map[string]any{
			"id":      devCfg.Name,
			"options": options,
		}
		data, err := json.Marshal(payload)
		if err != nil {
			logging.Error("Zigbee: failed to marshal device settings",
				"adapter", t.cfg.Name, "device", devCfg.Name, "error", err)
			continue
		}

		if err := t.broker.PublishRaw(ctx, topic, messaging.AtLeastOnce, false, data); err != nil {
			logging.Warn("Zigbee: failed to apply device settings",
				"adapter", t.cfg.Name, "device", devCfg.Name, "error", err)
		} else {
			logging.Info("Zigbee: applied device settings",
				"adapter", t.cfg.Name, "device", devCfg.Name, "options", options)
		}
	}
}

// processExposes recursively walks Z2M expose definitions and populates
// the device's propertyTypes and writableProperties maps.
func processExposes(exposes []json.RawMessage, dev *z2mDevice, prefix string) {
	for _, raw := range exposes {
		var expose struct {
			Type     string            `json:"type"`
			Name     string            `json:"name"`
			Property string            `json:"property"`
			Access   int               `json:"access"`
			Values   []string          `json:"values"`
			Features []json.RawMessage `json:"features"`
		}
		if err := json.Unmarshal(raw, &expose); err != nil {
			continue
		}

		// Recurse into features for all expose types that have them.
		// - "composite": dot-notation prefix (e.g. overload_protection.enable_max_voltage)
		// - "switch", "light", "fan", "cover", "lock", "climate": Z2M "specific" types
		//   that group features — no prefix (features are top-level properties)
		if len(expose.Features) > 0 {
			subPrefix := prefix
			if expose.Type == "composite" {
				subPrefix = expose.Property
				if prefix != "" {
					subPrefix = prefix + "." + expose.Property
				}
			}
			processExposes(expose.Features, dev, subPrefix)
			continue
		}

		// Skip internal Z2M properties
		prop := expose.Property
		if prop == "" {
			prop = expose.Name
		}
		if prop == "" || prop == "update" || prop == "last_seen" {
			continue
		}

		if prefix != "" {
			prop = prefix + "." + prop
		}

		// Map Z2M type + access to UHN resource type
		// Access bits: 1=readable, 2=writable, 4=publishable
		writable := expose.Access&2 != 0
		uhnType := mapExposeToUHNType(expose.Type, writable, expose.Values)
		if uhnType == "" {
			continue
		}

		dev.propertyTypes[prop] = uhnType
		if writable {
			dev.writableProperties[prop] = true
		}
	}
}

// mapExposeToUHNType converts a Z2M expose type + writable flag to a UHN resource type.
// For enum types, checks if the values are ON/OFF which is semantically digital.
func mapExposeToUHNType(z2mType string, writable bool, values []string) string {
	switch z2mType {
	case "binary":
		if writable {
			return "digitalOutput"
		}
		return "digitalInput"
	case "numeric":
		if writable {
			return "analogOutput"
		}
		return "analogInput"
	case "enum":
		// ON/OFF enums (e.g. "state" on smart plugs) are semantically digital
		if isOnOffEnum(values) {
			if writable {
				return "digitalOutput"
			}
			return "digitalInput"
		}
		if writable {
			return "analogOutput"
		}
		// Read-only enums (e.g. power_on_behavior) — no UHN type mapping yet
		return ""
	default:
		return ""
	}
}

// isOnOffEnum returns true if the enum values contain ON and OFF
// (Z2M uses enum with ON/OFF/TOGGLE for switch-type devices).
func isOnOffEnum(values []string) bool {
	hasOn, hasOff := false, false
	for _, v := range values {
		switch v {
		case "ON":
			hasOn = true
		case "OFF":
			hasOff = true
		}
	}
	return hasOn && hasOff
}

func (t *ZigbeeTransport) handleZ2MBridgeState(payload []byte) {
	// Bridge state can be plain text ("online"/"offline") or JSON
	state := strings.TrimSpace(string(payload))

	// Try JSON format first
	var stateObj struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(payload, &stateObj); err == nil && stateObj.State != "" {
		state = stateObj.State
	}

	logging.Info("Zigbee: bridge state changed",
		"adapter", t.cfg.Name,
		"state", state)
}

// handleZ2MDeviceState processes a per-device state message from Z2M.
func (t *ZigbeeTransport) handleZ2MDeviceState(ctx context.Context, topic string, payload []byte) {
	// Extract device name from topic: {baseTopic}/{friendlyName}
	deviceName := extractDeviceName(topic, t.cfg.BaseTopic)
	if deviceName == "" || deviceName == "bridge" {
		return // bridge messages handled separately
	}

	// Only process devices listed in edge config
	if _, configured := t.configDevices[deviceName]; !configured {
		return
	}

	t.devicesMu.RLock()
	dev, known := t.devices[deviceName]
	t.devicesMu.RUnlock()
	if !known {
		return
	}

	// Always cache the raw blob for replay after ResourceMap is built
	t.lastBlobsMu.Lock()
	t.lastBlobs[deviceName] = payload
	t.lastBlobsMu.Unlock()

	// Don't process until ResourceMap exists — state will be replayed
	if t.resourceLookup == nil || !t.resourceLookup.HasResourceMap() {
		return
	}

		// Parse the flat JSON blob
		var blob map[string]any
		if err := json.Unmarshal(payload, &blob); err != nil {
		logging.Warn("Zigbee: failed to parse device state",
			"adapter", t.cfg.Name, "device", deviceName, "error", err)
		return
	}

	timestamp := currentTimeMs()

	t.processStateBlob(ctx, deviceName, dev, blob, "", timestamp)
}

// processStateBlob walks the JSON blob, handling flat and nested (composite) properties.
func (t *ZigbeeTransport) processStateBlob(
	ctx context.Context,
	deviceName string,
	dev *z2mDevice,
	blob map[string]any,
	prefix string,
	timestamp int64,
) {
	for key, value := range blob {
		// Skip Z2M internal objects
		if key == "update" || key == "last_seen" {
			continue
		}

		propName := key
		if prefix != "" {
			propName = prefix + "." + key
		}

		// Check for nested composite
		if nested, ok := value.(map[string]any); ok {
			t.processStateBlob(ctx, deviceName, dev, nested, propName, timestamp)
			continue
		}

		// Check if this property is mapped to a UHN type
		uhnType, mapped := dev.propertyTypes[propName]
		if !mapped {
			continue
		}

		// Filter: only publish properties that exist in the current blueprint.
		// This prevents battery, linkquality, etc. from being published
		// unless the blueprint explicitly exports them.
		if t.resourceLookup != nil && !t.resourceLookup.HasResourceForAddress(deviceName, uhnType, propName) {
			continue
		}

		// Convert Z2M value to UHN value
		uhnValue := convertZ2MValue(value, uhnType)

		// Round analog values based on resource decimals precision.
		// Reduces noise from sensors that report tiny fluctuations.
		if f, ok := uhnValue.(float64); ok && t.resourceLookup != nil {
			decimals := t.resourceLookup.GetDecimalPrecisionForAddress(deviceName, uhnType, propName)
			if decimals >= 0 {
				uhnValue = roundToDecimals(f, decimals)
			}
		}

		// Change detection
		lastKey := deviceName + ":" + propName
		t.lastValuesMu.Lock()
		prev, hasPrev := t.lastValues[lastKey]
		t.lastValues[lastKey] = uhnValue
		t.lastValuesMu.Unlock()

		if hasPrev && prev == uhnValue {
			continue
		}

		// Publish to UHN state model
		t.stateUpdater.UpdatePhysicalStateByAddress(ctx, deviceName, uhnType, propName, uhnValue, timestamp)

		logging.Debug("Zigbee: state update",
			"adapter", t.cfg.Name,
			"device", deviceName,
			"property", propName,
			"type", uhnType,
			"value", uhnValue)
	}
}

// convertZ2MValue converts a Z2M property value to the appropriate UHN value.
func convertZ2MValue(value any, uhnType string) any {
	switch uhnType {
	case "digitalOutput", "digitalInput":
		// Z2M binary: "ON"/"OFF" strings, true/false, or 1/0
		switch v := value.(type) {
		case bool:
			return v
		case string:
			return strings.EqualFold(v, "ON") || strings.EqualFold(v, "true")
		case float64:
			return v != 0
		default:
			return false
		}
	case "analogOutput", "analogInput":
		// Keep numeric values as-is
		switch v := value.(type) {
		case float64:
			return v
		case string:
			// Enum values pass through as strings
			return v
		default:
			return value
		}
	default:
		return value
	}
}

// handleZ2MDeviceAvailability processes per-device availability messages.
func (t *ZigbeeTransport) handleZ2MDeviceAvailability(ctx context.Context, topic string, payload []byte) {
	// Topic: {baseTopic}/{friendlyName}/availability
	prefix := t.cfg.BaseTopic + "/"
	suffix := "/availability"
	if !strings.HasPrefix(topic, prefix) || !strings.HasSuffix(topic, suffix) {
		return
	}
	deviceName := topic[len(prefix) : len(topic)-len(suffix)]
	if deviceName == "" {
		return
	}

	// Only publish availability for devices listed in edge config
	if _, configured := t.configDevices[deviceName]; !configured {
		return
	}

	// Availability can be plain text or JSON {"state": "online"|"offline"}
	state := strings.TrimSpace(string(payload))
	var stateObj struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(payload, &stateObj); err == nil && stateObj.State != "" {
		state = stateObj.State
	}

	// Publish to UHN device availability topic (retained, config-listed devices only)
	availTopic := fmt.Sprintf("device/%s/availability", deviceName)
	availPayload := []byte(state)
	if err := t.broker.Publish(ctx, availTopic, messaging.AtLeastOnce, true, availPayload); err != nil {
		logging.Error("Zigbee: failed to publish device availability",
			"adapter", t.cfg.Name, "device", deviceName, "error", err)
	} else {
		logging.Debug("Zigbee: device availability",
			"adapter", t.cfg.Name, "device", deviceName, "state", state)
	}
}

// --- Helpers ---

// extractDeviceName extracts the Z2M device friendly name from a full MQTT topic.
func extractDeviceName(topic, baseTopic string) string {
	// Topic format: {baseTopic}/{friendlyName}
	prefix := baseTopic + "/"
	if !strings.HasPrefix(topic, prefix) {
		return ""
	}
	remainder := topic[len(prefix):]
	// Only take the first segment (no slashes = leaf device topic)
	if idx := strings.Index(remainder, "/"); idx >= 0 {
		return ""
	}
	return remainder
}

// roundToDecimals rounds a float64 to the specified number of decimal places.
func roundToDecimals(v float64, decimals int) float64 {
	if decimals <= 0 {
		return math.Round(v)
	}
	pow := math.Pow(10, float64(decimals))
	return math.Round(v*pow) / pow
}

func currentTimeMs() int64 {
	return time.Now().UnixMilli()
}

// z2mSubscriber wraps a handler function as a messaging.Subscriber.
type z2mSubscriber struct {
	handler func(ctx context.Context, topic string, payload []byte)
}

func (s *z2mSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	s.handler(ctx, topic, payload)
}
