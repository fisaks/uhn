package ihcsim

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/ihc"
)

type restHandler struct {
	store       *IHCSimStore
	soapServers map[string]*SOAPServer
}

// StartRestAPI starts the IHC simulator REST control plane.
func StartRestAPI(store *IHCSimStore, addr string, soapServers map[string]*SOAPServer) error {
	mux := http.NewServeMux()
	h := &restHandler{store: store, soapServers: soapServers}

	// Resource endpoints
	mux.HandleFunc("GET /controller/{name}/resources", h.getAllResources)
	mux.HandleFunc("GET /controller/{name}/resource/{id}", h.getResource)
	mux.HandleFunc("PUT /controller/{name}/resource/{id}", h.setResource)
	mux.HandleFunc("POST /controller/{name}/resource/{id}/toggle", h.toggleResource)
	mux.HandleFunc("POST /controller/{name}/resource/{id}/press/{mode}", h.pressResource)

	// Session endpoints
	mux.HandleFunc("GET /controller/{name}/sessions", h.getSessions)
	mux.HandleFunc("POST /controller/{name}/session/expire", h.expireSessions)

	// Binding endpoints
	mux.HandleFunc("GET /bindings", h.listBindings)
	mux.HandleFunc("POST /bindings", h.addBinding)
	mux.HandleFunc("DELETE /bindings/{id}", h.removeBinding)

	// Admin endpoints
	mux.HandleFunc("GET /admin/status", h.adminStatus)
	mux.HandleFunc("POST /admin/stop/{name}", h.adminStop)
	mux.HandleFunc("POST /admin/start/{name}", h.adminStart)

	go func() {
		log.Printf("IHC Sim REST API listening on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("IHC REST API error: %v", err)
		}
	}()
	return nil
}

// --- Controller + resource lookup ---

func (h *restHandler) getController(w http.ResponseWriter, r *http.Request) *ControllerState {
	name := r.PathValue("name")
	ctrl := h.store.GetController(name)
	if ctrl == nil {
		fail(w, http.StatusNotFound, fmt.Sprintf("controller %q not found", name))
		return nil
	}
	return ctrl
}

// parseResourceID parses a resource ID from the URL path (hex 0x... or decimal).
func parseResourceID(idStr string) (int, error) {
	idStr = strings.TrimSpace(idStr)
	if strings.HasPrefix(idStr, "0x") || strings.HasPrefix(idStr, "0X") {
		v, err := strconv.ParseInt(idStr[2:], 16, 64)
		return int(v), err
	}
	return strconv.Atoi(idStr)
}

// --- Resource handlers ---

func (h *restHandler) getAllResources(w http.ResponseWriter, r *http.Request) {
	ctrl := h.getController(w, r)
	if ctrl == nil {
		return
	}

	resources := ctrl.GetAllResources()
	type resJSON struct {
		ResourceID string `json:"resourceId"`
		Type       string `json:"type"`
		Value      any    `json:"value"`
	}
	result := make([]resJSON, 0, len(resources))
	for _, res := range resources {
		result = append(result, resJSON{
			ResourceID: fmt.Sprintf("0x%X", res.ResourceID),
			Type:       res.Type,
			Value:      res.Value.ToAny(),
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *restHandler) getResource(w http.ResponseWriter, r *http.Request) {
	ctrl := h.getController(w, r)
	if ctrl == nil {
		return
	}

	resourceID, err := parseResourceID(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid resource ID")
		return
	}

	res, ok := ctrl.GetResourceValue(resourceID)
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("resource 0x%X not found", resourceID))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"resourceId": fmt.Sprintf("0x%X", res.ResourceID),
		"type":       res.Type,
		"value":      res.Value.ToAny(),
	})
}

