package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Logger handles structured logging with optional file output
type Logger struct {
	Verbose bool
	writer  io.Writer
	mu      sync.Mutex
	fileLog *os.File
	hasBar  bool
}

// New creates a new Logger instance
func New(verbose bool) *Logger {
	return &Logger{
		Verbose: verbose,
		writer:  os.Stdout,
	}
}

// SetFileLog enables logging to a file
func (l *Logger) SetFileLog(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	l.fileLog = f
	return nil
}

// SetProgressBar indicates that a progress bar is active
func (l *Logger) SetProgressBar(active bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hasBar = active
}

// Close closes the log file if open
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.fileLog != nil {
		return l.fileLog.Close()
	}
	return nil
}

// Info logs informational messages
func (l *Logger) Info(format string, args ...interface{}) {
	l.log("INFO", format, args...)
}

// Debug logs detailed messages only in verbose mode
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.Verbose {
		l.log("DEBUG", format, args...)
	} else if l.fileLog != nil {
		// Always log debug to file even in non-verbose mode
		l.logToFile("DEBUG", format, args...)
	}
}

// Error logs error messages to stderr
func (l *Logger) Error(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf("[ERROR] "+format+"\n", args...)
	fmt.Fprint(os.Stderr, msg)

	if l.fileLog != nil {
		l.fileLog.WriteString(msg)
	}
}

// Warn logs warning messages
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log("WARN", format, args...)
}

// log handles the actual logging
func (l *Logger) log(level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var msg string
	if level == "INFO" {
		msg = fmt.Sprintf(format+"\n", args...)
	} else {
		msg = fmt.Sprintf("["+level+"] "+format+"\n", args...)
	}

	// Write to stdout (unless we have a progress bar and not verbose)
	if l.Verbose || !l.hasBar {
		fmt.Fprint(l.writer, msg)
	}

	// Always write to file if available
	if l.fileLog != nil {
		l.fileLog.WriteString(msg)
	}
}

// logToFile writes only to file
func (l *Logger) logToFile(level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.fileLog != nil {
		msg := fmt.Sprintf("["+level+"] "+format+"\n", args...)
		l.fileLog.WriteString(msg)
	}
}
