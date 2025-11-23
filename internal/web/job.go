package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ytmusic/internal/config"
)

// JobStatus represents the current status of a job
type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusRunning    JobStatus = "running"
	StatusCompleted  JobStatus = "completed"
	StatusFailed     JobStatus = "failed"
	StatusCancelled  JobStatus = "cancelled"
)

// Job represents a download job
type Job struct {
	ID          string
	URL         string
	Config      config.Config
	Status      JobStatus
	Progress    int
	Total       int
	Error       string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	Cancel      context.CancelFunc
}

// JobManager manages download jobs
type JobManager struct {
	jobs      map[string]*Job
	mu        sync.RWMutex
	listeners map[string][]chan *Job
}

// NewJobManager creates a new job manager
func NewJobManager() *JobManager {
	return &JobManager{
		jobs:      make(map[string]*Job),
		listeners: make(map[string][]chan *Job),
	}
}

// CreateJob creates a new job
func (jm *JobManager) CreateJob(url string, cfg config.Config) *Job {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job := &Job{
		ID:        generateJobID(),
		URL:       url,
		Config:    cfg,
		Status:    StatusPending,
		CreatedAt: time.Now(),
	}

	jm.jobs[job.ID] = job
	return job
}

// GetJob retrieves a job by ID
func (jm *JobManager) GetJob(id string) (*Job, error) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", id)
	}
	return job, nil
}

// ListJobs returns all jobs
func (jm *JobManager) ListJobs() []*Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]*Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// UpdateJob updates job status
func (jm *JobManager) UpdateJob(id string, fn func(*Job)) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, ok := jm.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	oldStatus := job.Status
	fn(job)

	// Update timestamps based on status changes
	if oldStatus != job.Status {
		switch job.Status {
		case StatusRunning:
			if job.StartedAt == nil {
				now := time.Now()
				job.StartedAt = &now
			}
		case StatusCompleted, StatusFailed, StatusCancelled:
			if job.CompletedAt == nil {
				now := time.Now()
				job.CompletedAt = &now
			}
		}
	}

	jm.notifyListeners(id, job)
	return nil
}

// Subscribe subscribes to job updates
func (jm *JobManager) Subscribe(jobID string) <-chan *Job {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	ch := make(chan *Job, 10)
	jm.listeners[jobID] = append(jm.listeners[jobID], ch)
	return ch
}

// Unsubscribe removes a listener
func (jm *JobManager) Unsubscribe(jobID string, ch <-chan *Job) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	listeners := jm.listeners[jobID]
	for i, listener := range listeners {
		if listener == ch {
			jm.listeners[jobID] = append(listeners[:i], listeners[i+1:]...)
			close(listener)
			break
		}
	}
}

// notifyListeners sends updates to all listeners
func (jm *JobManager) notifyListeners(jobID string, job *Job) {
	for _, ch := range jm.listeners[jobID] {
		select {
		case ch <- job:
		default:
		}
	}
}

// generateJobID generates a unique job ID
func generateJobID() string {
	return fmt.Sprintf("job_%d", time.Now().UnixNano())
}
