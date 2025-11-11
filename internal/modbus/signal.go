package modbus

// ZeroSignal is a zero-size “just-a-signal” type.
type ZeroSignal struct{}

// Zero is the canonical value to send on signal channels.
var Zero ZeroSignal


// CmdCh returns a send-only channel for enqueueing DeviceCommand values.
func (p *BusPoller) CmdCh() chan<- DeviceCommand {
    return p.cmdCh
}
