package runtime

import (
	"encoding/binary"
	"math"
	"strconv"

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

// extractRegisters iterates over the register range, decodes values according
// to the range's Type (uint16, int16, float32, uint32, int32), and maps them
// to resource IDs via the resource map.
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
	width := regRange.RegisterWidth() // 1 or 2 registers per value

	for i := 0; i < count; i += width {
		byteOffset := i * 2

		if byteOffset+width*2-1 >= len(data) {
			break
		}

		reg := start + i
		resourceID, ok := resourceMap.LookupResourceID(deviceName, resourceType, reg)
		if !ok {
			continue
		}

		var value any
		switch regRange.Type {
		case "int16":
			value = int(int16(binary.BigEndian.Uint16(data[byteOffset:])))
		case "float32":
			bits := binary.BigEndian.Uint32(data[byteOffset:])
			f32 := math.Float32frombits(bits)
			// Round-trip through shortest-representation string to trim
			// float64 noise (e.g. 72.19999694824219 → 72.2).
			rounded, _ := strconv.ParseFloat(strconv.FormatFloat(float64(f32), 'f', -1, 32), 64)
			value = rounded
		case "uint32":
			value = int(binary.BigEndian.Uint32(data[byteOffset:]))
		case "int32":
			value = int(int32(binary.BigEndian.Uint32(data[byteOffset:])))
		default: // "", "uint16"
			value = int(binary.BigEndian.Uint16(data[byteOffset:]))
		}

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
