package logging

import (
	"log/slog"
	"os"
)

var Logger *slog.Logger

func init() {
	level := new(slog.LevelVar) // dynamic level if we ever want to adjust it

	var handler slog.Handler
	if os.Getenv("LOG_FORMAT") == "text" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}

	Logger = slog.New(handler)
}

// Shortcut helpers (optional)
var (
	Info  = Logger.Info
	Error = Logger.Error
	Warn  = Logger.Warn
	Debug = Logger.Debug
)
