package web

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"ytmusic/internal/logger"
)

func dialJob(t *testing.T, s *Server, id string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)

	header := http.Header{}
	header.Set("Cookie", sessionCookie(t, s).String())
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws?job_id="+id, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readUpdate(t *testing.T, conn *websocket.Conn) wsMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg wsMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("reading an update: %v", err)
	}
	return msg
}

func expectClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	var netErr net.Error
	switch {
	case err == nil:
		t.Errorf("got %s, want the socket closed", data)
	case errors.As(err, &netErr) && netErr.Timeout():
		t.Errorf("socket still open after 5 s")
	}
}

func seqs(lines []LogLine) []int {
	out := make([]int, len(lines))
	for i, l := range lines {
		out[i] = l.Seq
	}
	return out
}

func TestWebSocketSendsTheWholeLogThenOnlyNewLines(t *testing.T) {
	s := newTestServer(t, nil)
	job, err := s.jobMgr.CreateJob(s.ctx, "https://youtube.com/x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	s.jobMgr.AppendLog(job.ID, logger.Line{Time: time.Now(), Level: "INFO", Text: "zero"})
	s.jobMgr.AppendLog(job.ID, logger.Line{Time: time.Now(), Level: "INFO", Text: "one"})

	conn := dialJob(t, s, job.ID)

	first := readUpdate(t, conn)
	if first.Job.Status != StatusPending || len(first.Log) != 2 || first.Log[0].Seq != 0 || first.Log[1].Seq != 1 {
		t.Fatalf("first update = %+v %v, want pending with lines 0 and 1", first.Job, seqs(first.Log))
	}

	s.jobMgr.AppendLog(job.ID, logger.Line{Time: time.Now(), Level: "INFO", Text: "two"})
	if next := readUpdate(t, conn); len(next.Log) != 1 || next.Log[0].Seq != 2 {
		t.Fatalf("next update has lines %v, want only 2", seqs(next.Log))
	}

	s.updateJob(job.ID, func(j *Job) { j.Status = StatusCompleted })
	if last := readUpdate(t, conn); last.Job.Status != StatusCompleted {
		t.Fatalf("last update = %q, want completed", last.Job.Status)
	}
	expectClosed(t, conn)
}

func TestWebSocketSendsAFinishedJobAndCloses(t *testing.T) {
	s := newTestServer(t, nil)
	job, err := s.jobMgr.CreateJob(s.ctx, "https://youtube.com/x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	s.updateJob(job.ID, func(j *Job) {
		j.Status = StatusFailed
		j.Error = "boom"
	})

	conn := dialJob(t, s, job.ID)

	if msg := readUpdate(t, conn); msg.Job.Status != StatusFailed || msg.Job.Error != "boom" {
		t.Errorf("update = %+v, want failed with its error", msg.Job)
	}
	expectClosed(t, conn)
}

func TestWebSocketClosesOnAnUnknownJob(t *testing.T) {
	s := newTestServer(t, nil)
	expectClosed(t, dialJob(t, s, "job_unknown"))
}

func TestSocketDropsAClientThatStopsAnswering(t *testing.T) {
	s := newTestServer(t, nil)
	s.pongWait = 50 * time.Millisecond
	job, err := s.jobMgr.CreateJob(t.Context(), "https://youtube.com/x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	dialJob(t, s, job.ID) // never reads, so it never answers a ping

	listening := func() bool {
		s.jobMgr.mu.RLock()
		defer s.jobMgr.mu.RUnlock()
		return len(s.jobMgr.listeners[job.ID]) > 0
	}
	waitFor(t, listening, "the socket to open")
	waitFor(t, func() bool { return !listening() }, "the socket of a silent client to close")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSocketRefusesAnOversizedFrame(t *testing.T) {
	s := newTestServer(t, nil)
	job, err := s.jobMgr.CreateJob(t.Context(), "https://youtube.com/x", s.config)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	conn := dialJob(t, s, job.ID)

	if err := conn.WriteMessage(websocket.TextMessage, make([]byte, 1<<20)); err != nil {
		return // the server hung up before the frame was through
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
			t.Errorf("read: %v, want the close for a frame too big", err)
		}
		return
	}
}
