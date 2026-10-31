package web

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

func TestCleanup(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()

	old, err := jm.CreateJob(t.Context(), "https://example.com/old", cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := jm.UpdateJob(old.ID, func(j *Job) {
		j.Status = StatusCompleted
	}); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	jm.mu.Lock()
	past := time.Now().Add(-2 * time.Hour)
	jm.jobs[old.ID].CompletedAt = &past
	jm.mu.Unlock()

	recent, err := jm.CreateJob(t.Context(), "https://example.com/recent", cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := jm.UpdateJob(recent.ID, func(j *Job) {
		j.Status = StatusCompleted
	}); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	running, err := jm.CreateJob(t.Context(), "https://example.com/running", cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := jm.UpdateJob(running.ID, func(j *Job) {
		j.Status = StatusRunning
	}); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	jm.cleanup()

	if _, err := jm.GetJob(old.ID); err == nil {
		t.Error("old completed job should have been cleaned up")
	}
	if _, err := jm.GetJob(recent.ID); err != nil {
		t.Error("recent completed job should NOT have been cleaned up")
	}
	if _, err := jm.GetJob(running.ID); err != nil {
		t.Error("running job should NOT have been cleaned up")
	}
}

func TestCreateJobUniqueIDs(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()

	ids := make(map[string]bool)
	for range 100 {
		job, err := jm.CreateJob(t.Context(), "https://example.com", cfg)
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		if ids[job.ID] {
			t.Fatalf("duplicate job ID: %s", job.ID)
		}
		ids[job.ID] = true
	}
}

func TestJobIDFormat(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()

	job, err := jm.CreateJob(t.Context(), "https://example.com", cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if !strings.HasPrefix(job.ID, "job_") {
		t.Errorf("job ID should start with 'job_', got %q", job.ID)
	}
}

func TestUpdateJobTimestamps(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()
	job, err := jm.CreateJob(t.Context(), "https://example.com", cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := jm.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusRunning
	}); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	j, _ := jm.GetJob(job.ID)
	if j.StartedAt == nil {
		t.Error("StartedAt should be set when status changes to running")
	}

	if err := jm.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusCompleted
	}); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	j, _ = jm.GetJob(job.ID)
	if j.CompletedAt == nil {
		t.Error("CompletedAt should be set when status changes to completed")
	}
}

func TestUpdateJobNotFound(t *testing.T) {
	jm := NewJobManager()
	err := jm.UpdateJob("nonexistent", func(j *Job) {})
	if err == nil {
		t.Error("UpdateJob should return error for nonexistent job")
	}
}

func TestSubscribeReceivesUpdates(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()
	job, err := jm.CreateJob(t.Context(), "https://example.com", cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	ch := jm.Subscribe(job.ID)

	if err := jm.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusRunning
	}); err != nil {

		t.Fatalf("UpdateJob: %v", err)

	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for update")
	}
	if got, err := jm.GetJob(job.ID); err != nil || got.Status != StatusRunning {
		t.Errorf("after the signal the job is %q (%v), want running", got.Status, err)
	}

	jm.Unsubscribe(job.ID, ch)
}

func TestSignalsCoalesceAndTheFinalStateIsReadable(t *testing.T) {
	jm := NewJobManager()
	job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	ch := jm.Subscribe(job.ID)

	for i := range 49 {
		if err := jm.UpdateJob(job.ID, func(j *Job) { j.Progress = i }); err != nil {
			t.Fatalf("UpdateJob: %v", err)
		}
	}
	if err := jm.UpdateJob(job.ID, func(j *Job) { j.Status = StatusCompleted }); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	if n := len(ch); n != 1 {
		t.Errorf("%d signals pending, want exactly 1", n)
	}
	if got, _ := jm.GetJob(job.ID); got.Status != StatusCompleted {
		t.Errorf("status = %q, want completed: a slow listener must still read the final state", got.Status)
	}
}

func appendLines(t *testing.T, jm *JobManager, id string, n int) {
	t.Helper()
	for i := range n {
		jm.AppendLog(id, logger.Line{Time: time.Now(), Level: "INFO", Text: fmt.Sprint(i)})
	}
}

func TestAppendLogKeepsTheNewestLines(t *testing.T) {
	jm := NewJobManager()
	job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	appendLines(t, jm, job.ID, maxLogLines+3)

	lines, dropped, err := jm.LogSince(job.ID, 0)
	if err != nil {
		t.Fatalf("LogSince: %v", err)
	}
	if len(lines) != maxLogLines || dropped != 3 {
		t.Fatalf("got %d lines, %d dropped; want %d and 3", len(lines), dropped, maxLogLines)
	}
	if lines[0].Seq != 3 || lines[0].Text != "3" {
		t.Errorf("first kept line = %+v, want seq 3 text 3: the oldest go first", lines[0])
	}
}

