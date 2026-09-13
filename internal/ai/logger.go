package ai

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/applog"
)

// bifrostLogger routes Bifrost's own diagnostics into Yuna's log. Without it
// Bifrost falls back to its default logger and the most useful line it emits --
// "primary provider X with model Y returned error: ..." when a fallback takes
// over -- never reaches the log file.
//
// Bifrost formats with fmt verbs ("%s", "%v"), so every call goes through
// Sprintf; applog then applies the debug switch, which keeps these lines quiet
// until runtime.debug is enabled.
type bifrostLogger struct {
	log *applog.Logger
}

// newBifrostLogger returns nil for a nil parent so callers can hand the result
// straight to BifrostConfig without a nil check.
func newBifrostLogger(log *applog.Logger) *bifrostLogger {
	if log == nil {
		return nil
	}
	return &bifrostLogger{log: log}
}

func (b *bifrostLogger) Debug(msg string, args ...any) {
	if b == nil || b.log == nil {
		return
	}
	// Bifrost emits this line after every successful request, where it
	// restates what the reply attribution already records and drowns the
	// lines about attempts and failures. The upstream call passes no
	// arguments, so matching the raw message is exact.
	if msg == noPrimaryErrorMessage {
		return
	}
	b.log.Debugf("%s", formatArgs(msg, args))
}

// noPrimaryErrorMessage is Bifrost's success-path line in shouldTryFallbacks.
const noPrimaryErrorMessage = "no primary error, we should not try fallbacks"

func (b *bifrostLogger) Info(msg string, args ...any) {
	if b == nil || b.log == nil {
		return
	}
	b.log.Infof("%s", formatArgs(msg, args))
}

func (b *bifrostLogger) Warn(msg string, args ...any) {
	if b == nil || b.log == nil {
		return
	}
	b.log.Warnf("%s", formatArgs(msg, args))
}

func (b *bifrostLogger) Error(msg string, args ...any) {
	if b == nil || b.log == nil {
		return
	}
	b.log.Errorf("%s", formatArgs(msg, args))
}

// Fatal logs at error level instead of exiting. Bifrost only reaches for Fatal
// in situations it cannot recover from, and crashing the process from inside a
// logging adapter would take Discord down with it.
func (b *bifrostLogger) Fatal(msg string, args ...any) {
	if b == nil || b.log == nil {
		return
	}
	b.log.Errorf("bifrost fatal: %s", formatArgs(msg, args))
}

// SetLevel and SetOutputType are no-ops: verbosity is owned by applog so a
// single runtime.debug switch controls both Yuna's lines and Bifrost's.
func (b *bifrostLogger) SetLevel(schemas.LogLevel)              {}
func (b *bifrostLogger) SetOutputType(schemas.LoggerOutputType) {}

// LogHTTPRequest returns a builder that folds its fields into one line when
// Send is called.
func (b *bifrostLogger) LogHTTPRequest(level schemas.LogLevel, msg string) schemas.LogEventBuilder {
	if b == nil || b.log == nil {
		return schemas.NoopLogEvent
	}
	return &logEvent{log: b.log, level: level, msg: msg}
}

// formatArgs applies Bifrost's fmt-style placeholders, falling back to the raw
// message when the format string has no verbs.
func formatArgs(msg string, args []any) string {
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// logEvent collects the key/value pairs of one Bifrost HTTP access log line and
// emits them as a single structured message.
type logEvent struct {
	log   *applog.Logger
	level schemas.LogLevel
	msg   string
	pairs []string
}

func (e *logEvent) Str(key, val string) schemas.LogEventBuilder {
	if e == nil {
		return schemas.NoopLogEvent
	}
	e.pairs = append(e.pairs, key+"="+val)
	return e
}

func (e *logEvent) Int(key string, val int) schemas.LogEventBuilder {
	return e.Int64(key, int64(val))
}

func (e *logEvent) Int64(key string, val int64) schemas.LogEventBuilder {
	if e == nil {
		return schemas.NoopLogEvent
	}
	e.pairs = append(e.pairs, fmt.Sprintf("%s=%d", key, val))
	return e
}

func (e *logEvent) Send() {
	if e == nil || e.log == nil {
		return
	}
	line := e.msg
	if len(e.pairs) > 0 {
		line += " [" + strings.Join(e.pairs, " ") + "]"
	}
	switch e.level {
	case schemas.LogLevelDebug:
		e.log.Debugf("%s", line)
	case schemas.LogLevelWarn:
		e.log.Warnf("%s", line)
	case schemas.LogLevelError:
		e.log.Errorf("%s", line)
	default:
		e.log.Infof("%s", line)
	}
}
