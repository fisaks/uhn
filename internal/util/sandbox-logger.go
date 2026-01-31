package util

import (
	"encoding/json"
	"fmt"
	"os"
)

type RuleRuntimeLogMessage struct {
	Kind      string `json:"kind"`
	Cmd       string `json:"cmd"`
	Level     string `json:"level"`
	Component string `json:"component"`
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
}

func log(level, component, format string, args ...any) {
	msg := RuleRuntimeLogMessage{
		Kind:      "event",
		Cmd:       "log",
		Level:     level,
		Component: component,
		Message:   fmt.Sprintf(format, args...),
	}

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(msg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode log message: %v\n", err)
	}
}

func Info(component, format string, args ...any) {
	log("info", component, format, args...)
}

func Warn(component, format string, args ...any) {
	log("warn", component, format, args...)
}

func Error(component, format string, args ...any) {
	log("error", component, format, args...)
}

func Fatal(component, format string, args ...any) {
	log("error", component, format, args...)
	os.Exit(1)
}