func TestLogSinceReturnsOnlyNewerLines(t *testing.T) {
	jm := NewJobManager()
	job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	ch := jm.Subscribe(job.ID)
	at := time.Date(2026, 10, 19, 21, 4, 10, 0, time.Local)
	for i := range 3 {
		jm.AppendLog(job.ID, logger.Line{Time: at, Level: "DEBUG", Prefix: "[1/3]", Text: fmt.Sprint(i)})
	}

	if len(ch) != 1 {
		t.Error("appending a line did not signal the listener")
	}
	lines, dropped, err := jm.LogSince(job.ID, 2)
	if err != nil {
		t.Fatalf("LogSince: %v", err)
	}
	want := LogLine{Seq: 2, Time: "21:04:10", Level: "DEBUG", Prefix: "[1/3]", Text: "2"}
	if len(lines) != 1 || lines[0] != want || dropped != 0 {
		t.Errorf("LogSince(2) = %+v, %d dropped; want [%+v], 0", lines, dropped, want)
	}
}

func TestCleanupDropsTheLog(t *testing.T) {
	jm := NewJobManager()
	job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	appendLines(t, jm, job.ID, 2)

	completed := time.Now().Add(-2 * jobRetention)
	jm.mu.Lock()
	jm.jobs[job.ID].CompletedAt = &completed
	jm.mu.Unlock()
	jm.cleanup()

	if _, _, err := jm.LogSince(job.ID, 0); err == nil {
		t.Error("the log outlived its job")
	}
}

func TestJobCopiesDoNotShareWarnings(t *testing.T) {
	jm := NewJobManager()
	job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := jm.UpdateJob(job.ID, func(j *Job) { j.Warnings = append(j.Warnings, "a") }); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	got, _ := jm.GetJob(job.ID)
	got.Warnings[0] = "changed"

	if again, _ := jm.GetJob(job.ID); again.Warnings[0] != "a" {
		t.Errorf("warnings = %q: a copy wrote through to the job", again.Warnings)
	}
}

func TestCreateJobReturnsErrorWhenRandomFails(t *testing.T) {
	original := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("entropy exhausted") }
	defer func() { randRead = original }()

	jm := NewJobManager()
	if _, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig()); err == nil {
		t.Error("CreateJob must return an error when the random source fails")
	}
}

func TestCleanupClosesListenerChannels(t *testing.T) {
	jm := NewJobManager()

	job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	updates := jm.Subscribe(job.ID)

	completed := time.Now().Add(-2 * jobRetention)
	jm.mu.Lock()
	jm.jobs[job.ID].CompletedAt = &completed
	jm.mu.Unlock()

	jm.cleanup()

	for {
		select {
		case _, ok := <-updates:
			if !ok {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("listener channel was never closed after cleanup")
		}
	}
}

func TestCreateJobArmsCancelImmediately(t *testing.T) {
	jm := NewJobManager()

	job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if job.Cancel == nil {
		t.Fatal("a freshly created job has no cancel function")
	}
	if job.ctx == nil {
		t.Fatal("a freshly created job has no context")
	}

	select {
	case <-job.ctx.Done():
		t.Fatal("a fresh job context is already cancelled")
	default:
	}

	job.Cancel()

	select {
	case <-job.ctx.Done():
	case <-time.After(time.Second):
		t.Error("Cancel did not cancel the job context")
	}
}

func TestJobContextDerivesFromParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(t.Context())
	jm := NewJobManager()

	job, err := jm.CreateJob(parent, "https://example.com", config.DefaultConfig())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	cancelParent()

	select {
	case <-job.ctx.Done():
	case <-time.After(time.Second):
		t.Error("cancelling the parent did not reach the job")
	}
}

// The frontend shows the list as it comes.
func TestListJobsNewestFirst(t *testing.T) {
	jm := NewJobManager()
	var ids []string
	for i := range 3 {
		job, err := jm.CreateJob(t.Context(), "https://example.com", config.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		createdAt := time.Now().Add(time.Duration(i) * time.Minute)
		if err := jm.UpdateJob(job.ID, func(j *Job) { j.CreatedAt = createdAt }); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
	}

	jobs := jm.ListJobs(0)
	for i, job := range jobs {
		if want := ids[len(ids)-1-i]; job.ID != want {
			t.Errorf("jobs[%d] = %s, want %s", i, job.ID, want)
		}
	}
}
