package simulator

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/womat/mbserver"
)

// DeviceStateRequest is the JSON body for PATCH/GET device state.
type DeviceStateRequest struct {
	DigitalOutputs []uint   `json:"digitalOutputs,omitempty"`
	DigitalInputs  []uint   `json:"digitalInputs,omitempty"`
	AnalogOutputs  []uint16 `json:"analogOutputs,omitempty"`
	AnalogInputs   []uint16 `json:"analogInputs,omitempty"`
}

/* ---- internal handler struct ---- */

type restHandler struct {
	simStore *SimStore
}

// StartRestAPI starts the simulator REST API on the given address.
// If tcpCtrl is non-nil, admin stop/start endpoints are registered.
func StartRestAPI(simStore *SimStore, addr string, tcpCtrl *TCPListenerControl) error {
	mux := http.NewServeMux()
	h := &restHandler{simStore: simStore}

	// Admin endpoints for chaos testing
	if tcpCtrl != nil {
		ctrl := tcpCtrl
		mux.HandleFunc("POST /admin/stop", func(w http.ResponseWriter, r *http.Request) {
			ctrl.Stop()
			writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
		})
		mux.HandleFunc("POST /admin/start", func(w http.ResponseWriter, r *http.Request) {
			if err := ctrl.Start(); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
		})
		mux.HandleFunc("GET /admin/status", func(w http.ResponseWriter, r *http.Request) {
			running := ctrl.IsRunning()
			writeJSON(w, http.StatusOK, map[string]any{"running": running})
		})
	}

	mux.HandleFunc("GET /device/{busId}/{deviceName}", h.getDeviceState)
	mux.HandleFunc("PATCH /device/{busId}/{deviceName}", h.setDeviceState)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/digitalOutput/{index}", h.getDigitalOutput)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/digitalOutput/{index}", h.setDigitalOutput)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/digitalInput/{index}", h.getDigitalInput)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/digitalInput/{index}", h.setDigitalInput)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/analogOutput/{index}", h.getAnalogOutput)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/analogOutput/{index}", h.setAnalogOutput)

	mux.HandleFunc("GET /device/{busId}/{deviceName}/analogInput/{index}", h.getAnalogInput)
	mux.HandleFunc("PUT /device/{busId}/{deviceName}/analogInput/{index}", h.setAnalogInput)

	// toggles
	mux.HandleFunc("POST /device/{busId}/{deviceName}/digitalOutput/{index}/toggle", h.toggleDigitalOutput)
	mux.HandleFunc("POST /device/{busId}/{deviceName}/digitalInput/{index}/toggle", h.toggleDigitalInput)

	// press simulation (digital input)
	mux.HandleFunc("POST /device/{busId}/{deviceName}/digitalInput/{index}/press/{mode}", h.pressDigitalInput)

	log.Printf("Simulator REST API listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

/* ---- helpers: json & errors ---- */

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

/* ---- device lookup ---- */

func (h *restHandler) getSimAndDevice(w http.ResponseWriter, busId, deviceName string) (*mbserver.Server, *mbserver.Device, *SimDeviceConfig, bool) {
	srv, dev, cfg, ok := h.simStore.GetDevice(busId, deviceName)
	if !ok {
		if h.simStore.GetServer(busId) == nil {
			fail(w, http.StatusNotFound, "bus not found")
		} else if h.simStore.GetDeviceConfig(deviceName) == nil {
			fail(w, http.StatusNotFound, "bus device config not found")
		} else {
			fail(w, http.StatusNotFound, "device not found")
		}
		return nil, nil, nil, false
	}
	return srv, dev, cfg, true
}

/* ---- handlers ---- */

func (h *restHandler) setDeviceState(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")

	var req DeviceStateRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "bad json")
		return
	}

	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	if req.DigitalOutputs != nil {
		copy(device.Coils[cfg.DigitalOutputsStart:], uintsToBytes(req.DigitalOutputs))
	}
	if req.DigitalInputs != nil {
		copy(device.DiscreteInputs[cfg.DigitalInputsStart:], uintsToBytes(req.DigitalInputs))
	}
	if req.AnalogOutputs != nil {
		copy(device.HoldingRegisters[cfg.AnalogOutputsStart:], req.AnalogOutputs)
	}
	if req.AnalogInputs != nil {
		copy(device.InputRegisters[cfg.AnalogInputsStart:], req.AnalogInputs)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *restHandler) getDeviceState(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")

	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}

	out := DeviceStateRequest{}
	if cfg.DigitalOutputs > 0 {
		start := cfg.DigitalOutputsStart
		out.DigitalOutputs = toJsonByteArray(device.Coils[start : start+cfg.DigitalOutputs])
	}
	if cfg.DigitalInputs > 0 {
		start := cfg.DigitalInputsStart
		out.DigitalInputs = toJsonByteArray(device.DiscreteInputs[start : start+cfg.DigitalInputs])
	}
	if cfg.AnalogOutputs > 0 {
		start := cfg.AnalogOutputsStart
		out.AnalogOutputs = device.HoldingRegisters[start : start+cfg.AnalogOutputs]
	}
	if cfg.AnalogInputs > 0 {
		start := cfg.AnalogInputsStart
		out.AnalogInputs = device.InputRegisters[start : start+cfg.AnalogInputs]
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *restHandler) getDigitalOutput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.DigitalOutputs) {
		fail(w, http.StatusBadRequest, "DigitalOutputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint8{"value": device.Coils[int(cfg.DigitalOutputsStart)+idx]})
}

func (h *restHandler) setDigitalOutput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
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
	device.Coils[int(cfg.DigitalOutputsStart)+idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *restHandler) getDigitalInput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.DigitalInputs) {
		fail(w, http.StatusBadRequest, "DigitalInputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint8{"value": device.DiscreteInputs[int(cfg.DigitalInputsStart)+idx]})
}

func (h *restHandler) setDigitalInput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
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
	device.DiscreteInputs[int(cfg.DigitalInputsStart)+idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *restHandler) getAnalogOutput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.AnalogOutputs) {
		fail(w, http.StatusBadRequest, "AnalogOutputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint16{"value": device.HoldingRegisters[int(cfg.AnalogOutputsStart)+idx]})
}

func (h *restHandler) setAnalogOutput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
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
	device.HoldingRegisters[int(cfg.AnalogOutputsStart)+idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *restHandler) getAnalogInput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.AnalogInputs) {
		fail(w, http.StatusBadRequest, "AnalogInputs index out of range")
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint16{"value": device.InputRegisters[int(cfg.AnalogInputsStart)+idx]})
}

