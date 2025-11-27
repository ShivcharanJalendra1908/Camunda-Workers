package logger

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
)

// Logger defines the minimal logging interface used across workers.
type Logger interface {
	Debug(msg string, fields map[string]interface{})
	Info(msg string, fields map[string]interface{})
	Warn(msg string, fields map[string]interface{})
	Error(msg string, fields map[string]interface{})
	WithFields(fields map[string]interface{}) Logger
	WithError(err error) Logger
	With(fields map[string]interface{}) Logger // Added for compatibility with worker interfaces
}

func New(levelStr, format string) *zap.Logger {
	level := zapcore.InfoLevel
	switch levelStr {
	case "debug":
		level = zapcore.DebugLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	}

	var cfg zap.Config
	if format == "json" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
	}
	cfg.Level = zap.NewAtomicLevelAt(level)
	logger, _ := cfg.Build()
	return logger
}

// zapWrapper adapts zap.Logger to the Logger interface.
type zapWrapper struct {
	l *zap.Logger
}

func (z *zapWrapper) Debug(msg string, fields map[string]interface{}) {
	z.l.Debug(msg, mapToZapFields(fields)...)
}

func (z *zapWrapper) Info(msg string, fields map[string]interface{}) {
	z.l.Info(msg, mapToZapFields(fields)...)
}

func (z *zapWrapper) Warn(msg string, fields map[string]interface{}) {
	z.l.Warn(msg, mapToZapFields(fields)...)
}

func (z *zapWrapper) Error(msg string, fields map[string]interface{}) {
	z.l.Error(msg, mapToZapFields(fields)...)
}

func (z *zapWrapper) WithFields(fields map[string]interface{}) Logger {
	return &zapWrapper{
		l: z.l.With(mapToZapFields(fields)...),
	}
}

func (z *zapWrapper) WithError(err error) Logger {
	return &zapWrapper{
		l: z.l.With(zap.Error(err)),
	}
}

// With is an alias for WithFields to match worker interface expectations
func (z *zapWrapper) With(fields map[string]interface{}) Logger {
	return &zapWrapper{
		l: z.l.With(mapToZapFields(fields)...),
	}
}

func mapToZapFields(fields map[string]interface{}) []zap.Field {
	if len(fields) == 0 {
		return nil
	}
	out := make([]zap.Field, 0, len(fields))
	for k, v := range fields {
		out = append(out, zap.Any(k, v))
	}
	return out
}

// NewStructured creates a Logger that logs using zap under the hood.
func NewStructured(levelStr, format string) Logger {
	return &zapWrapper{l: New(levelStr, format)}
}

// NewZapAdapter wraps an existing *zap.Logger to implement the Logger interface
func NewZapAdapter(l *zap.Logger) Logger {
	return &zapWrapper{l: l}
}

// NewTestLogger creates a Logger suitable for testing that outputs to testing.T
func NewTestLogger(t testing.TB) Logger {
	return &zapWrapper{l: zaptest.NewLogger(t)}
}

// NewNoOpLogger creates a Logger that doesn't output anything (useful for tests)
func NewNoOpLogger() Logger {
	return &zapWrapper{l: zap.NewNop()}
}

// package logger

// import (
// 	"testing"

// 	"go.uber.org/zap"
// 	"go.uber.org/zap/zapcore"
// 	"go.uber.org/zap/zaptest"
// )

// // Logger defines the minimal logging interface used across workers.
// type Logger interface {
// 	Debug(msg string, fields map[string]interface{})
// 	Info(msg string, fields map[string]interface{})
// 	Warn(msg string, fields map[string]interface{})
// 	Error(msg string, fields map[string]interface{})
// 	WithFields(fields map[string]interface{}) Logger
// 	WithError(err error) Logger
// }

// func New(levelStr, format string) *zap.Logger {
// 	level := zapcore.InfoLevel
// 	switch levelStr {
// 	case "debug":
// 		level = zapcore.DebugLevel
// 	case "warn":
// 		level = zapcore.WarnLevel
// 	case "error":
// 		level = zapcore.ErrorLevel
// 	}

// 	var cfg zap.Config
// 	if format == "json" {
// 		cfg = zap.NewProductionConfig()
// 	} else {
// 		cfg = zap.NewDevelopmentConfig()
// 	}
// 	cfg.Level = zap.NewAtomicLevelAt(level)
// 	logger, _ := cfg.Build()
// 	return logger
// }

// // zapWrapper adapts zap.Logger to the Logger interface.
// type zapWrapper struct {
// 	l *zap.Logger
// }

