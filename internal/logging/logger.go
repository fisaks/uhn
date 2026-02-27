package logging

import (
	"log"
	"log/slog"
	"os"
	"strings"
)

const LevelTrace = slog.Level(-8) // below slog.LevelDebug (-4)

var Logger *slog.Logger
var logLevel *slog.LevelVar

func Init() {
	logLevel = new(slog.LevelVar)

	switch strings.ToLower(os.Getenv("UHN_LOG_LEVEL")) {
	case "trace":
		logLevel.Set(LevelTrace)
	case "debug":
		logLevel.Set(slog.LevelDebug)
	case "warn":
		logLevel.Set(slog.LevelWarn)
	case "error":
		logLevel.Set(slog.LevelError)
	default:
		logLevel.Set(slog.LevelInfo)
	}
	opts := &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				if level, ok := a.Value.Any().(slog.Level); ok && level == LevelTrace {
					a.Value = slog.StringValue("TRACE")
				}
			}
			return a
		},
	}
	var handler slog.Handler
	if os.Getenv("LOG_FORMAT") == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	Logger = slog.New(handler)
}

func SetLevel(l slog.Level) { logLevel.Set(l) }
func GetLevel() slog.Level   { return logLevel.Level() }

func GetLevelName() string {
	switch logLevel.Level() {
	case LevelTrace:
		return "trace"
	case slog.LevelDebug:
		return "debug"
	case slog.LevelWarn:
		return "warn"
	case slog.LevelError:
		return "error"
	default:
		return "info"
	}
}

func ParseLevel(name string) (slog.Level, bool) {
	switch strings.ToLower(name) {
	case "trace":
		return LevelTrace, true
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
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
func Trace(msg string, args ...any) { Logger.Log(nil, LevelTrace, msg, args...) }