func (h *restHandler) setAnalogInput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, device, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
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
	device.InputRegisters[int(cfg.AnalogInputsStart)+idx] = payload.Value
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *restHandler) toggleDigitalOutput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, dev, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.DigitalOutputs) {
		fail(w, http.StatusBadRequest, "DigitalOutputs index out of range")
		return
	}
	absIdx := int(cfg.DigitalOutputsStart) + idx
	dev.Coils[absIdx] ^= 1
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "value": dev.Coils[absIdx]})
}

func (h *restHandler) toggleDigitalInput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, dev, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.DigitalInputs) {
		fail(w, http.StatusBadRequest, "DigitalInputs index out of range")
		return
	}
	absIdx := int(cfg.DigitalInputsStart) + idx
	dev.DiscreteInputs[absIdx] ^= 1
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "digitalInput": dev.DiscreteInputs[absIdx]})
}

func (h *restHandler) pressDigitalInput(w http.ResponseWriter, r *http.Request) {
	busId := r.PathValue("busId")
	deviceName := r.PathValue("deviceName")
	mode := r.PathValue("mode")
	idx, ok := parseIndex(w, r.PathValue("index"))
	if !ok {
		return
	}
	_, dev, cfg, ok := h.getSimAndDevice(w, busId, deviceName)
	if !ok {
		return
	}
	if idx >= int(cfg.DigitalInputs) {
		fail(w, http.StatusBadRequest, "DigitalInputs index out of range")
		return
	}

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

	go simulatePress(dev, byte(int(cfg.DigitalInputsStart)+idx), d)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "scheduled",
		"mode":   mode,
		"index":  idx,
		"ms":     d.Milliseconds(),
	})
}

/* ---- utilities ---- */

func simulatePress(dev *mbserver.Device, idx byte, hold time.Duration) {
	dev.DiscreteInputs[idx] = 1
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
		out[i] = byte(v)
	}
	return out
}
