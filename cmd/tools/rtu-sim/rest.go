package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/womat/mbserver"
)

type DeviceStateRequest struct {
	DigitalOutputs []uint   `json:"digitalOutputs,omitempty"`
	DigitalInputs  []uint   `json:"digitalInputs,omitempty"`
	AnalogOutputs  []uint16 `json:"analogOutputs,omitempty"`
	AnalogInputs   []uint16 `json:"analogInputs,omitempty"`
}

func StartRestAPI() error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /device/{busId}/{deviceName}", getDeviceStateHandler)
	mux.HandleFunc("PATCH /device/{busId}/{deviceName}", setDeviceStateHandler)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/digitalOutput/{index}", getDigitalOutputStateHandler)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/digitalOutput/{index}", setDigitalOutputStateHandler)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/digitalInput/{index}", getDigitalInputStateHandler)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/digitalInput/{index}", setDigitalInputStateHandler)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/analogOutput/{index}", getAnalogOutputStateHandler)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/analogOutput/{index}", setAnalogOutputStateHandler)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/analogInput/{index}", getAnalogInputStateHandler)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/analogInput/{index}", setAnalogInputStateHandler)

	// toggles
	mux.HandleFunc("POST /device/{busId}/{deviceName}/digitalOutput/{index}/toggle", toggleDigitalOutputHandler)
	mux.HandleFunc("POST /device/{busId}/{deviceName}/digitalInput/{index}/toggle", toggleDigitalInputHandler)

	// press simulation (digital input)
	mux.HandleFunc("POST /device/{busId}/{deviceName}/digitalInput/{index}/press/{mode}", pressDigitalInputHandler)

	log.Println("RTU Simulator REST API listening on :8080")
	return http.ListenAndServe(":8080", mux)

}

/* ------------------------ helpers: json & errors ------------------------ */

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseIndex(w http.ResponseWriter, s string) (int, bool) {
	i, err := strconv.Atoi(s)
	if err != nil || i < 0 {
		fail(w, http.StatusBadRequest, "invalid index")
		return 0, false
	}
	return i, true
}

/* ----------------------- device lookup ------------------- */

func getSimAndDevice(w http.ResponseWriter, busId, deviceName string) (*mbserver.Server, *mbserver.Device, *SimDeviceConfig, bool) {
	simulatorsMu.RLock()
	sim, ok := simulators[busId]
	simulatorsMu.RUnlock()
	if !ok {
		fail(w, http.StatusNotFound, "bus not found")
		return nil, nil, nil, false
	}

	deviceConfigsMu.RLock()
	deviceConfig, ok := deviceConfigs[deviceName]
	deviceConfigsMu.RUnlock()
	if !ok {
		fail(w, http.StatusNotFound, "bus device config not found")
		return nil, nil, nil, false
	}

	dev, ok := sim.Devices[deviceConfig.UnitID]
	if !ok {
		fail(w, http.StatusNotFound, "device not found")
		return nil, nil, nil, false
	}
	return sim, &dev, deviceConfig, true
}

/* ------------------------------ handlers -------------------------------- */

func setDeviceStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")

	var req DeviceStateRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "bad json")
		return
	}

	_, device, _, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	// Copy only provided fields
	if req.DigitalOutputs != nil {
		copy(device.Coils, uintsToBytes(req.DigitalOutputs))
	}
	if req.DigitalInputs != nil {
		copy(device.DiscreteInputs, uintsToBytes(req.DigitalInputs))
	}
	if req.AnalogOutputs != nil {
		copy(device.HoldingRegisters, req.AnalogOutputs)
	}
	if req.AnalogInputs != nil {
		copy(device.InputRegisters, req.AnalogInputs)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func getDeviceStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")

	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	out := DeviceStateRequest{}
	if cfg.DigitalOutputs > 0 {
		out.DigitalOutputs = toJsonByteArray(device.Coils[:cfg.DigitalOutputs])
	}
	if cfg.DigitalInputs > 0 {
		out.DigitalInputs = toJsonByteArray(device.DiscreteInputs[:cfg.DigitalInputs])
	}
	if cfg.AnalogOutputs > 0 {
		out.AnalogOutputs = device.HoldingRegisters[:cfg.AnalogOutputs]
	}
	if cfg.AnalogInputs > 0 {
		out.AnalogInputs = device.InputRegisters[:cfg.AnalogInputs]
	}

	writeJSON(w, http.StatusOK, out)
}

func getDigitalOutputStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}
	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.DigitalOutputs) {
		fail(w, http.StatusBadRequest, "DigitalOutputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint8{"value": device.Coils[idx]})
}

func setDigitalOutputStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}
	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.DigitalOutputs) {
		fail(w, http.StatusBadRequest, "DigitalOutput index out of range")
		return
	}

	var payload struct {
		Value uint8 `json:"value"`
	}
	if err := readJSON(r, &payload); err != nil {
		fail(w, http.StatusBadRequest, "invalid json")
		return
	}

	device.Coils[idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------- digital INPUT (byte) ----------------------

func getDigitalInputStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.DigitalInputs) {
		fail(w, http.StatusBadRequest, "DigitalInputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint8{"value": device.DiscreteInputs[idx]})
}

func setDigitalInputStateHandler(w http.ResponseWriter, r *http.Request) {
	// NOTE: In real Modbus, discrete inputs are read-only. In a simulator we allow setting.
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.DigitalInputs) {
		fail(w, http.StatusBadRequest, "DigitalInputs index out of range")
		return
	}

	var payload struct {
		Value uint8 `json:"value"`
	}
	if err := readJSON(r, &payload); err != nil {
		fail(w, http.StatusBadRequest, "invalid json")
		return
	}
	device.DiscreteInputs[idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------- analog OUTPUT (uint16) ----------------------

func getAnalogOutputStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.AnalogOutputs) {
		fail(w, http.StatusBadRequest, "AnalogOutputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint16{"value": device.HoldingRegisters[idx]})
}

func setAnalogOutputStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.AnalogOutputs) {
		fail(w, http.StatusBadRequest, "AnalogOutputs index out of range")
		return
	}

	var payload struct {
		Value uint16 `json:"value"`
	}
	if err := readJSON(r, &payload); err != nil {
		fail(w, http.StatusBadRequest, "invalid json")
		return
	}
	device.HoldingRegisters[idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------- analog INPUT (uint16) ----------------------

func getAnalogInputStateHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.AnalogInputs) {
		fail(w, http.StatusBadRequest, "AnalogInputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint16{"value": device.InputRegisters[idx]})
}

func setAnalogInputStateHandler(w http.ResponseWriter, r *http.Request) {
	// NOTE: In real Modbus, input registers are read-only. In a simulator we allow setting.
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, device, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.AnalogInputs) {
		fail(w, http.StatusBadRequest, "AnalogInputs index out of range")
		return
	}

	var payload struct {
		Value uint16 `json:"value"`
	}
	if err := readJSON(r, &payload); err != nil {
		fail(w, http.StatusBadRequest, "invalid json")
		return
	}
	device.InputRegisters[idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------- TOGGLE: digital OUTPUT ----------------------

func toggleDigitalOutputHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, dev, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.DigitalOutputs) {
		fail(w, http.StatusBadRequest, "DigitalOutputs index out of range")
		return
	}

	// flip bit 0/1; if values may vary, normalize to 0/1 first:
	dev.Coils[idx] ^= 1
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "value": dev.Coils[idx]})
}

// ---------------------- TOGGLE: digital INPUT ----------------------

func toggleDigitalInputHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	_, dev, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.DigitalInputs) {
		fail(w, http.StatusBadRequest, "DigitalInputs index out of range")
		return
	}

	dev.DiscreteInputs[idx] ^= 1
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "digitalInput": dev.DiscreteInputs[idx]})
}

// ---------------------- PRESS SIMULATION: digital INPUT ----------------------

func pressDigitalInputHandler(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idxStr := r.PathValue("index")
	mode := r.PathValue("mode") // tap | hold1 | hold2

	idx, ok := parseIndex(w, idxStr)
	if !ok {
		return
	}

	sim, dev, cfg, ok := getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if idx >= int(cfg.DigitalInputs) {
		fail(w, http.StatusBadRequest, "DigitalInputs index out of range")
		return
	}

	// durations by mode
	var d time.Duration
	switch mode {
	case "tap":
		d = 500 * time.Millisecond
	case "hold1":
		d = 1 * time.Second
	case "hold2":
		d = 2 * time.Second
	default:
		fail(w, http.StatusBadRequest, "mode must be one of: tap, hold1, hold2")
		return
	}

	// Fire-and-forget simulation in a goroutine
	go simulatePress(sim, dev, byte(idx), d)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "scheduled",
		"mode":   mode,
		"index":  idx,
		"ms":     d.Milliseconds(),
	})
}

/* ------------------------------ utilities -------------------------------- */

// simulatePress sets the digital input to 1 and back to 0 after 'hold'.
func simulatePress(sim *mbserver.Server, dev *mbserver.Device, idx byte, hold time.Duration) {
	// Press (1)
	dev.DiscreteInputs[idx] = 1

	// Release after hold duration
	time.AfterFunc(hold, func() {
		dev.DiscreteInputs[idx] = 0
	})
}

func toJsonByteArray(data []byte) []uint {
	out := make([]uint, len(data))
	for i, v := range data {
		out[i] = uint(v)
	}
	return out
}

func uintsToBytes(ints []uint) []byte {
	out := make([]byte, len(ints))
	for i, v := range ints {
		out[i] = byte(v) // low 8 bits
	}
	return out
}
