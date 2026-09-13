// Package applog provides a small leveled logger that writes human-readable
// lines to stdout and to a size-rotated file at the same time.
package applog

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Level is a log severity. Lower values are more verbose.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) label() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func parseLevel(debug bool) Level {
	if debug {
		return LevelDebug
	}
	return LevelInfo
}

// Logger writes leveled log lines. The zero value is not usable; build one
// with New. Derived loggers from Named share the parent's sink.
type Logger struct {
	sink     *log.Logger
	mu       *sync.Mutex
	closer   io.Closer
	name     string
	minLevel Level
}

// New builds a logger writing to stdout and, when filePath is non-empty, to a
// rotating file (10 MiB per file, 3 backups, compressed).
func New(filePath string, debug bool) (*Logger, error) {
	var sinks []io.Writer
	sinks = append(sinks, os.Stdout)

	var closer io.Closer
	if filePath != "" {
		if dir := filepath.Dir(filePath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create log directory %q: %w", dir, err)
			}
		}
		file := &lumberjack.Logger{
			Filename:   filePath,
			MaxSize:    10,
			MaxBackups: 3,
			MaxAge:     28,
			Compress:   true,
		}
		sinks = append(sinks, file)
		closer = file
	}

	return &Logger{
		sink:     log.New(io.MultiWriter(sinks...), "", log.LstdFlags),
		mu:       new(sync.Mutex),
		closer:   closer,
		minLevel: parseLevel(debug),
	}, nil
}

// Named returns a logger that tags every line with name. It shares the parent
// sink and lock, so lines never interleave.
func (l *Logger) Named(name string) *Logger {
	if l == nil {
		return nil
	}
	return &Logger{sink: l.sink, mu: l.mu, closer: l.closer, name: name, minLevel: l.minLevel}
}

// Close releases the rotating log file. It is a no-op for a logger that only
// writes to stdout, and safe to call on a logger whose file is already closed.
func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.closer.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	l.closer = nil
	return nil
}

func (l *Logger) logf(level Level, format string, args ...any) {
	if l == nil || level < l.minLevel {
		return
	}
	tag := level.label()
	if l.name != "" {
		tag = l.name + " " + tag
	}
	msg := fmt.Sprintf(format, args...)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sink.Printf("[%s] %s", tag, msg)
}

func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, format, args...) }
