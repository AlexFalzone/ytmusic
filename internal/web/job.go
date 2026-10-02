package web

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"sync"
	"time"

	"ytmusic/internal/config"
)

type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

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

	// Created with the job, so a cancel request always has something to act on.
	ctx context.Context
}

// Accessors return copies: a pointer would let callers read while a job goroutine writes.
type JobManager struct {
	jobs      map[string]*Job
	mu        sync.RWMutex
	listeners map[string][]chan Job
}

const jobRetention = 1 * time.Hour

func NewJobManager() *JobManager {
	return &JobManager{
		jobs:      make(map[string]*Job),
		listeners: make(map[string][]chan Job),
	}
}

func (jm *JobManager) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				jm.cleanup()
			}
		}
	}()
}

func (jm *JobManager) cleanup() {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	cutoff := time.Now().Add(-jobRetention)
	for id, job := range jm.jobs {
		if job.CompletedAt != nil && job.CompletedAt.Before(cutoff) {
			// A listener left on an open channel nobody writes to blocks forever.
			for _, ch := range jm.listeners[id] {
				close(ch)
			}
			delete(jm.jobs, id)
			delete(jm.listeners, id)
		}
	}
}

func (jm *JobManager) CreateJob(parent context.Context, url string, cfg config.Config) (Job, error) {
	id, err := generateJobID()
	if err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithCancel(parent)

	jm.mu.Lock()
	defer jm.mu.Unlock()

	job := &Job{
		ID:        id,
		URL:       url,
		Config:    cfg,
		Status:    StatusPending,
		CreatedAt: time.Now(),
		Cancel:    cancel,
		ctx:       ctx,
	}

	jm.jobs[job.ID] = job
	return *job, nil
}

func (jm *JobManager) GetJob(id string) (Job, error) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("job not found: %s", id)
	}
	return *job, nil
}

// Newest first.
func (jm *JobManager) ListJobs(limit int) []Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, *job)
	}

	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})

	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs
}

func (jm *JobManager) UpdateJob(id string, fn func(*Job)) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	job, ok := jm.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	oldStatus := job.Status
	fn(job)

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

func (jm *JobManager) Subscribe(jobID string) <-chan Job {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	ch := make(chan Job, 10)
	jm.listeners[jobID] = append(jm.listeners[jobID], ch)
	return ch
}

func (jm *JobManager) Unsubscribe(jobID string, ch <-chan Job) {
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

// Caller holds jm.mu.
func (jm *JobManager) notifyListeners(jobID string, job *Job) {
	for _, ch := range jm.listeners[jobID] {
		select {
		case ch <- *job:
		default:
		}
	}
}

var randRead = rand.Read

func generateJobID() (string, error) {
	b := make([]byte, 8)
	if _, err := randRead(b); err != nil {
		return "", fmt.Errorf("generating job ID: %w", err)
	}
	return fmt.Sprintf("job_%x", b), nil
}
