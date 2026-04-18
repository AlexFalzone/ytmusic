package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type Logger struct {
	Verbose bool
	writer  io.Writer
	mu      *sync.Mutex
	fileLog *os.File
	hasBar  *bool
	prefix  string
}

func New(verbose bool) *Logger {
	hasBar := false
	return &Logger{
		Verbose: verbose,
		writer:  os.Stdout,
		mu:      &sync.Mutex{},
		hasBar:  &hasBar,
	}
}

// WithPrefix returns a child logger that prepends [prefix] to every message body.
// The child shares the parent's writer, fileLog, and mutex so concurrent writes remain safe.
func (l *Logger) WithPrefix(prefix string) *Logger {
	return &Logger{
		Verbose: l.Verbose,
		writer:  l.writer,
		mu:      l.mu,
		fileLog: l.fileLog,
		hasBar:  l.hasBar, // shared pointer so SetProgressBar on parent propagates to all children
		prefix:  prefix,
	}
}

func (l *Logger) SetFileLog(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	l.fileLog = f
	return nil
}

func (l *Logger) SetProgressBar(active bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.hasBar = active
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fileLog != nil {
		return l.fileLog.Close()
	}
	return nil
}

func (l *Logger) Info(format string, args ...interface{}) {
	l.log("INFO", format, args...)
}

func (l *Logger) Debug(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debugLocked(format, args...)
}

// debugLocked writes a DEBUG line; caller must hold l.mu.
// Always captures debug detail in the file even when not printing to stdout.
func (l *Logger) debugLocked(format string, args ...interface{}) {
	if l.Verbose {
		msg := l.formatMsg("DEBUG", format, args...)
		fmt.Fprint(l.writer, msg)
		if l.fileLog != nil {
			l.fileLog.WriteString(msg) //nolint:errcheck — best-effort file write
		}
	} else if l.fileLog != nil {
		msg := l.formatMsg("DEBUG", format, args...)
		l.fileLog.WriteString(msg) //nolint:errcheck — best-effort file write
	}
}

func (l *Logger) Error(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := l.formatMsg("ERROR", format, args...)
	fmt.Fprint(os.Stderr, msg)
	if l.fileLog != nil {
		l.fileLog.WriteString(msg) //nolint:errcheck — best-effort file write
	}
}

func (l *Logger) Warn(format string, args ...interface{}) {
	l.log("WARN", format, args...)
}

func (l *Logger) log(level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := l.formatMsg(level, format, args...)
	if l.Verbose || !*l.hasBar {
		fmt.Fprint(l.writer, msg)
	}
	if l.fileLog != nil {
		l.fileLog.WriteString(msg) //nolint:errcheck — best-effort file write
	}
}

func (l *Logger) formatMsg(level, format string, args ...interface{}) string {
	ts := time.Now().Format("2006-01-02 15:04:05")
	body := fmt.Sprintf(format, args...)
	if l.prefix != "" {
		return fmt.Sprintf("%s [%s] [%s] %s\n", ts, level, l.prefix, body)
	}
	return fmt.Sprintf("%s [%s] %s\n", ts, level, body)
}
