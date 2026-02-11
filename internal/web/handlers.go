package web

import (
	"context"
	"encoding/json"
	"net/http"
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

	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		http.Error(w, "URL must start with http:// or https://", http.StatusBadRequest)
		return
	}

	jobConfig := s.config
	jobConfig.PlaylistURL = req.URL

	job := s.jobMgr.CreateJob(req.URL, jobConfig)
	s.logger.Info("Created job %s for URL: %s", job.ID, req.URL)

	go s.processJob(job)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.jobToResponse(job))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobs := s.jobMgr.ListJobs()
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

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
		return
	}

	http.Error(w, "Invalid request", http.StatusBadRequest)
}

func (s *Server) processJob(job *Job) {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	s.jobMgr.UpdateJob(job.ID, func(j *Job) {
		j.Cancel = cancel
		j.Status = StatusRunning
	})

	s.logger.Info("Starting job %s", job.ID)

	tempDir, err := utils.CreateTempDir()
	if err != nil {
		s.logger.Error("Failed to create temp dir: %v", err)
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

	if err := pipeline.Run(ctx, job.Config, s.logger, tempDir, hooks); err != nil {
		s.logger.Error("Job %s failed: %v", job.ID, err)
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
		s.logger.Info("Job %s completed with warnings: %s", job.ID, warningMsg)
	} else {
		s.logger.Info("Job %s completed successfully", job.ID)
	}
}

func (s *Server) jobToResponse(job *Job) *JobResponse {
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
