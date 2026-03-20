package util

import (
	"net"
	"os"
	"strconv"
	"strings"
)

func GetEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func BytesToBinaryString(bs []byte, count int) string {
	var s strings.Builder
	bitsAdded := 0
	for _, b := range bs {
		for i := 0; i < 8 && bitsAdded < count; i++ {
			if b&(1<<i) != 0 {
				s.WriteString("1")
			} else {
				s.WriteString("0")
			}
			bitsAdded++
		}
	}
	return s.String()
}

func IntToBinaryString(words []uint16, count int) string {
	var s strings.Builder
	bitsAdded := 0
	for _, w := range words {
		for i := 0; i < 16 && bitsAdded < count; i++ {
			if w&(1<<i) != 0 {
				s.WriteString("1")
			} else {
				s.WriteString("0")
			}
			bitsAdded++
		}
	}
	return s.String()
}

func ToUint16(v any) uint16 {
	switch x := v.(type) {
	case float64:
		return uint16(x)
	case int:
		return uint16(x)
	case uint16:
		return x
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			if u, err := strconv.ParseUint(s[2:], 16, 16); err == nil {
				return uint16(u)
			}
		}
		if u, err := strconv.ParseUint(s, 10, 16); err == nil {
			return uint16(u)
		}
	}
	return 0
}

func ToInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
	}
	return 0
}

// Clamp restricts v to [lo, hi].
func Clamp(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

func SplitHostAndPort(hostPort string, defaultAddr string) (address, host string, port int, err error) {
	addr := hostPort
	if addr == "" {
		addr = defaultAddr
	}
	addressStr, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", 0, err
	}
	portInt, err := strconv.Atoi(portStr)
	if err != nil {
		return "", "", 0, err
	}
	return addr, addressStr, portInt, nil
}
