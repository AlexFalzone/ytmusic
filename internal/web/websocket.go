package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
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

	updates := s.jobMgr.Subscribe(jobID)
	defer s.jobMgr.Unsubscribe(jobID, updates)

	// Read pump: processes close/ping/pong from client.
	// Signals via clientGone when the client disconnects.
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Send initial job state
	job, err := s.jobMgr.GetJob(jobID)
	if err == nil {
		data, _ := json.Marshal(s.jobToResponse(job))
		conn.WriteMessage(websocket.TextMessage, data)
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-clientGone:
			return

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
				return
			}

			if job.Status == StatusCompleted || job.Status == StatusFailed || job.Status == StatusCancelled {
				return
			}

		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
