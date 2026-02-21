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
		if r.Edge != edgeName {
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
func (rm *ResourceMap) LookupResourceID(device, resourceType string, pin int) (string, bool) {
	key := fmt.Sprintf("%s:%s:%d", device, resourceType, pin)
	id, ok := rm.byAddressKey[key]
	return id, ok
}

// LookupResource returns the resource for a given resource ID.
func (rm *ResourceMap) LookupResource(resourceID string) (*RuntimeResource, bool) {
	r, ok := rm.byResourceID[resourceID]
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
		return fmt.Sprintf("%s:%s:%d", r.Device, r.Type, *r.Pin)
	}
	return ""
}
