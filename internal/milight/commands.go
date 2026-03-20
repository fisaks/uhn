package milight

// FUT069 RGB+CCT command builders.
// All functions return a 9-byte command payload for use with UDPClient.SendCommand.
//
// Command byte reference (byte 4 of the 9-byte payload):
//   0x01 = hue/color (0x00-0xFF, repeated 4x)
//   0x02 = saturation (0x00-0x64)
//   0x03 = brightness (0x00-0x64)
//   0x04 = power/control (0x01=on, 0x02=off, 0x03=speed up, 0x04=speed down, 0x05=night mode)
//   0x05 = color temperature (0x00-0x64, 0=warm 100=cool; 0x64=white mode/RGB off)
//   0x06 = mode/effect (0x01-0x09)

// CmdPowerOn returns the power-on command.
func CmdPowerOn() [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x04, 0x01, 0x00, 0x00, 0x00}
}

// CmdPowerOff returns the power-off command.
func CmdPowerOff() [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x04, 0x02, 0x00, 0x00, 0x00}
}

// CmdNightMode enters night mode (very dim, below min brightness).
func CmdNightMode() [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x04, 0x05, 0x00, 0x00, 0x00}
}

// CmdBrightness sets brightness. val: 1–100.
func CmdBrightness(val byte) [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x03, val, 0x00, 0x00, 0x00}
}

// CmdHue sets hue/color (enters color mode). h: 0–255.
func CmdHue(h byte) [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x01, h, h, h, h}
}

// CmdSaturation sets saturation (RGB+CCT only). val: 0–100.
func CmdSaturation(val byte) [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x02, val, 0x00, 0x00, 0x00}
}

// CmdColorTemp sets color temperature (switches to CCT chip). val: 0–100 (0=warm, 100=cool).
func CmdColorTemp(val byte) [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x05, val, 0x00, 0x00, 0x00}
}

// CmdMode sets a built-in color effect. mode: 1–9.
func CmdMode(mode byte) [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x06, mode, 0x00, 0x00, 0x00}
}

// CmdModeSpeedUp increases the speed of the current effect (one step).
func CmdModeSpeedUp() [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x04, 0x03, 0x00, 0x00, 0x00}
}

// CmdModeSpeedDown decreases the speed of the current effect (one step).
func CmdModeSpeedDown() [9]byte {
	return [9]byte{0x31, 0x00, 0x00, 0x08, 0x04, 0x04, 0x00, 0x00, 0x00}
}
