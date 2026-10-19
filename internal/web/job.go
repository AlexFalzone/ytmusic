package web

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"
)

type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

func (s JobStatus) finished() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

type Job struct {
	ID          string
	URL         string
	Config      config.Config
	Status      JobStatus
	Progress    int
	Total       int
	Error       string
	Warnings    []string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	Cancel      context.CancelFunc

	// Created with the job, so a cancel request always has something to act on.
	ctx context.Context
}

// A copy whose slices are its own, so it can be read while the job goroutine appends.
func (j *Job) snapshot() Job {
	c := *j
	c.Warnings = slices.Clone(j.Warnings)
	return c
}

// One line of a job's log as the page shows it. Seq counts from the job's first line, dropped ones included.
type LogLine struct {
	Seq    int    `json:"seq"`
	Time   string `json:"time"`
	Level  string `json:"level"`
	Prefix string `json:"prefix"`
	Text   string `json:"text"`
}

type jobLog struct {
	lines   []LogLine
	next    int
	dropped int
}

// About 1 MB at most; an album of ~300 tracks fits with its debug lines.
const maxLogLines = 5000

// Accessors return copies: a pointer would let callers read while a job goroutine writes.
// Never log while holding mu: a job's logger writes into the manager under the logger's own lock.
type JobManager struct {
	jobs      map[string]*Job
	logs      map[string]*jobLog
	mu        sync.RWMutex
	listeners map[string][]chan struct{}
}

const jobRetention = 1 * time.Hour

func NewJobManager() *JobManager {
	return &JobManager{
		jobs:      make(map[string]*Job),
		logs:      make(map[string]*jobLog),
		listeners: make(map[string][]chan struct{}),
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
			delete(jm.logs, id)
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
	jm.logs[job.ID] = &jobLog{}
	return job.snapshot(), nil
}

func (jm *JobManager) GetJob(id string) (Job, error) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("job not found: %s", id)
	}
	return job.snapshot(), nil
}

// Newest first.
func (jm *JobManager) ListJobs(limit int) []Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	jobs := make([]Job, 0, len(jm.jobs))
	for _, job := range jm.jobs {
		jobs = append(jobs, job.snapshot())
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

	jm.notifyListeners(id)
	return nil
}

// Unknown ids are ignored: a line can arrive after its job was cleaned up.
func (jm *JobManager) AppendLog(id string, line logger.Line) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	l, ok := jm.logs[id]
	if !ok {
		return
	}
	if len(l.lines) == maxLogLines {
		l.lines = slices.Delete(l.lines, 0, 1)
		l.dropped++
	}
	l.lines = append(l.lines, LogLine{
		Seq:    l.next,
		Time:   line.Time.Format(time.TimeOnly),
		Level:  line.Level,
		Prefix: line.Prefix,
		Text:   line.Text,
	})
	l.next++
	jm.notifyListeners(id)
}

// The lines numbered seq and after, and how many lines the job has dropped so far.
func (jm *JobManager) LogSince(id string, seq int) ([]LogLine, int, error) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	l, ok := jm.logs[id]
	if !ok {
		return nil, 0, fmt.Errorf("job not found: %s", id)
	}
	first := 0
	if len(l.lines) > 0 {
		first = min(max(seq-l.lines[0].Seq, 0), len(l.lines))
	}
	return slices.Clone(l.lines[first:]), l.dropped, nil
}

// A signal, not the job: the listener reads the current state, so a signal it missed loses nothing.
func (jm *JobManager) Subscribe(jobID string) <-chan struct{} {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	ch := make(chan struct{}, 1)
	jm.listeners[jobID] = append(jm.listeners[jobID], ch)
	return ch
}

func (jm *JobManager) Unsubscribe(jobID string, ch <-chan struct{}) {
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

// Caller holds jm.mu. A signal already pending covers this one.
func (jm *JobManager) notifyListeners(jobID string) {
	for _, ch := range jm.listeners[jobID] {
		select {
		case ch <- struct{}{}:
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
