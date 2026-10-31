package logger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestLogger(verbose bool) (*Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := New(verbose)
	l.writer = buf
	return l, buf
}

func TestInfoHasTimestampAndPrefix(t *testing.T) {
	l, buf := newTestLogger(false)
	l.Info("hello world")
	out := buf.String()
	if !strings.Contains(out, "[INFO]") {
		t.Errorf("expected [INFO] prefix, got: %q", out)
	}
	if len(out) < 20 {
		t.Errorf("expected timestamp in output, got: %q", out)
	}
}

func TestWarnHasTimestampAndPrefix(t *testing.T) {
	l, buf := newTestLogger(false)
	l.Warn("something wrong")
	out := buf.String()
	if !strings.Contains(out, "[WARN]") {
		t.Errorf("expected [WARN] prefix, got: %q", out)
	}
}

func TestDebugOnlyInVerbose(t *testing.T) {
	l, buf := newTestLogger(false)
	l.Debug("secret")
	if buf.Len() > 0 {
		t.Errorf("Debug should not write to stdout in non-verbose mode, got: %q", buf.String())
	}

	lv, bufv := newTestLogger(true)
	lv.Debug("secret")
	if !strings.Contains(bufv.String(), "[DEBUG]") {
		t.Errorf("expected [DEBUG] prefix in verbose mode, got: %q", bufv.String())
	}
}

func TestWithPrefixPrependsToMessages(t *testing.T) {
	l, buf := newTestLogger(false)
	prefixed := l.WithPrefix("job_abc123")
	prefixed.Info("starting")
	out := buf.String()
	if !strings.Contains(out, "[job_abc123]") {
		t.Errorf("expected [job_abc123] in output, got: %q", out)
	}
	if !strings.Contains(out, "[INFO]") {
		t.Errorf("expected [INFO] prefix retained, got: %q", out)
	}
}

func TestWithPrefixKeepsTheParentPrefix(t *testing.T) {
	l, buf := newTestLogger(false)
	l.WithPrefix("job_42").WithPrefix("3/20").Info("processing")

	if out := buf.String(); !strings.Contains(out, "[INFO] [job_42] [3/20] processing") {
		t.Errorf("want both prefixes in order, got: %q", out)
	}
}

func TestWithPrefixSharesWriter(t *testing.T) {
	l, buf := newTestLogger(false)
	prefixed := l.WithPrefix("job_1")
	l.Info("from root")
	prefixed.Info("from prefixed")
	out := buf.String()
	if !strings.Contains(out, "from root") || !strings.Contains(out, "from prefixed") {
		t.Errorf("both root and prefixed should write to same buffer, got: %q", out)
	}
}

func TestWithPrefixSharesHasBar(t *testing.T) {
	l, buf := newTestLogger(false)
	child := l.WithPrefix("child")

	child.Info("before bar")
	if !strings.Contains(buf.String(), "before bar") {
		t.Fatalf("expected output before bar active, got: %q", buf.String())
	}
	buf.Reset()

	l.SetProgressBar(true)
	child.Info("during bar")
	if buf.Len() > 0 {
		t.Errorf("child should suppress stdout when parent sets hasBar=true, got: %q", buf.String())
	}
	buf.Reset()

	l.SetProgressBar(false)
	child.Info("after bar")
	if !strings.Contains(buf.String(), "after bar") {
		t.Errorf("child should resume stdout after bar deactivated, got: %q", buf.String())
	}
}

func TestDebugRaceCondition(t *testing.T) {
	l, _ := newTestLogger(true)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			l.Debug("concurrent debug %d", i)
		}()
		go func(v bool) {
			defer wg.Done()
			l.SetProgressBar(v)
		}(i%2 == 0)
	}
	wg.Wait()
}

func collectLines(lines *[]Line) func(Line) {
	return func(l Line) { *lines = append(*lines, l) }
}

func TestSinkGetsEveryLevelEvenWithoutVerbose(t *testing.T) {
	l, _ := newTestLogger(false)
	var got []Line
	job := l.WithPrefix("job_1").WithSink(collectLines(&got))

	job.Debug("d")
	job.Info("i")
	job.Warn("w")
	job.Error("e")

	var levels, texts, prefixes []string
	for _, line := range got {
		levels = append(levels, line.Level)
		texts = append(texts, line.Text)
		prefixes = append(prefixes, line.Prefix)
		if line.Time.IsZero() {
			t.Errorf("line %q has no time", line.Text)
		}
	}
	if strings.Join(levels, " ") != "DEBUG INFO WARN ERROR" || strings.Join(texts, " ") != "d i w e" {
		t.Errorf("sink got levels %q texts %q, want DEBUG INFO WARN ERROR / d i w e", levels, texts)
	}
	if strings.Join(prefixes, "") != "" {
		t.Errorf("prefixes = %q, want none: the sink's own prefix is implied", prefixes)
	}
}

func TestSinkIsInheritedWithoutTheOwnersPrefix(t *testing.T) {
	l, buf := newTestLogger(false)
	var got []Line
	l.WithPrefix("job_1").WithSink(collectLines(&got)).WithPrefix("3/20").Info("x")
	l.Info("from the root")

	if len(got) != 1 || got[0].Prefix != "[3/20]" || got[0].Text != "x" {
		t.Errorf("sink got %+v, want one line [3/20] x", got)
	}
	if !strings.Contains(buf.String(), "[job_1] [3/20] x") {
		t.Errorf("stdout lost the full prefix: %q", buf.String())
	}
}

// Titles and yt-dlp's messages come from the web, and reach the terminal.
func TestArgumentsCannotDriveTheTerminalOrForgeALine(t *testing.T) {
	l, buf := newTestLogger(false)

	l.Info("title: %s, reason: %v", "a\x1b[2Jb\nFAKE [ERROR] line", errors.New("c\x07d"))

	out := buf.String()
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("control characters reached the output: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("one call wrote %d lines: %q", strings.Count(out, "\n"), out)
	}
	if !strings.Contains(out, "title: a[2Jb FAKE [ERROR] line, reason: cd") {
		t.Errorf("out = %q", out)
	}
}

// A stack trace is ours, and useless on one line.
func TestFormatNewlinesAndStackTracesSurvive(t *testing.T) {
	l, buf := newTestLogger(true)

	l.Debug("panic: %v\n%s", "boom", []byte("goroutine 1\nmain.go:1"))

	if !strings.Contains(buf.String(), "panic: boom\ngoroutine 1\nmain.go:1") {
		t.Errorf("out = %q", buf.String())
	}
}

func TestLogFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	l := New(false)
	if err := l.SetFileLog(path); err != nil {
		t.Fatalf("SetFileLog: %v", err)
	}
	defer func() { _ = l.Close() }() // only the mode matters here

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600: the log holds playlist URLs", info.Mode().Perm())
	}
}
