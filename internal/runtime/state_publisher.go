package runtime

import (
	"context"

	"github.com/fisaks/uhn/internal/uhn"
)

// BridgedPublisher wraps an EdgePublisher to intercept polled device states
// and feed them to the IPC bridge before delegating to the inner publisher.
// This decorator pattern avoids modifying poller internals.
type BridgedPublisher struct {
	inner     uhn.EdgePublisher
	ipcBridge *IPCBridge
}

// NewBridgedPublisher creates a new BridgedPublisher.
func NewBridgedPublisher(inner uhn.EdgePublisher, ipcBridge *IPCBridge) *BridgedPublisher {
	return &BridgedPublisher{
		inner:     inner,
		ipcBridge: ipcBridge,
	}
}

// PublishDeviceState feeds state to the IPC bridge then delegates to the inner publisher.
func (p *BridgedPublisher) PublishDeviceState(ctx context.Context, state uhn.DeviceState) error {
	p.ipcBridge.HandleDeviceState(ctx, state)
	return p.inner.PublishDeviceState(ctx, state)
}

// ClearPublishedState delegates to the inner publisher.
func (p *BridgedPublisher) ClearPublishedState() {
	p.inner.ClearPublishedState()
}

// Ensure BridgedPublisher satisfies EdgePublisher.
var _ uhn.EdgePublisher = (*BridgedPublisher)(nil)
