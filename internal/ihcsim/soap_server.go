package ihcsim

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/ihc"
)

// SOAPServer serves SOAP endpoints for one IHC controller.
type SOAPServer struct {
	controller *ControllerState
	server     *http.Server
	listener   net.Listener
	mu         sync.Mutex
	running    bool
}

// NewSOAPServer creates a SOAP server for the given controller.
func NewSOAPServer(controller *ControllerState) *SOAPServer {
	s := &SOAPServer{controller: controller}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/AuthenticationService", s.handleAuth)
	mux.HandleFunc("/ws/ResourceInteractionService", s.handleResource)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", controller.Port),
		Handler: mux,
	}
	return s
}

// Start begins listening. Returns immediately; serves in background.
func (s *SOAPServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("already running")
	}

	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.running = true

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("IHC SOAP server %s error: %v", s.controller.Name, err)
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	log.Printf("IHC SOAP server %s listening on %s", s.controller.Name, s.server.Addr)
	return nil
}

// Stop shuts down the SOAP server.
func (s *SOAPServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("not running")
	}
	return s.server.Close()
}

// IsRunning returns whether the server is currently running.
func (s *SOAPServer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Restart stops and starts the SOAP server.
func (s *SOAPServer) Restart() error {
	// Stop if running
	s.mu.Lock()
	wasRunning := s.running
	s.mu.Unlock()

	if wasRunning {
		if err := s.Stop(); err != nil {
			return err
		}
		// Give the port a moment to free
		time.Sleep(100 * time.Millisecond)
	}

	// Create a new http.Server (can't reuse after Close)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/AuthenticationService", s.handleAuth)
	mux.HandleFunc("/ws/ResourceInteractionService", s.handleResource)
	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.controller.Port),
		Handler: mux,
	}

	return s.Start()
}

// --- SOAP request routing ---

func (s *SOAPServer) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	action := extractSOAPAction(r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	switch action {
	case "authenticate":
		s.handleAuthenticate(w, body)
	case "disconnect":
		s.handleDisconnect(w, r)
	default:
		writeSOAP(w, SOAPFaultXML("SOAP-ENV:Client", "Unknown action: "+action))
	}
}

func (s *SOAPServer) handleResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := extractSessionID(r)
	if !s.controller.ValidateSession(sessionID) {
		writeSOAP(w, SessionExpiredFaultXML())
		return
	}

	action := extractSOAPAction(r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	switch action {
	case "enableRuntimeValueNotifications":
		s.handleEnableNotifications(w, sessionID, body)
	case "waitForResourceValueChanges":
		s.handleWaitForChanges(w, sessionID, body)
	case "setResourceValue":
		s.handleSetResourceValue(w, body)
	default:
		writeSOAP(w, SOAPFaultXML("SOAP-ENV:Client", "Unknown action: "+action))
	}
}

// --- Individual SOAP handlers ---

func (s *SOAPServer) handleAuthenticate(w http.ResponseWriter, body []byte) {
	username := extractXMLElement(string(body), "username")
	password := extractXMLElement(string(body), "password")

	sessionID, ok := s.controller.Authenticate(username, password)
	if !ok {
		writeSOAP(w, AuthenticateFailureXML())
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:  "JSESSIONID",
		Value: sessionID,
		Path:  "/",
	})
	writeSOAP(w, AuthenticateSuccessXML())
	log.Printf("IHC Sim %s: authenticated session %s", s.controller.Name, sessionID)
}

func (s *SOAPServer) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	sessionID := extractSessionID(r)
	if sessionID != "" {
		s.controller.notifier.CleanupSession(sessionID)
		s.controller.RemoveSession(sessionID)
		log.Printf("IHC Sim %s: disconnected session %s", s.controller.Name, sessionID)
	}
	writeSOAP(w, DisconnectSuccessXML())
}

func (s *SOAPServer) handleEnableNotifications(w http.ResponseWriter, sessionID string, body []byte) {
	resourceIDs := extractArrayItems(string(body))
	s.controller.Subscribe(sessionID, resourceIDs)
	log.Printf("IHC Sim %s: session %s subscribed to %d resources", s.controller.Name, sessionID, len(resourceIDs))
	writeSOAP(w, EnableNotificationsXML())
}

