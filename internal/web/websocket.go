package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkWSOrigin,
}

// checkWSOrigin rejects handshakes coming from another site. A missing Origin
// means a non-browser client: browsers always send it on cross-origin requests.
func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// wsReader is the slice of a WebSocket connection the read pump needs.
type wsReader interface {
	ReadMessage() (messageType int, p []byte, err error)
}

// readPump drains client frames (close, ping, pong) and closes done when the
// client goes away. It recovers from panics because it runs in its own
// goroutine, off any handler stack: net/http would not catch one here, so a
// panic would take down the server and every running job with it.
func (s *Server) readPump(conn wsReader, done chan<- struct{}) {
	defer close(done)
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("websocket read pump panic: %v", r)
		}
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("WebSocket upgrade failed: %v", err)
		return
	}
	// The handler is done with the connection either way, and a close error
	// on a dead client has no one to be reported to.
	defer func() { _ = conn.Close() }()

	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		s.logger.Error("WebSocket connection missing job_id")
		return
	}

	updates := s.jobMgr.Subscribe(jobID)
	defer s.jobMgr.Unsubscribe(jobID, updates)

	clientGone := make(chan struct{})
	go s.readPump(conn, clientGone)

	// Send initial job state
	job, err := s.jobMgr.GetJob(jobID)
	if err == nil {
		data, err := json.Marshal(s.jobToResponse(job))
		if err != nil {
			s.logger.Error("failed to marshal initial job response: %v", err)
		} else if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			// The client is already gone: waiting on a dead connection would
			// hold the subscription open until the job ends.
			return
		}
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
