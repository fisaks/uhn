package zigbeesim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/fisaks/uhn/internal/logging"
)

// Z2MSimREST provides a REST control plane for the Z2M simulator.
type Z2MSimREST struct {
	store  *Z2MSimStore
	server *Z2MSimServer
}

// NewZ2MSimREST creates a REST handler.
func NewZ2MSimREST(store *Z2MSimStore, server *Z2MSimServer) *Z2MSimREST {
	return &Z2MSimREST{store: store, server: server}
}

// Start starts the REST server.
func (r *Z2MSimREST) Start(port int) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/devices", r.handleDevices)
	mux.HandleFunc("/device/", r.handleDevice)
	mux.HandleFunc("/admin/publish-devices", r.handlePublishDevices)

	addr := fmt.Sprintf(":%d", port)
	logging.Info("Z2M sim REST: listening", "port", port)
	return http.ListenAndServe(addr, mux)
}

// GET /devices — list all devices with current state
func (r *Z2MSimREST) handleDevices(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result := make(map[string]any)
	for _, name := range r.store.DeviceNames() {
		result[name] = r.store.GetStateMap(name)
	}
	writeJSON(w, result)
}

// GET /device/{name} — get device state
// PUT /device/{name} — set device properties (partial update)
// POST /device/{name}/toggle — toggle state
func (r *Z2MSimREST) handleDevice(w http.ResponseWriter, req *http.Request) {
	// Parse path: /device/{name} or /device/{name}/toggle
	path := strings.TrimPrefix(req.URL.Path, "/device/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case req.Method == http.MethodGet && action == "":
		state := r.store.GetStateMap(name)
		if state == nil {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		writeJSON(w, state)

	case req.Method == http.MethodPut && action == "":
		var props map[string]any
		if err := json.NewDecoder(req.Body).Decode(&props); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		var lastData []byte
		for prop, value := range props {
			data, err := r.store.SetProperty(name, prop, value)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			lastData = data
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(lastData)

	case req.Method == http.MethodPost && action == "toggle":
		data, err := r.store.ToggleState(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)

	case req.Method == http.MethodPost && action == "action":
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Action == "" {
			http.Error(w, `{"error":"missing action field"}`, http.StatusBadRequest)
			return
		}
		data, err := r.store.EmitAction(name, body.Action)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		// Publish the transient state (with action) to MQTT
		r.server.PublishDeviceState(name, data)
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)

	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// POST /admin/publish-devices — re-publish bridge/devices to MQTT
func (r *Z2MSimREST) handlePublishDevices(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.server != nil {
		r.server.PublishBridgeDevices(req.Context())
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
