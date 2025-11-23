package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for simplicity
	},
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		s.logger.Error("WebSocket connection missing job_id")
		return
	}

	// Subscribe to job updates
	updates := s.jobMgr.Subscribe(jobID)
	defer s.jobMgr.Unsubscribe(jobID, updates)

	// Send initial job state
	job, err := s.jobMgr.GetJob(jobID)
	if err == nil {
		data, _ := json.Marshal(s.jobToResponse(job))
		conn.WriteMessage(websocket.TextMessage, data)
	}

	// Listen for updates and send to client
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case job, ok := <-updates:
			if !ok {
				return
			}

			data, err := json.Marshal(s.jobToResponse(job))
			if err != nil {
				s.logger.Error("Failed to marshal job: %v", err)
				continue
			}

			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				s.logger.Error("Failed to write WebSocket message: %v", err)
				return
			}

			// Close connection if job is done
			if job.Status == StatusCompleted || job.Status == StatusFailed || job.Status == StatusCancelled {
				return
			}

		case <-ticker.C:
			// Send ping to keep connection alive
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
