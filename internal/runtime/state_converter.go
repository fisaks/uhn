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

	// Analog inputs
	if spec.AnalogInputs != nil && len(state.AnalogInputs) > 0 {
		out = extractRegisters(out, state.AnalogInputs, spec.AnalogInputs,
			state.Name, "analogInput", resourceMap, timestamp)
	}

	// Analog outputs
	if spec.AnalogOutputs != nil && len(state.AnalogOutputs) > 0 {
		out = extractRegisters(out, state.AnalogOutputs, spec.AnalogOutputs,
			state.Name, "analogOutput", resourceMap, timestamp)
	}

	return out
}

// extractRegisters iterates over the register range, extracts 16-bit big-endian
// values, and maps them to resource IDs via the resource map.
func extractRegisters(
	out []RuntimeResourceState,
	data []byte,
	regRange *config.Range,
	deviceName string,
	resourceType string,
	resourceMap *ResourceMap,
	timestamp int64,
) []RuntimeResourceState {
	start := int(regRange.Start)
	count := int(regRange.Count)

	for i := 0; i < count; i++ {
		byteOffset := i * 2

		if byteOffset+1 >= len(data) {
			break
		}

		reg := start + i
		resourceID, ok := resourceMap.LookupResourceID(deviceName, resourceType, reg)
		if !ok {
			continue
		}

		value := int(data[byteOffset])<<8 | int(data[byteOffset+1]) // big-endian uint16

		out = append(out, RuntimeResourceState{
			ResourceID: resourceID,
			Value:      value,
			Timestamp:  timestamp,
		})
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
