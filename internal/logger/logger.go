package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	Verbose  bool
	writer   io.Writer
	mu       *sync.Mutex
	fileLog  *os.File
	hasBar   *bool
	prefix   string // "[job] [3/20]": outermost first
	sink     func(Line)
	sinkFrom int // length of the prefix the sink's owner had: the part it already implies
}

// One log line as a sink receives it.
type Line struct {
	Time   time.Time
	Level  string
	Prefix string
	Text   string
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

func (l *Logger) WithPrefix(prefix string) *Logger {
	tag := "[" + prefix + "]"
	if l.prefix != "" {
		tag = l.prefix + " " + tag
	}
	return &Logger{
		Verbose:  l.Verbose,
		writer:   l.writer,
		mu:       l.mu,
		fileLog:  l.fileLog,
		hasBar:   l.hasBar,
		prefix:   tag,
		sink:     l.sink,
		sinkFrom: l.sinkFrom,
	}
}

// Every line of the copy and of its WithPrefix children also goes to sink, at every level, debug included.
func (l *Logger) WithSink(sink func(Line)) *Logger {
	c := *l
	c.sink = sink
	c.sinkFrom = len(l.prefix)
	return &c
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

func (l *Logger) Info(format string, args ...any) {
	l.log("INFO", format, args...)
}

func (l *Logger) Debug(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debugLocked(format, args...)
}

// Caller holds l.mu. The file and the sink get debug lines even when stdout does not.
func (l *Logger) debugLocked(format string, args ...any) {
	if !l.Verbose && l.fileLog == nil && l.sink == nil {
		return
	}
	msg := l.record("DEBUG", format, args...)
	if l.Verbose {
		fmt.Fprint(l.writer, msg)
	}
	l.writeFileLocked(msg)
}

func (l *Logger) Error(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := l.record("ERROR", format, args...)
	fmt.Fprint(os.Stderr, msg)
	l.writeFileLocked(msg)
}

func (l *Logger) Warn(format string, args ...any) {
	l.log("WARN", format, args...)
}

func (l *Logger) log(level, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := l.record(level, format, args...)
	if l.Verbose || !*l.hasBar {
		fmt.Fprint(l.writer, msg)
	}
	l.writeFileLocked(msg)
}

// Caller holds l.mu.
func (l *Logger) writeFileLocked(msg string) {
	if l.fileLog != nil {
		_, _ = l.fileLog.WriteString(msg) // a logger has nowhere to report its own failure
	}
}

// Caller holds l.mu, so the sink sees lines in the order the file does. Returns the line as the file gets it.
func (l *Logger) record(level, format string, args ...any) string {
	now := time.Now()
	body := fmt.Sprintf(format, args...)
	if l.sink != nil {
		l.sink(Line{Time: now, Level: level, Prefix: strings.TrimSpace(l.prefix[l.sinkFrom:]), Text: body})
	}
	ts := now.Format(time.DateTime)
	if l.prefix != "" {
		return fmt.Sprintf("%s [%s] %s %s\n", ts, level, l.prefix, body)
	}
	return fmt.Sprintf("%s [%s] %s\n", ts, level, body)
}
