package ihcsim

import (
	"fmt"
	"log"
	"sync"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/ihc"
)

// ResourceState holds the current value of one IHC resource in the simulator.
type ResourceState struct {
	ResourceID int
	Type       string // "digitalOutput"|"digitalInput"|"analogOutput"|"analogInput"
	Value      ihc.IHCValue
}

// Session represents an authenticated SOAP session.
type Session struct {
	ID         string
	Expired    bool
	Subscribed map[int]bool // resource IDs this session is subscribed to
}

// ControllerState holds all state for one simulated IHC controller.
type ControllerState struct {
	Name     string
	Host     string
	Port     int
	Username string
	Password string

	mu             sync.Mutex
	resources      map[int]*ResourceState // resourceID => state
	sessions       map[string]*Session    // sessionID => session
	notifier       *Notifier
	bindingManager *BindingManager // set after store init
}

// IHCSimStore is the central state for all simulated IHC controllers.
type IHCSimStore struct {
	controllers    map[string]*ControllerState // controller name => state
	bindingManager *BindingManager
}

// SetupFromConfig creates an IHCSimStore from the edge config.
func SetupFromConfig(cfg *config.EdgeConfig) (*IHCSimStore, error) {
	store := &IHCSimStore{
		controllers: make(map[string]*ControllerState),
	}
	store.bindingManager = NewBindingManager(store)

	for _, ctrl := range cfg.IHCControllers {

		cs := &ControllerState{
			Name:      ctrl.Name,
			Host:      ctrl.Host,
			Port:      ctrl.Port,
			Username:  ctrl.Username,
			Password:  ctrl.Password,
			resources: make(map[int]*ResourceState),
			sessions:  make(map[string]*Session),
			notifier:  NewNotifier(),
		}

		for _, res := range ctrl.Resources {
			zeroValue := zeroValueForType(res.Type)
			cs.resources[res.ResourceIntID] = &ResourceState{
				ResourceID: res.ResourceIntID,
				Type:       res.Type,
				Value:      zeroValue,
			}
		}

		store.controllers[ctrl.Name] = cs
		log.Printf("IHC Sim: controller %s configured with %d resources on port %d",
			ctrl.Name, len(ctrl.Resources), ctrl.Port)
		for _, res := range ctrl.Resources {
			log.Printf("  - 0x%X (%s)", res.ResourceIntID, res.Type)
		}
	}

	// Wire binding manager to each controller
	for _, cs := range store.controllers {
		cs.bindingManager = store.bindingManager
	}

	return store, nil
}

// GetController returns the controller state by name, or nil.
func (s *IHCSimStore) GetController(name string) *ControllerState {
	return s.controllers[name]
}

// BindingManager returns the store's binding manager.
func (s *IHCSimStore) BindingManager() *BindingManager {
	return s.bindingManager
}

// Controllers returns all controller names.
func (s *IHCSimStore) Controllers() []string {
	names := make([]string, 0, len(s.controllers))
	for name := range s.controllers {
		names = append(names, name)
	}
	return names
}

// --- ControllerState methods ---

// Authenticate validates credentials and creates a session.
func (cs *ControllerState) Authenticate(username, password string) (string, bool) {
	if username != cs.Username || password != cs.Password {
		return "", false
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()

	sessionID := fmt.Sprintf("sim-%s-%d", cs.Name, len(cs.sessions)+1)
	cs.sessions[sessionID] = &Session{
		ID:         sessionID,
		Subscribed: make(map[int]bool),
	}
	return sessionID, true
}

// ValidateSession checks if a session is valid (exists and not expired).
func (cs *ControllerState) ValidateSession(sessionID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	session, ok := cs.sessions[sessionID]
	return ok && !session.Expired
}

// RemoveSession removes a session.
func (cs *ControllerState) RemoveSession(sessionID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.sessions, sessionID)
}

// Subscribe registers resource IDs for notification on a session and queues
// current values so the next WaitForResourceValueChanges returns them.
func (cs *ControllerState) Subscribe(sessionID string, resourceIDs []int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	session, ok := cs.sessions[sessionID]
	if !ok {
		return
	}
	for _, id := range resourceIDs {
		session.Subscribed[id] = true
	}

	// Queue current values for immediate delivery
	var initial []ihc.ResourceValueEnvelope
	for _, id := range resourceIDs {
		if res, ok := cs.resources[id]; ok {
			initial = append(initial, ihc.ResourceValueEnvelope{
				ResourceID:     res.ResourceID,
				IsValueRuntime: true,
				Value:          res.Value,
			})
		}
	}
	if len(initial) > 0 {
		cs.notifier.QueueForSession(sessionID, initial)
	}
}

// SetResourceValue updates a resource value, notifies subscribers, and fires bindings.
func (cs *ControllerState) SetResourceValue(resourceID int, value ihc.IHCValue) bool {
	cs.mu.Lock()
	res, ok := cs.resources[resourceID]
	if !ok {
		cs.mu.Unlock()
		return false
	}
	res.Value = value

	// Collect sessions subscribed to this resource
	var subscribedSessions []string
	for _, session := range cs.sessions {
		if !session.Expired && session.Subscribed[resourceID] {
			subscribedSessions = append(subscribedSessions, session.ID)
		}
	}
	cs.mu.Unlock()

	// Notify outside the lock
	env := ihc.ResourceValueEnvelope{
		ResourceID:     resourceID,
		IsValueRuntime: true,
		Value:          value,
	}
	cs.notifier.Notify(subscribedSessions, env)

	// Fire bindings
	if cs.bindingManager != nil {
		cs.bindingManager.OnResourceChanged(cs.Name, resourceID, value)
	}

	return true
}

// GetResourceValue returns the current value of a resource.
func (cs *ControllerState) GetResourceValue(resourceID int) (*ResourceState, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	res, ok := cs.resources[resourceID]
	return res, ok
}

// GetAllResources returns all resource states.
func (cs *ControllerState) GetAllResources() []*ResourceState {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	result := make([]*ResourceState, 0, len(cs.resources))
	for _, res := range cs.resources {
		result = append(result, res)
	}
	return result
}

// GetSessions returns all sessions.
func (cs *ControllerState) GetSessions() []*Session {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	result := make([]*Session, 0, len(cs.sessions))
	for _, s := range cs.sessions {
		result = append(result, s)
	}
	return result
}

// ExpireAllSessions marks all sessions as expired.
func (cs *ControllerState) ExpireAllSessions() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, session := range cs.sessions {
		session.Expired = true
	}
	// Wake up any waiting long-polls so they get the expired error
	cs.notifier.WakeAll()
}

// zeroValueForType returns the zero value for a given resource type.
func zeroValueForType(resourceType string) ihc.IHCValue {
	switch resourceType {
	case "digitalOutput", "digitalInput":
		return ihc.BoolValue(false)
	case "analogOutput", "analogInput":
		return ihc.IntValue(0)
	default:
		return ihc.BoolValue(false)
	}
}
