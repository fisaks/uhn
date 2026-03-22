package runtime

import (
	"fmt"
)

// ResourceMap provides bidirectional lookup between address keys and resource IDs.
// Address keys use the format "{device}:{type}:{pin}" matching the master's makeAddressKey().
type ResourceMap struct {
	byAddressKey map[string]string           // "{device}:{type}:{pin}" -> resourceId
	byResourceID map[string]*RuntimeResource // resourceId -> resource
}

// NewResourceMap builds a ResourceMap filtered to resources belonging to the given edge.
func NewResourceMap(edgeName string, resources []RuntimeResource) *ResourceMap {
	rm := &ResourceMap{
		byAddressKey: make(map[string]string),
		byResourceID: make(map[string]*RuntimeResource),
	}

	for i := range resources {
		r := &resources[i]
		belongsToEdge := (r.Edge != "" && r.Edge == edgeName) ||
			(r.Host != "" && r.Host == edgeName)
		if !belongsToEdge {
			continue
		}
		rm.byResourceID[r.ID] = r

		key := makeAddressKey(r)
		if key != "" {
			rm.byAddressKey[key] = r.ID
		}
	}
	return rm
}

// LookupResourceID returns the resource ID for a device/type/pin address.
// Pin can be int/float64 (numeric) or string (Z2M property name).
func (rm *ResourceMap) LookupResourceID(device, resourceType string, pin any) (string, bool) {
	key := fmt.Sprintf("%s:%s:%s", device, resourceType, formatPinForKey(pin))
	id, ok := rm.byAddressKey[key]
	return id, ok
}

// LookupResource returns the resource for a given resource ID.
func (rm *ResourceMap) LookupResource(resourceID string) (*RuntimeResource, bool) {
	r, ok := rm.byResourceID[resourceID]
	return r, ok
}

// LookupByAddress returns the resource for a device/type/pin address.
func (rm *ResourceMap) LookupByAddress(device, resourceType string, pin any) (*RuntimeResource, bool) {
	key := fmt.Sprintf("%s:%s:%s", device, resourceType, formatPinForKey(pin))
	id, ok := rm.byAddressKey[key]
	if !ok {
		return nil, false
	}
	r, ok := rm.byResourceID[id]
	return r, ok
}

// AllResourceIDs returns all resource IDs in the map.
func (rm *ResourceMap) AllResourceIDs() []string {
	ids := make([]string, 0, len(rm.byResourceID))
	for id := range rm.byResourceID {
		ids = append(ids, id)
	}
	return ids
}

// makeAddressKey mirrors the TypeScript makeAddressKey from resource.util.ts.
// For pin-based resources: "{device}:{type}:{pin}"
func makeAddressKey(r *RuntimeResource) string {
	if r.Device != "" && r.Type != "" && r.Pin != nil {
		return fmt.Sprintf("%s:%s:%s", r.Device, r.Type, formatPinForKey(r.Pin))
	}
	return ""
}

// formatPinForKey formats a pin value for use in address keys.
// Numeric pins (float64 from JSON, int) are formatted as integers.
// String pins (Z2M property names) are used as-is.
func formatPinForKey(pin any) string {
	switch v := pin.(type) {
	case float64:
		return fmt.Sprintf("%d", int(v))
	case int:
		return fmt.Sprintf("%d", v)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}
