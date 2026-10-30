package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type liveHub struct {
	url  string
	log  *slog.Logger
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newLiveHub(url string, log *slog.Logger) *liveHub {
	return &liveHub{url: url, log: log, subs: map[chan string]struct{}{}}
}

func (h *liveHub) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := h.listen(ctx); err != nil && ctx.Err() == nil {
			h.log.Warn("canlı olay bağlantısı koptu", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (h *liveHub) listen(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, h.url)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN live_events"); err != nil {
		return err
	}
	for {
		note, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.publish(note.Payload)
	}
}

func (h *liveHub) publish(payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub <- payload:
		default:
		}
	}
}

func (h *liveHub) subscribe() (chan string, func()) {
	sub := make(chan string, 32)
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub, func() {
		h.mu.Lock()
		delete(h.subs, sub)
		h.mu.Unlock()
	}
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("akış desteklenmiyor")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": bağlandı\n\n")
	flusher.Flush()

	sub, cancel := s.live.subscribe()
	defer cancel()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case payload := <-sub:
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
