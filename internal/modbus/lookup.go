package modbus

import "github.com/fisaks/uhn/internal/config"

// findOwnerAndDevice returns (busPoller, pointer-to-device) or (nil,nil) if not found.
func findOwnerAndDevice(buses map[string]*BusPoller, deviceName string) (*BusPoller, *config.DeviceConfig) {
	for _, poller := range buses {
		for i := range poller.Devices {
			if poller.Devices[i].Name == deviceName {
				return poller, &poller.Devices[i]
			}
		}
	}
	return nil, nil
}
