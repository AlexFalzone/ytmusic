package logger

import (
	"bytes"
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

// The web server prefixes a job's logger with the job ID and the resolver
// prefixes it again per file: with jobs running side by side, a file's line
// must still say which job it belongs to.
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

	// With hasBar false, Info should write to the buffer.
	child.Info("before bar")
	if !strings.Contains(buf.String(), "before bar") {
		t.Fatalf("expected output before bar active, got: %q", buf.String())
	}
	buf.Reset()

	// Activate bar on parent; child should now suppress stdout output.
	l.SetProgressBar(true)
	child.Info("during bar")
	if buf.Len() > 0 {
		t.Errorf("child should suppress stdout when parent sets hasBar=true, got: %q", buf.String())
	}
	buf.Reset()

	// Deactivate bar on parent; output should resume.
	l.SetProgressBar(false)
	child.Info("after bar")
	if !strings.Contains(buf.String(), "after bar") {
		t.Errorf("child should resume stdout after bar deactivated, got: %q", buf.String())
	}
}

func TestDebugRaceCondition(t *testing.T) {
	// Run concurrent Debug and SetProgressBar calls to verify no data race under -race.
	l, _ := newTestLogger(true)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
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
