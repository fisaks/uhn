package logging

import (
	"log"
	"log/slog"
	"os"
	"strings"
)

var Logger *slog.Logger

func Init() {
	level := new(slog.LevelVar) // dynamic level if we ever want to adjust it

	switch strings.ToLower(os.Getenv("UHN_LOG_LEVEL")) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	var handler slog.Handler
	if os.Getenv("LOG_FORMAT") == "text" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}

	Logger = slog.New(handler)
}

// Fatal logs an error message and exits the program.
func Fatal(msg string, args ...any) {
	Logger.Error(msg, args...)
	os.Exit(1)
}

type slogWriter struct {
	sl *slog.Logger
}

func (w slogWriter) Write(p []byte) (int, error) {
	msg := string(p)
	// Trim trailing newline because log.Logger always appends one
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	w.sl.Info(msg)
	return len(p), nil
}

func WrapSlog(args ...any) *log.Logger {
	return log.New(slogWriter{Logger.With(args...)}, "", 0)
}

func Info(msg string, args ...any)  { Logger.Info(msg, args...) }
func Error(msg string, args ...any) { Logger.Error(msg, args...) }
func Warn(msg string, args ...any)  { Logger.Warn(msg, args...) }
func Debug(msg string, args ...any) { Logger.Debug(msg, args...) }
