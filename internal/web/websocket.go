package web

import (
	"encoding/json"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     originMatchesHost,
}

type wsMessage struct {
	Job        *JobResponse `json:"job"`
	Log        []LogLine    `json:"log"`
	LogDropped int          `json:"log_dropped"`
}

type wsReader interface {
	ReadMessage() (messageType int, p []byte, err error)
}

// Drains client frames (close, ping, pong); done closes when the client goes away.
func (s *Server) readPump(conn wsReader, done chan<- struct{}) {
	defer close(done)
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("websocket read pump panic: %v\n%s", r, debug.Stack())
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
	// A close error on a dead client has no one to be reported to.
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

	sender := &jobSender{s: s, conn: conn, jobID: jobID}
	if sender.send() {
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-clientGone:
			return

		case _, ok := <-updates:
			if !ok || sender.send() {
				return
			}

		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Sends a job's current state with the log lines the client has not had yet.
type jobSender struct {
	s      *Server
	conn   *websocket.Conn
	jobID  string
	cursor int
}

// done: the job is over or gone, or the client cannot be written to.
func (js *jobSender) send() (done bool) {
	job, err := js.s.jobMgr.GetJob(js.jobID)
	if err != nil {
		return true
	}
	lines, dropped, err := js.s.jobMgr.LogSince(js.jobID, js.cursor)
	if err != nil {
		return true
	}
	if n := len(lines); n > 0 {
		js.cursor = lines[n-1].Seq + 1
	}

	data, err := json.Marshal(wsMessage{Job: js.s.jobToResponse(job), Log: lines, LogDropped: dropped})
	if err != nil {
		js.s.logger.Error("marshalling the update of job %s: %v", js.jobID, err)
		return true
	}
	if err := js.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return true
	}
	return job.Status.finished()
}