func (s *SOAPServer) handleWaitForChanges(w http.ResponseWriter, sessionID string, body []byte) {
	timeoutStr := extractXMLElement(string(body), "waitForResourceValueChanges1")
	timeoutSec, _ := strconv.Atoi(timeoutStr)
	if timeoutSec <= 0 {
		timeoutSec = 10
	}

	changes := s.controller.notifier.Wait(sessionID, time.Duration(timeoutSec)*time.Second)

	// Check if session expired while waiting
	if !s.controller.ValidateSession(sessionID) {
		writeSOAP(w, SessionExpiredFaultXML())
		return
	}

	writeSOAP(w, WaitForChangesXML(changes))
}

func (s *SOAPServer) handleSetResourceValue(w http.ResponseWriter, body []byte) {
	bodyStr := string(body)
	resourceIDStr := extractXMLElement(bodyStr, "resourceID")
	resourceID, err := strconv.Atoi(resourceIDStr)
	if err != nil {
		writeSOAP(w, SOAPFaultXML("SOAP-ENV:Client", "Invalid resourceID"))
		return
	}

	value := parseSetValueXML(bodyStr)

	if s.controller.SetResourceValue(resourceID, value) {
		log.Printf("IHC Sim %s: set resource 0x%X = %v", s.controller.Name, resourceID, value.ToAny())
		writeSOAP(w, SetResourceValueSuccessXML())
	} else {
		writeSOAP(w, SOAPFaultXML("SOAP-ENV:Server", fmt.Sprintf("Resource 0x%X not found", resourceID)))
	}
}

// --- XML parsing helpers ---

// extractSOAPAction extracts the SOAP action from the request headers.
// The SOAPAction header is quoted: "authenticate" → authenticate
func extractSOAPAction(r *http.Request) string {
	action := r.Header.Get("SOAPAction")
	return strings.Trim(action, `"`)
}

// extractSessionID extracts the JSESSIONID from cookies.
func extractSessionID(r *http.Request) string {
	cookie, err := r.Cookie("JSESSIONID")
	if err != nil {
		return ""
	}
	return cookie.Value
}

// extractXMLElement extracts the text content of a simple XML element.
// Works for elements like <username>value</username>.
func extractXMLElement(body, elementName string) string {
	re := regexp.MustCompile(`<` + elementName + `[^>]*>([^<]*)<`)
	match := re.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

// extractArrayItems extracts integer values from <arrayItem>123</arrayItem> elements.
func extractArrayItems(body string) []int {
	re := regexp.MustCompile(`<arrayItem>(\d+)</arrayItem>`)
	matches := re.FindAllStringSubmatch(body, -1)
	var ids []int
	for _, m := range matches {
		if id, err := strconv.Atoi(m[1]); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// parseSetValueXML extracts the typed value from a setResourceValue SOAP body.
func parseSetValueXML(body string) ihc.IHCValue {
	switch {
	case strings.Contains(body, "WSBooleanValue"):
		valStr := extractXMLElement(body, "value")
		// The body has multiple <value> elements — find the one with boolean content
		re := regexp.MustCompile(`WSBooleanValue[^<]*<[^>]*value[^>]*>(\w+)<`)
		if m := re.FindStringSubmatch(body); len(m) >= 2 {
			valStr = m[1]
		}
		return ihc.BoolValue(valStr == "true")
	case strings.Contains(body, "WSIntegerValue"):
		intStr := extractXMLElement(body, "integer")
		v, _ := strconv.Atoi(intStr)
		return ihc.IntValue(v)
	case strings.Contains(body, "WSFloatingPointValue"):
		floatStr := extractXMLElement(body, "floatingPointValue")
		v, _ := strconv.ParseFloat(floatStr, 64)
		return ihc.FloatValue(v)
	default:
		return ihc.BoolValue(false)
	}
}

// writeSOAP writes a SOAP XML response.
func writeSOAP(w http.ResponseWriter, xml string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Write([]byte(xml))
}
