package web

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ytmusic/internal/config"
)

func TestCleanup(t *testing.T) {
	jm := NewJobManager()
	cfg := config.DefaultConfig()

	old, err := jm.CreateJob(context.Background(), "https://example.com/old", cfg)
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

	recent, err := jm.CreateJob(context.Background(), "https://example.com/recent", cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := jm.UpdateJob(recent.ID, func(j *Job) {
		j.Status = StatusCompleted
	}); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	running, err := jm.CreateJob(context.Background(), "https://example.com/running", cfg)
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
	for i := 0; i < 100; i++ {
		job, err := jm.CreateJob(context.Background(), "https://example.com", cfg)
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

	job, err := jm.CreateJob(context.Background(), "https://example.com", cfg)
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
	job, err := jm.CreateJob(context.Background(), "https://example.com", cfg)
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
	job, err := jm.CreateJob(context.Background(), "https://example.com", cfg)
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
	case update := <-ch:
		if update.Status != StatusRunning {
			t.Errorf("expected status running, got %s", update.Status)
		}
	case <-time.After(time.Second):
		t.Error("timed out waiting for update")
	}

	jm.Unsubscribe(job.ID, ch)
}

func TestCreateJobReturnsErrorWhenRandomFails(t *testing.T) {
	original := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("entropy exhausted") }
	defer func() { randRead = original }()

	jm := NewJobManager()
	if _, err := jm.CreateJob(context.Background(), "https://example.com", config.DefaultConfig()); err == nil {
		t.Error("CreateJob must return an error when the random source fails")
	}
}

func TestCleanupClosesListenerChannels(t *testing.T) {
	jm := NewJobManager()

	job, err := jm.CreateJob(context.Background(), "https://example.com", config.DefaultConfig())
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

	job, err := jm.CreateJob(context.Background(), "https://example.com", config.DefaultConfig())
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
	parent, cancelParent := context.WithCancel(context.Background())
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
		job, err := jm.CreateJob(context.Background(), "https://example.com", config.DefaultConfig())
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
