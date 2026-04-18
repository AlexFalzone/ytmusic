package logger

import (
	"bytes"
	"strings"
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
