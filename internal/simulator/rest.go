package simulator

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
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

/* ---- typed register helpers ---- */

func regsToUint32(regs []uint16) uint32 {
	var buf [4]byte
	binary.BigEndian.PutUint16(buf[0:2], regs[0])
	binary.BigEndian.PutUint16(buf[2:4], regs[1])
	return binary.BigEndian.Uint32(buf[:])
}

func uint32ToRegs(regs []uint16, val uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], val)
	regs[0] = binary.BigEndian.Uint16(buf[0:2])
	regs[1] = binary.BigEndian.Uint16(buf[2:4])
}

func writeFloat32ToRegs(regs []uint16, val float32) {
	uint32ToRegs(regs, math.Float32bits(val))
}

func readFloat32FromRegs(regs []uint16) float32 {
	return math.Float32frombits(regsToUint32(regs))
}

// registerWidth returns 2 for two-register types, 1 otherwise.
func registerWidth(typ string) int {
	switch typ {
	case "float32", "uint32", "int32":
		return 2
	default:
		return 1
	}
}

// readTypedRegs reads registers and returns a typed JSON value.
func readTypedRegs(regs []uint16, idx int, typ string) any {
	switch typ {
	case "int16":
		return int16(regs[idx])
	case "float32":
		return readFloat32FromRegs(regs[idx:])
	case "uint32":
		return regsToUint32(regs[idx:])
	case "int32":
		return int32(regsToUint32(regs[idx:]))
	default: // "", "uint16"
		return regs[idx]
	}
}

// analogValue holds a parsed analog value with an explicit type.
type analogValue struct {
	intVal int64
	fltVal float32
	typ    string // "", "uint16", "int16", "float32", "uint32", "int32"
}

// width returns the register width for this value's type.
func (v analogValue) width() int { return registerWidth(v.typ) }

// writeToRegs writes the value into the register slice at position 0.
func (v analogValue) writeToRegs(regs []uint16) {
	switch v.typ {
	case "float32":
		writeFloat32ToRegs(regs, v.fltVal)
	case "uint32":
		uint32ToRegs(regs, uint32(v.intVal))
	case "int32":
		uint32ToRegs(regs, uint32(int32(v.intVal)))
	case "int16":
		regs[0] = uint16(int16(v.intVal))
	default: // "", "uint16"
		regs[0] = uint16(v.intVal)
	}
}

// readAnalogPayload decodes {"value": N} or {"value": N, "type": "..."}.
// When type is omitted, a decimal value implies float32 and an integer implies uint16
// (backward compatible).
func readAnalogPayload(r *http.Request) (analogValue, error) {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()

	var payload struct {
		Value json.Number `json:"value"`
		Type  string      `json:"type"`
	}
	if err := dec.Decode(&payload); err != nil {
		return analogValue{}, err
	}

	typ := payload.Type
	s := payload.Value.String()

	// No explicit type — auto-detect like before
	if typ == "" {
		if strings.Contains(s, ".") {
			typ = "float32"
		} else {
			typ = "uint16"
		}
	}

	switch typ {
	case "float32":
		f, err := payload.Value.Float64()
		if err != nil {
			return analogValue{}, err
		}
		return analogValue{fltVal: float32(f), typ: "float32"}, nil

	case "int16":
		i, err := payload.Value.Int64()
		if err != nil {
			return analogValue{}, err
		}
		if i < -32768 || i > 32767 {
			return analogValue{}, fmt.Errorf("value %d out of int16 range", i)
		}
		return analogValue{intVal: i, typ: "int16"}, nil

	case "int32":
		i, err := payload.Value.Int64()
		if err != nil {
			return analogValue{}, err
		}
		if i < -2147483648 || i > 2147483647 {
			return analogValue{}, fmt.Errorf("value %d out of int32 range", i)
		}
		return analogValue{intVal: i, typ: "int32"}, nil

	case "uint32":
		i, err := payload.Value.Int64()
		if err != nil {
			return analogValue{}, err
		}
		if i < 0 || i > 4294967295 {
			return analogValue{}, fmt.Errorf("value %d out of uint32 range", i)
		}
		return analogValue{intVal: i, typ: "uint32"}, nil

	default: // "uint16" or ""
		i, err := payload.Value.Int64()
		if err != nil {
			return analogValue{}, err
		}
		if i < 0 || i > 65535 {
			return analogValue{}, fmt.Errorf("value %d out of uint16 range", i)
		}
		return analogValue{intVal: i, typ: "uint16"}, nil
	}
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
	absIdx := int(cfg.AnalogOutputsStart) + idx
	typ := r.URL.Query().Get("type")
	if rw := registerWidth(typ); rw > 1 && idx+rw-1 >= int(cfg.AnalogOutputs) {
		fail(w, http.StatusBadRequest, fmt.Sprintf("%s requires %d registers, index+%d out of range", typ, rw, rw-1))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": readTypedRegs(device.HoldingRegisters, absIdx, typ)})
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

	val, err := readAnalogPayload(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	absIdx := int(cfg.AnalogOutputsStart) + idx
	if rw := val.width(); rw > 1 && idx+rw-1 >= int(cfg.AnalogOutputs) {
		fail(w, http.StatusBadRequest, fmt.Sprintf("%s requires %d registers, index+%d out of range", val.typ, rw, rw-1))
		return
	}
	val.writeToRegs(device.HoldingRegisters[absIdx:])
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
	absIdx := int(cfg.AnalogInputsStart) + idx
	typ := r.URL.Query().Get("type")
	if rw := registerWidth(typ); rw > 1 && idx+rw-1 >= int(cfg.AnalogInputs) {
		fail(w, http.StatusBadRequest, fmt.Sprintf("%s requires %d registers, index+%d out of range", typ, rw, rw-1))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": readTypedRegs(device.InputRegisters, absIdx, typ)})
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

	val, err := readAnalogPayload(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	absIdx := int(cfg.AnalogInputsStart) + idx
	if rw := val.width(); rw > 1 && idx+rw-1 >= int(cfg.AnalogInputs) {
		fail(w, http.StatusBadRequest, fmt.Sprintf("%s requires %d registers, index+%d out of range", val.typ, rw, rw-1))
		return
	}
	val.writeToRegs(device.InputRegisters[absIdx:])
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