// func (z *zapWrapper) Debug(msg string, fields map[string]interface{}) {
// 	z.l.Debug(msg, mapToZapFields(fields)...)
// }

// func (z *zapWrapper) Info(msg string, fields map[string]interface{}) {
// 	z.l.Info(msg, mapToZapFields(fields)...)
// }

// func (z *zapWrapper) Warn(msg string, fields map[string]interface{}) {
// 	z.l.Warn(msg, mapToZapFields(fields)...)
// }

// func (z *zapWrapper) Error(msg string, fields map[string]interface{}) {
// 	z.l.Error(msg, mapToZapFields(fields)...)
// }

// func (z *zapWrapper) WithFields(fields map[string]interface{}) Logger {
// 	return &zapWrapper{
// 		l: z.l.With(mapToZapFields(fields)...),
// 	}
// }

// func (z *zapWrapper) WithError(err error) Logger {
// 	return &zapWrapper{
// 		l: z.l.With(zap.Error(err)),
// 	}
// }

// func mapToZapFields(fields map[string]interface{}) []zap.Field {
// 	if len(fields) == 0 {
// 		return nil
// 	}
// 	out := make([]zap.Field, 0, len(fields))
// 	for k, v := range fields {
// 		out = append(out, zap.Any(k, v))
// 	}
// 	return out
// }

// // NewStructured creates a Logger that logs using zap under the hood.
// func NewStructured(levelStr, format string) Logger {
// 	return &zapWrapper{l: New(levelStr, format)}
// }

// // NewZapAdapter wraps an existing *zap.Logger to implement the Logger interface
// func NewZapAdapter(l *zap.Logger) Logger {
// 	return &zapWrapper{l: l}
// }

// // NewTestLogger creates a Logger suitable for testing that outputs to testing.T
// func NewTestLogger(t testing.TB) Logger {
// 	return &zapWrapper{l: zaptest.NewLogger(t)}
// }

// // NewNoOpLogger creates a Logger that doesn't output anything (useful for tests)
// func NewNoOpLogger() Logger {
// 	return &zapWrapper{l: zap.NewNop()}
// }

// package logger

// import (
// 	"go.uber.org/zap"
// 	"go.uber.org/zap/zapcore"
// )

// // Logger defines the minimal logging interface used across workers.
// type Logger interface {
// 	Debug(msg string, fields map[string]interface{})
// 	Info(msg string, fields map[string]interface{})
// 	Warn(msg string, fields map[string]interface{})
// 	Error(msg string, fields map[string]interface{})
// }

// func New(levelStr, format string) *zap.Logger {
// 	level := zapcore.InfoLevel
// 	switch levelStr {
// 	case "debug":
// 		level = zapcore.DebugLevel
// 	case "warn":
// 		level = zapcore.WarnLevel
// 	case "error":
// 		level = zapcore.ErrorLevel
// 	}

// 	var cfg zap.Config
// 	if format == "json" {
// 		cfg = zap.NewProductionConfig()
// 	} else {
// 		cfg = zap.NewDevelopmentConfig()
// 	}
// 	cfg.Level = zap.NewAtomicLevelAt(level)
// 	logger, _ := cfg.Build()
// 	return logger
// }

// // zapWrapper adapts zap.Logger to the Logger interface.
// type zapWrapper struct {
// 	l *zap.Logger
// }

// func (z *zapWrapper) Debug(msg string, fields map[string]interface{}) {
// 	z.l.Debug(msg, mapToZapFields(fields)...)
// }

// func (z *zapWrapper) Info(msg string, fields map[string]interface{}) {
// 	z.l.Info(msg, mapToZapFields(fields)...)
// }

// func (z *zapWrapper) Warn(msg string, fields map[string]interface{}) {
// 	z.l.Warn(msg, mapToZapFields(fields)...)
// }

// func (z *zapWrapper) Error(msg string, fields map[string]interface{}) {
// 	z.l.Error(msg, mapToZapFields(fields)...)
// }

// func mapToZapFields(fields map[string]interface{}) []zap.Field {
// 	if len(fields) == 0 {
// 		return nil
// 	}
// 	out := make([]zap.Field, 0, len(fields))
// 	for k, v := range fields {
// 		out = append(out, zap.Any(k, v))
// 	}
// 	return out
// }

// // NewStructured creates a Logger that logs using zap under the hood.
// func NewStructured(levelStr, format string) Logger {
// 	return &zapWrapper{l: New(levelStr, format)}
// }
