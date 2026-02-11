package web

import (
	"strings"
	"testing"
	"time"

	"ytmusic/internal/config"
)

func TestCleanup(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()

	// Create an old completed job (2 hours ago)
	old := jm.CreateJob("https://example.com/old", cfg)
	jm.UpdateJob(old.ID, func(j *Job) {
		j.Status = StatusCompleted
	})
	// Backdate CompletedAt
	jm.mu.Lock()
	past := time.Now().Add(-2 * time.Hour)
	jm.jobs[old.ID].CompletedAt = &past
	jm.mu.Unlock()

	// Create a recent completed job (5 minutes ago)
	recent := jm.CreateJob("https://example.com/recent", cfg)
	jm.UpdateJob(recent.ID, func(j *Job) {
		j.Status = StatusCompleted
	})

	// Create a running job (should never be cleaned)
	running := jm.CreateJob("https://example.com/running", cfg)
	jm.UpdateJob(running.ID, func(j *Job) {
		j.Status = StatusRunning
	})

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
	for i := 0; i < 100; i++ {
		job := jm.CreateJob("https://example.com", cfg)
		if ids[job.ID] {
			t.Fatalf("duplicate job ID: %s", job.ID)
		}
		ids[job.ID] = true
	}
}

func TestJobIDFormat(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()

	job := jm.CreateJob("https://example.com", cfg)
	if !strings.HasPrefix(job.ID, "job_") {
		t.Errorf("job ID should start with 'job_', got %q", job.ID)
	}
}

func TestUpdateJobTimestamps(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()
	job := jm.CreateJob("https://example.com", cfg)

	// Pending → Running should set StartedAt
	jm.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusRunning
	})
	j, _ := jm.GetJob(job.ID)
	if j.StartedAt == nil {
		t.Error("StartedAt should be set when status changes to running")
	}

	// Running → Completed should set CompletedAt
	jm.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusCompleted
	})
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
	job := jm.CreateJob("https://example.com", cfg)

	ch := jm.Subscribe(job.ID)

	jm.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusRunning
	})

	select {
	case update := <-ch:
		if update.Status != StatusRunning {
			t.Errorf("expected status running, got %s", update.Status)
		}
	case <-time.After(time.Second):
		t.Error("timed out waiting for update")
	}

	jm.Unsubscribe(job.ID, ch)
}
