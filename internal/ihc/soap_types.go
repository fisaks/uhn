package ihc

import "errors"

// ErrSessionExpired is returned when the IHC controller reports a SOAP fault
// indicating the session has expired ("Logon Failed"). Callers should
// re-authenticate and retry.
var ErrSessionExpired = errors.New("IHC session expired")

// IHCValue represents a typed value from the IHC controller.
// Exactly one field is non-nil.
type IHCValue struct {
	Bool  *bool
	Int   *int
	Float *float64
}

// BoolValue creates an IHCValue holding a boolean.
func BoolValue(v bool) IHCValue { return IHCValue{Bool: &v} }

// IntValue creates an IHCValue holding an integer.
func IntValue(v int) IHCValue { return IHCValue{Int: &v} }

// FloatValue creates an IHCValue holding a float.
func FloatValue(v float64) IHCValue { return IHCValue{Float: &v} }

// ResourceValueEnvelope is a single resource value from a notification or getRuntimeValue response.
type ResourceValueEnvelope struct {
	ResourceID     int
	TypeString     string // e.g. "airlink_dimming", "dataline_output", "" (empty on waitForResourceValueChanges)
	IsValueRuntime bool
	Value          IHCValue
}

// ToAny returns the Go value (bool, int, or float64) for use in generic contexts.
func (v IHCValue) ToAny() any {
	if v.Bool != nil {
		return *v.Bool
	}
	if v.Int != nil {
		return *v.Int
	}
	if v.Float != nil {
		return *v.Float
	}
	return nil
}
