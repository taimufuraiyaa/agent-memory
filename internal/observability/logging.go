package observability

import (
	"context"
	"log/slog"
	"os"
	"sync"
)

// LogLevel represents the logging level.
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// LogFormat represents the log output format.
type LogFormat string

const (
	LogFormatJSON LogFormat = "json"
	LogFormatText LogFormat = "text"
)

// Logger wraps slog.Logger with convenience methods.
type Logger struct {
	*slog.Logger
}

var (
	defaultLogger *Logger
	loggerOnce    sync.Once
)

// GetLogger returns the global logger, initializing it if needed.
func GetLogger() *Logger {
	loggerOnce.Do(func() {
		defaultLogger = NewLogger(LogLevelInfo, LogFormatText)
	})
	return defaultLogger
}

// SetLogger sets the global logger.
func SetLogger(logger *Logger) {
	defaultLogger = logger
}

// NewLogger creates a new structured logger.
func NewLogger(level LogLevel, format LogFormat) *Logger {
	var handler slog.Handler
	
	// Determine log level
	var slogLevel slog.Level
	switch level {
	case LogLevelDebug:
		slogLevel = slog.LevelDebug
	case LogLevelInfo:
		slogLevel = slog.LevelInfo
	case LogLevelWarn:
		slogLevel = slog.LevelWarn
	case LogLevelError:
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	
	opts := &slog.HandlerOptions{
		Level: slogLevel,
		AddSource: false,
	}
	
	// Create handler based on format
	switch format {
	case LogFormatJSON:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case LogFormatText:
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	
	return &Logger{
		Logger: slog.New(handler),
	}
}

// WithWorkspace returns a logger with workspace context.
func (l *Logger) WithWorkspace(workspace string) *Logger {
	return &Logger{
		Logger: l.With("workspace", workspace),
	}
}

// WithComponent returns a logger with component context.
func (l *Logger) WithComponent(component string) *Logger {
	return &Logger{
		Logger: l.With("component", component),
	}
}

// WithFields returns a logger with additional fields.
func (l *Logger) WithFields(fields map[string]any) *Logger {
	args := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return &Logger{
		Logger: l.With(args...),
	}
}

// WithError returns a logger with error context.
func (l *Logger) WithError(err error) *Logger {
	if err == nil {
		return l
	}
	return &Logger{
		Logger: l.With("error", err.Error()),
	}
}

// LogOperation logs the start and end of an operation with duration.
func (l *Logger) LogOperation(ctx context.Context, operation string, fn func() error) error {
	timer := NewTimer()
	l.InfoContext(ctx, "operation started", "operation", operation)
	
	err := fn()
	duration := timer.Duration()
	
	if err != nil {
		l.ErrorContext(ctx, "operation failed",
			"operation", operation,
			"duration", duration,
			"error", err.Error(),
		)
		return err
	}
	
	l.InfoContext(ctx, "operation completed",
		"operation", operation,
		"duration", duration,
	)
	return nil
}

// DebugWithFields logs a debug message with structured fields.
func (l *Logger) DebugWithFields(msg string, fields map[string]any) {
	args := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	l.Debug(msg, args...)
}

// InfoWithFields logs an info message with structured fields.
func (l *Logger) InfoWithFields(msg string, fields map[string]any) {
	args := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	l.Info(msg, args...)
}

// WarnWithFields logs a warn message with structured fields.
func (l *Logger) WarnWithFields(msg string, fields map[string]any) {
	args := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	l.Warn(msg, args...)
}

// ErrorWithFields logs an error message with structured fields.
func (l *Logger) ErrorWithFields(msg string, fields map[string]any) {
	args := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	l.Error(msg, args...)
}

// LoggerFromContext retrieves a logger from context or returns the default.
func LoggerFromContext(ctx context.Context) *Logger {
	if logger, ok := ctx.Value(loggerContextKey).(*Logger); ok {
		return logger
	}
	return GetLogger()
}

// ContextWithLogger adds a logger to the context.
func ContextWithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
}

type contextKey string

const loggerContextKey contextKey = "logger"
