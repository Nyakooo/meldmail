package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	changed, ok := s.authSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ch, unsubscribe, err := s.mail.Events.Subscribe()
	if err != nil {
		http.Error(w, "too many event connections", 429)
		return
	}
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(message string) error {
		if err := controller.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := fmt.Fprint(w, message); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err := send("retry: 2000\n: connected\n\n"); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-changed:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if err := send(fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data)); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := send(": heartbeat\n\n"); err != nil {
				return
			}
		}
	}
}