func (h *restHandler) setResource(w http.ResponseWriter, r *http.Request) {
	ctrl := h.getController(w, r)
	if ctrl == nil {
		return
	}

	resourceID, err := parseResourceID(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid resource ID")
		return
	}

	var body struct {
		Value any `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	value := anyToIHCValue(body.Value)
	if !ctrl.SetResourceValue(resourceID, value) {
		fail(w, http.StatusNotFound, fmt.Sprintf("resource 0x%X not found", resourceID))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *restHandler) toggleResource(w http.ResponseWriter, r *http.Request) {
	ctrl := h.getController(w, r)
	if ctrl == nil {
		return
	}

	resourceID, err := parseResourceID(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid resource ID")
		return
	}

	res, ok := ctrl.GetResourceValue(resourceID)
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("resource 0x%X not found", resourceID))
		return
	}

	if res.Value.Bool == nil {
		fail(w, http.StatusBadRequest, "toggle only works on boolean resources")
		return
	}

	newVal := !*res.Value.Bool
	ctrl.SetResourceValue(resourceID, ihc.BoolValue(newVal))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "value": newVal})
}

func (h *restHandler) pressResource(w http.ResponseWriter, r *http.Request) {
	ctrl := h.getController(w, r)
	if ctrl == nil {
		return
	}

	resourceID, err := parseResourceID(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid resource ID")
		return
	}

	res, ok := ctrl.GetResourceValue(resourceID)
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("resource 0x%X not found", resourceID))
		return
	}
	if res.Value.Bool == nil {
		fail(w, http.StatusBadRequest, "press only works on boolean resources")
		return
	}

	mode := r.PathValue("mode")
	var holdMs int
	switch mode {
	case "tap":
		holdMs = 50
	case "hold1":
		holdMs = 500
	case "hold2":
		holdMs = 1500
	default:
		fail(w, http.StatusBadRequest, "mode must be tap, hold1, or hold2")
		return
	}

	// Activate → hold → deactivate
	ctrl.SetResourceValue(resourceID, ihc.BoolValue(true))
	go func() {
		time.Sleep(time.Duration(holdMs) * time.Millisecond)
		ctrl.SetResourceValue(resourceID, ihc.BoolValue(false))
	}()

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "mode": mode})
}

// --- Session handlers ---

func (h *restHandler) getSessions(w http.ResponseWriter, r *http.Request) {
	ctrl := h.getController(w, r)
	if ctrl == nil {
		return
	}

	sessions := ctrl.GetSessions()
	type sessionJSON struct {
		ID      string `json:"id"`
		Expired bool   `json:"expired"`
	}
	result := make([]sessionJSON, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, sessionJSON{ID: s.ID, Expired: s.Expired})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *restHandler) expireSessions(w http.ResponseWriter, r *http.Request) {
	ctrl := h.getController(w, r)
	if ctrl == nil {
		return
	}

	ctrl.ExpireAllSessions()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Admin handlers ---

func (h *restHandler) adminStatus(w http.ResponseWriter, r *http.Request) {
	type controllerStatus struct {
		Name    string `json:"name"`
		Port    int    `json:"port"`
		Running bool   `json:"running"`
	}
	var result []controllerStatus
	for _, name := range h.store.Controllers() {
		ctrl := h.store.GetController(name)
		server := h.soapServers[name]
		running := false
		if server != nil {
			running = server.IsRunning()
		}
		result = append(result, controllerStatus{
			Name:    ctrl.Name,
			Port:    ctrl.Port,
			Running: running,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *restHandler) adminStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	server, ok := h.soapServers[name]
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("controller %q not found", name))
		return
	}
	if err := server.Stop(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (h *restHandler) adminStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	server, ok := h.soapServers[name]
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("controller %q not found", name))
		return
	}
	if err := server.Restart(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// --- Binding handlers ---

func (h *restHandler) listBindings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.store.BindingManager().List())
}

func (h *restHandler) addBinding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Controller string `json:"controller"`
		Trigger    string `json:"trigger"`
		Action     string `json:"action"`
		Target     string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	b, err := h.store.BindingManager().Add(body.Controller, body.Trigger, body.Action, body.Target)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (h *restHandler) removeBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.store.BindingManager().Remove(id) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	} else {
		fail(w, http.StatusNotFound, fmt.Sprintf("binding %q not found", id))
	}
}

// --- Helpers ---

func anyToIHCValue(v any) ihc.IHCValue {
	switch val := v.(type) {
	case bool:
		return ihc.BoolValue(val)
	case float64:
		// JSON numbers are float64 — use int if no fractional part
		if val == float64(int(val)) {
			return ihc.IntValue(int(val))
		}
		return ihc.FloatValue(val)
	default:
		return ihc.BoolValue(false)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
