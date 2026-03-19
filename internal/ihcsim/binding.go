package ihcsim

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/fisaks/uhn/internal/ihc"
)

// Binding defines a reactive link between two resources on the same controller.
// When the trigger resource transitions to true (rising edge), the action is
// executed on the target resource.
type Binding struct {
	ID         string `json:"id"`
	Controller string `json:"controller"`
	Trigger    int    `json:"trigger"`           // resource ID (decimal)
	TriggerHex string `json:"triggerHex"`         // display only
	Action     string `json:"action"`             // "toggle"
	Target     int    `json:"target"`             // resource ID (decimal)
	TargetHex  string `json:"targetHex"`           // display only
}

// BindingsConfig is the JSON structure for the bindings config file.
type BindingsConfig struct {
	Bindings []BindingEntry `json:"bindings"`
}

// BindingEntry is the JSON representation of a binding in the config file.
// Resource IDs can be hex ("0x9F045C") or decimal.
type BindingEntry struct {
	Controller string `json:"controller"`
	Trigger    string `json:"trigger"` // hex or decimal
	Action     string `json:"action"`  // "toggle"
	Target     string `json:"target"`  // hex or decimal
}

// BindingManager manages reactive bindings for all controllers.
type BindingManager struct {
	mu       sync.RWMutex
	bindings []*Binding
	nextID   int
	store    *IHCSimStore
}

// NewBindingManager creates a new binding manager.
func NewBindingManager(store *IHCSimStore) *BindingManager {
	return &BindingManager{
		store:  store,
		nextID: 1,
	}
}

// LoadFromFile loads bindings from a JSON config file.
func (bm *BindingManager) LoadFromFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open bindings file: %w", err)
	}
	defer f.Close()

	var cfg BindingsConfig
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return fmt.Errorf("parse bindings file: %w", err)
	}

	for _, entry := range cfg.Bindings {
		if _, err := bm.Add(entry.Controller, entry.Trigger, entry.Action, entry.Target); err != nil {
			log.Printf("IHC Sim: skipping invalid binding: %v", err)
		}
	}
	return nil
}

// Add creates a new binding. triggerStr and targetStr accept hex ("0x9F045C") or decimal.
func (bm *BindingManager) Add(controller, triggerStr, action, targetStr string) (*Binding, error) {
	if !validBindingAction(action) {
		return nil, fmt.Errorf("unsupported action %q", action)
	}

	ctrl := bm.store.GetController(controller)
	if ctrl == nil {
		return nil, fmt.Errorf("controller %q not found", controller)
	}

	trigger, err := parseResourceID(triggerStr)
	if err != nil {
		return nil, fmt.Errorf("invalid trigger ID %q: %w", triggerStr, err)
	}
	target, err := parseResourceID(targetStr)
	if err != nil {
		return nil, fmt.Errorf("invalid target ID %q: %w", targetStr, err)
	}

	if _, ok := ctrl.GetResourceValue(trigger); !ok {
		return nil, fmt.Errorf("trigger resource 0x%X not found on controller %s", trigger, controller)
	}
	if _, ok := ctrl.GetResourceValue(target); !ok {
		return nil, fmt.Errorf("target resource 0x%X not found on controller %s", target, controller)
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	b := &Binding{
		ID:         fmt.Sprintf("b%d", bm.nextID),
		Controller: controller,
		Trigger:    trigger,
		TriggerHex: fmt.Sprintf("0x%X", trigger),
		Action:     action,
		Target:     target,
		TargetHex:  fmt.Sprintf("0x%X", target),
	}
	bm.nextID++
	bm.bindings = append(bm.bindings, b)

	log.Printf("IHC Sim: binding %s added: %s on %s trigger 0x%X → %s 0x%X",
		b.ID, controller, b.TriggerHex, trigger, action, target)
	return b, nil
}

// Remove deletes a binding by ID.
func (bm *BindingManager) Remove(id string) bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	for i, b := range bm.bindings {
		if b.ID == id {
			bm.bindings = append(bm.bindings[:i], bm.bindings[i+1:]...)
			log.Printf("IHC Sim: binding %s removed", id)
			return true
		}
	}
	return false
}

// List returns all bindings.
func (bm *BindingManager) List() []*Binding {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	result := make([]*Binding, len(bm.bindings))
	copy(result, bm.bindings)
	return result
}

// OnResourceChanged is called when a resource value changes.
// It checks for matching bindings and executes actions.
func (bm *BindingManager) OnResourceChanged(controller string, resourceID int, value ihc.IHCValue) {
	// Only trigger on rising edge (false → true)
	if value.Bool == nil || !*value.Bool {
		return
	}

	bm.mu.RLock()
	var matches []*Binding
	for _, b := range bm.bindings {
		if b.Controller == controller && b.Trigger == resourceID {
			matches = append(matches, b)
		}
	}
	bm.mu.RUnlock()

	for _, b := range matches {
		bm.executeAction(b)
	}
}

func (bm *BindingManager) executeAction(b *Binding) {
	ctrl := bm.store.GetController(b.Controller)
	if ctrl == nil {
		return
	}

	switch b.Action {
	case "toggle":
		res, ok := ctrl.GetResourceValue(b.Target)
		if !ok {
			return
		}
		if res.Value.Bool != nil {
			newVal := !*res.Value.Bool
			ctrl.SetResourceValue(b.Target, ihc.BoolValue(newVal))
			log.Printf("IHC Sim: binding %s fired: toggle 0x%X → %v", b.ID, b.Target, newVal)
		}
	}
}

func validBindingAction(action string) bool {
	switch action {
	case "toggle":
		return true
	default:
		return false
	}
}
