package util

import (
	"strconv"
	"strings"
)

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
