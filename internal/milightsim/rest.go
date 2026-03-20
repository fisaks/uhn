package milightsim

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
)

type restHandler struct {
	store  *MilightSimStore
	server *UDPServer
}

// StartRestAPI starts the Mi-Light simulator REST control plane.
func StartRestAPI(store *MilightSimStore, addr string, server *UDPServer) error {
	mux := http.NewServeMux()
	h := &restHandler{store: store, server: server}

	// Zone endpoints
	mux.HandleFunc("GET /zones", h.getAllZones)
	mux.HandleFunc("GET /zone/{zone}", h.getZone)
	mux.HandleFunc("PUT /zone/{zone}", h.setZone)
	mux.HandleFunc("POST /zone/{zone}/toggle", h.toggleZone)

	// Admin endpoints
	mux.HandleFunc("GET /admin/status", h.adminStatus)
	mux.HandleFunc("POST /admin/stop", h.adminStop)
	mux.HandleFunc("POST /admin/start", h.adminStart)

	go func() {
		log.Printf("Mi-Light Sim REST API listening on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("Mi-Light REST API error: %v", err)
		}
	}()
	return nil
}

func parseZone(r *http.Request) (byte, error) {
	s := r.PathValue("zone")
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid zone: %s", s)
	}
	return byte(v), nil
}

func (h *restHandler) getAllZones(w http.ResponseWriter, r *http.Request) {
	zones := h.store.GetAllZones()
	writeJSON(w, http.StatusOK, zones)
}

func (h *restHandler) getZone(w http.ResponseWriter, r *http.Request) {
	zone, err := parseZone(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	z, ok := h.store.GetZone(zone)
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("zone %d not found", zone))
		return
	}
	writeJSON(w, http.StatusOK, z)
}

func (h *restHandler) setZone(w http.ResponseWriter, r *http.Request) {
	zone, err := parseZone(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	var body struct {
		Power      *bool `json:"power"`
		Brightness *int  `json:"brightness"`
		ColorTemp  *int  `json:"colorTemp"`
		Hue        *int  `json:"hue"`
		Saturation *int  `json:"saturation"`
		Mode       *int  `json:"mode"`
		ModeSpeedUp   *bool `json:"modeSpeedUp"`
		ModeSpeedDown *bool `json:"modeSpeedDown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.Power != nil {
		h.store.SetPower(zone, *body.Power)
	}
	if body.Brightness != nil {
		h.store.SetBrightness(zone, *body.Brightness)
	}
	if body.ColorTemp != nil {
		h.store.SetColorTemp(zone, *body.ColorTemp)
	}
	if body.Hue != nil {
		h.store.SetHue(zone, *body.Hue)
	}
	if body.Saturation != nil {
		h.store.SetSaturation(zone, *body.Saturation)
	}
	if body.Mode != nil {
		h.store.SetMode(zone, *body.Mode)
	}
	if body.ModeSpeedUp != nil && *body.ModeSpeedUp {
		h.store.IncModeSpeed(zone)
	}
	if body.ModeSpeedDown != nil && *body.ModeSpeedDown {
		h.store.DecModeSpeed(zone)
	}

	z, ok := h.store.GetZone(zone)
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("zone %d not found", zone))
		return
	}
	writeJSON(w, http.StatusOK, z)
}

func (h *restHandler) toggleZone(w http.ResponseWriter, r *http.Request) {
	zone, err := parseZone(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	newPower, ok := h.store.TogglePower(zone)
	if !ok {
		fail(w, http.StatusNotFound, fmt.Sprintf("zone %d not found", zone))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "power": newPower})
}

func (h *restHandler) adminStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"running": h.server.IsRunning(),
		"port":    h.store.Port(),
	})
}

func (h *restHandler) adminStop(w http.ResponseWriter, r *http.Request) {
	h.server.Stop()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (h *restHandler) adminStart(w http.ResponseWriter, r *http.Request) {
	if err := h.server.Restart(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
