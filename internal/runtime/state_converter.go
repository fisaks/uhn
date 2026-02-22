package runtime

import (
	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/uhn"
)

// ExtractResourceStates converts raw DeviceState byte arrays into per-resource
// boolean states. This mirrors the master's processPins() logic from
// state-runtime.service.ts.
func ExtractResourceStates(
	state uhn.DeviceState,
	deviceCfg *config.DeviceConfig,
	resourceMap *ResourceMap,
	timestamp int64,
) []RuntimeResourceState {
	var out []RuntimeResourceState

	spec := deviceCfg.CatalogSpec
	if spec == nil {
		return out
	}

	// Digital inputs
	if spec.DigitalInputs != nil && len(state.DigitalInputs) > 0 {
		out = extractPins(out, state.DigitalInputs, spec.DigitalInputs,
			state.Name, "digitalInput", resourceMap, timestamp)
	}

	// Digital outputs
	if spec.DigitalOutputs != nil && len(state.DigitalOutputs) > 0 {
		out = extractPins(out, state.DigitalOutputs, spec.DigitalOutputs,
			state.Name, "digitalOutput", resourceMap, timestamp)
	}

	return out
}

// extractPins iterates over the pin range, extracts individual bit values,
// and maps them to resource IDs via the resource map.
func extractPins(
	out []RuntimeResourceState,
	data []byte,
	pinRange *config.Range,
	deviceName string,
	resourceType string,
	resourceMap *ResourceMap,
	timestamp int64,
) []RuntimeResourceState {
	start := int(pinRange.Start)
	count := int(pinRange.Count)

	for i := 0; i < count; i++ {
		byteIndex := i / 8
		bit := i % 8

		if byteIndex >= len(data) {
			break
		}

		pin := start + i
		resourceID, ok := resourceMap.LookupResourceID(deviceName, resourceType, pin)
		if !ok {
			continue
		}

		value := (data[byteIndex] & (1 << bit)) != 0

		out = append(out, RuntimeResourceState{
			ResourceID: resourceID,
			Value:      value,
			Timestamp:  timestamp,
		})
	}
	return out
}
