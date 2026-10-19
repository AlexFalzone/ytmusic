package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"ytmusic/internal/logger"
	"ytmusic/internal/pipeline"
	"ytmusic/pkg/utils"
)

type DownloadRequest struct {
	URL string `json:"url"`
}

type JobResponse struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Status      JobStatus `json:"status"`
	Progress    int       `json:"progress"`
	Total       int       `json:"total"`
	Error       string    `json:"error,omitempty"`
	Warnings    []string  `json:"warnings,omitempty"`
	CreatedAt   string    `json:"created_at"`
	StartedAt   *string   `json:"started_at,omitempty"`
	CompletedAt *string   `json:"completed_at,omitempty"`
}

type LogResponse struct {
	Lines   []LogLine `json:"lines"`
	Dropped int       `json:"dropped"`
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !hasJSONContentType(r) {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	if err := validateDownloadURL(req.URL, s.config.AllowedHosts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	jobConfig := s.config
	jobConfig.PlaylistURL = req.URL

	job, err := s.jobMgr.CreateJob(s.ctx, req.URL, jobConfig)
	if err != nil {
		s.logger.Error("failed to create job: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	s.logger.Info("Created job %s for URL: %s", job.ID, req.URL)

	s.wg.Add(1)
	go s.processJob(job)

	s.writeJSON(w, s.jobToResponse(job))
}

func validateDownloadURL(raw string, allowedHosts []string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must start with http:// or https://")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no host")
	}
	if !hostAllowed(u.Hostname(), allowedHosts) {
		return fmt.Errorf("host %q is not in allowed_hosts", u.Hostname())
	}
	return nil
}

func hostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}

	jobs := s.jobMgr.ListJobs(limit)
	responses := make([]*JobResponse, len(jobs))
	for i, job := range jobs {
		responses[i] = s.jobToResponse(job)
	}

	s.writeJSON(w, responses)
}

func (s *Server) handleJobAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	jobID := parts[0]

	if r.Method == http.MethodGet && len(parts) == 1 {
		job, err := s.jobMgr.GetJob(jobID)
		if err != nil {
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}

		s.writeJSON(w, s.jobToResponse(job))
		return
	}

	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "log" {
		lines, dropped, err := s.jobMgr.LogSince(jobID, 0)
		if err != nil {
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}

		s.writeJSON(w, LogResponse{Lines: lines, Dropped: dropped})
		return
	}

	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "cancel" {
		job, err := s.jobMgr.GetJob(jobID)
		if err != nil {
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}

		// The job writes its own final state: a run that finishes moving its files despite the cancel is completed.
		job.Cancel()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		s.writeJSON(w, map[string]string{"status": "cancelling"})
		return
	}

	http.Error(w, "Invalid request", http.StatusBadRequest)
}

func (s *Server) processJob(job Job) {
	defer s.wg.Done()

	jobLog := s.logger.WithPrefix(job.ID).WithSink(func(l logger.Line) {
		s.jobMgr.AppendLog(job.ID, l)
	})

	defer func() {
		if r := recover(); r != nil {
			jobLog.Error("panic: %v\n%s", r, debug.Stack())
			s.updateJob(job.ID, func(j *Job) {
				j.Status = StatusFailed
				j.Error = fmt.Sprintf("internal error: %v", r)
			})
		}
	}()

	defer job.Cancel()

	select {
	case s.jobSem <- struct{}{}:
		defer func() { <-s.jobSem }()
	case <-job.ctx.Done():
		jobLog.Info("cancelled while queued")
		s.updateJob(job.ID, func(j *Job) {
			j.Status = StatusCancelled
		})
		return
	}

	s.updateJob(job.ID, func(j *Job) {
		j.Status = StatusRunning
	})

	jobLog.Info("starting")

	tempDir, err := utils.CreateTempDir()
	if err != nil {
		jobLog.Error("failed to create temp dir: %v", err)
		s.updateJob(job.ID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = err.Error()
		})
		return
	}
	defer func() {
		if err := utils.Cleanup(tempDir); err != nil {
			jobLog.Warn("Error during cleanup: %v", err)
		}
	}()

	hooks := pipeline.Hooks{
		OnURLsExtracted: func(total int) {
			s.updateJob(job.ID, func(j *Job) {
				j.Total = total
			})
		},
		OnProgress: func() {
			s.updateJob(job.ID, func(j *Job) {
				j.Progress++
			})
		},
		OnWarning: func(msg string) {
			s.updateJob(job.ID, func(j *Job) {
				j.Warnings = append(j.Warnings, msg)
			})
		},
	}

	if err := s.runPipeline(job.ctx, job.Config, jobLog, tempDir, hooks); err != nil {
		if errors.Is(err, context.Canceled) {
			if s.ctx.Err() != nil {
				jobLog.Info("cancelled by server shutdown")
			} else {
				jobLog.Info("cancelled by user")
			}
			s.updateJob(job.ID, func(j *Job) {
				j.Status = StatusCancelled
			})
			return
		}

		jobLog.Error("job failed: %v", err)
		s.updateJob(job.ID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = err.Error()
		})
		return
	}

	// Before the final state: the WebSocket closes on it, and a later line would reach only the log endpoint.
	if done, err := s.jobMgr.GetJob(job.ID); err == nil && len(done.Warnings) > 0 {
		jobLog.Info("completed with %d warnings", len(done.Warnings))
	} else {
		jobLog.Info("completed successfully")
	}
	s.updateJob(job.ID, func(j *Job) {
		j.Status = StatusCompleted
	})
}

// Cleanup never removes a running job, so a failure here is a broken invariant.
func (s *Server) updateJob(id string, fn func(*Job)) {
	if err := s.jobMgr.UpdateJob(id, fn); err != nil {
		s.logger.Warn("updating job %s: %v", id, err)
	}
}

func (s *Server) jobToResponse(job Job) *JobResponse {
	resp := &JobResponse{
		ID:        job.ID,
		URL:       job.URL,
		Status:    job.Status,
		Progress:  job.Progress,
		Total:     job.Total,
		Error:     job.Error,
		Warnings:  job.Warnings,
		CreatedAt: job.CreatedAt.Format(time.DateTime),
	}

	if job.StartedAt != nil {
		started := job.StartedAt.Format(time.DateTime)
		resp.StartedAt = &started
	}

	if job.CompletedAt != nil {
		completed := job.CompletedAt.Format(time.DateTime)
		resp.CompletedAt = &completed
	}

	return resp
}
