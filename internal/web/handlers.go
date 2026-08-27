package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	CreatedAt   string    `json:"created_at"`
	StartedAt   *string   `json:"started_at,omitempty"`
	CompletedAt *string   `json:"completed_at,omitempty"`
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

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.jobToResponse(job))
}

// validateDownloadURL checks the URL is an http(s) address whose host is
// allowed. yt-dlp supports well over a thousand sites, so without an allowlist
// the server is a general-purpose downloader for anyone who can reach it.
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

// hostAllowed matches on label boundaries, so "youtube.com" accepts
// "www.youtube.com" but not "notyoutube.com" or "youtube.com.evil.test".
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responses)
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

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.jobToResponse(job))
		return
	}

	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "cancel" {
		job, err := s.jobMgr.GetJob(jobID)
		if err != nil {
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}

		if job.Cancel != nil {
			job.Cancel()
		}

		s.jobMgr.UpdateJob(jobID, func(j *Job) {
			j.Status = StatusCancelled
		})

		writeJSON(w, map[string]string{"status": "cancelled"})
		return
	}

	http.Error(w, "Invalid request", http.StatusBadRequest)
}

func (s *Server) processJob(job Job) {
	defer s.wg.Done()

	jobLog := s.logger.WithPrefix(job.ID)

	defer func() {
		if r := recover(); r != nil {
			jobLog.Error("panic: %v", r)
			s.jobMgr.UpdateJob(job.ID, func(j *Job) {
				j.Status = StatusFailed
				j.Error = fmt.Sprintf("internal error: %v", r)
			})
		}
	}()

	// The job's context was created with the job; releasing it here keeps the
	// parent from accumulating cancel functions for finished work.
	defer job.Cancel()

	s.jobMgr.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusRunning
	})

	jobLog.Info("starting")

	tempDir, err := utils.CreateTempDir()
	if err != nil {
		jobLog.Error("failed to create temp dir: %v", err)
		s.jobMgr.UpdateJob(job.ID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = err.Error()
		})
		return
	}
	defer utils.Cleanup(tempDir)

	var warningMsg string
	hooks := pipeline.Hooks{
		OnURLsExtracted: func(total int) {
			s.jobMgr.UpdateJob(job.ID, func(j *Job) {
				j.Total = total
			})
		},
		OnProgress: func() {
			s.jobMgr.UpdateJob(job.ID, func(j *Job) {
				j.Progress++
			})
		},
		OnWarning: func(msg string) {
			warningMsg = msg
		},
	}

	if err := pipeline.Run(job.ctx, job.Config, jobLog, tempDir, hooks); err != nil {
		jobLog.Error("job failed: %v", err)
		s.jobMgr.UpdateJob(job.ID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = err.Error()
		})
		return
	}

	s.jobMgr.UpdateJob(job.ID, func(j *Job) {
		j.Status = StatusCompleted
		if warningMsg != "" {
			j.Error = warningMsg
		}
	})

	if warningMsg != "" {
		jobLog.Info("completed with warnings: %s", warningMsg)
	} else {
		jobLog.Info("completed successfully")
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
		CreatedAt: job.CreatedAt.Format("2006-01-02 15:04:05"),
	}

	if job.StartedAt != nil {
		started := job.StartedAt.Format("2006-01-02 15:04:05")
		resp.StartedAt = &started
	}

	if job.CompletedAt != nil {
		completed := job.CompletedAt.Format("2006-01-02 15:04:05")
		resp.CompletedAt = &completed
	}

	return resp
}
