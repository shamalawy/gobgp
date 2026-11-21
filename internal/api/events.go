package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

const (
	logBacklog   = 200 // FSM events replayed to a new client
	clientBuffer = 256 // messages queued per client before it is dropped
)

// hub fans events out to the connected SSE clients.
type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
	log  [][]byte // recent "log" events, oldest first
}

func newHub() *hub { return &hub{subs: map[chan []byte]struct{}{}} }

func (h *hub) clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// frame encodes one server-sent event.
func frame(event string, data any) []byte {
	payload, _ := json.Marshal(data) // the event types are plain data
	return fmt.Appendf(nil, "event: %s\ndata: %s\n\n", event, payload)
}

// publish sends an event to every client. A client that does not keep up is
// disconnected instead of blocking the publisher; its browser reconnects.
func (h *hub) publish(event string, data any) {
	msg := frame(event, data)
	h.mu.Lock()
	defer h.mu.Unlock()
	if event == "log" {
		if len(h.log) == logBacklog {
			h.log = h.log[1:]
		}
		h.log = append(h.log, msg)
	}
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

func (h *hub) subscribe() (chan []byte, [][]byte) {
	ch := make(chan []byte, clientBuffer)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[ch] = struct{}{}
	return ch, append([][]byte(nil), h.log...)
}

func (h *hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
}

// events is the server-sent event stream. A new client first gets the current
// status, the injection progress and the recent FSM log, then live updates:
// "status", "log" (bgp.Event) and "inject" (bgp.InjectStatus).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)

	ch, backlog := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	if st, err := s.status(r.Context()); err == nil {
		w.Write(frame("status", st))
	}
	w.Write(frame("inject", s.engine.InjectStatus()))
	for _, msg := range backlog {
		w.Write(msg)
	}
	rc.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			rc.Flush()
		}
	}
}
