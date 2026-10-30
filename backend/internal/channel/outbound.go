package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"time"

	"konakpro/internal/db"
	"konakpro/internal/platform/store"
)

type Observer interface {
	OutboxDelivered()
	OutboxRetried()
	OutboxDead()
}

type Worker struct {
	Store       *store.Store
	Client      *http.Client
	Log         *slog.Logger
	BaseURL     string
	Interval    time.Duration
	Batch       int32
	MaxAttempts int32
	Backoff     time.Duration
	MaxBackoff  time.Duration
	Observer    Observer
}

type ARI struct {
	Channel   string `json:"channel"`
	RoomType  string `json:"room_type"`
	Date      string `json:"date"`
	Available int32  `json:"available"`
	OutboxID  int64  `json:"outbox_id"`
}

func Delay(base, limit time.Duration, attempts int32, id int64) time.Duration {
	delay := base
	for i := int32(1); i < attempts && delay < limit; i++ {
		delay *= 2
	}
	if delay > limit {
		delay = limit
	}
	hash := fnv.New32a()
	fmt.Fprintf(hash, "%d:%d", id, attempts)
	jitter := time.Duration(hash.Sum32()%1000) * delay / 4000
	return delay + jitter
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-purge.C:
			if err := w.Store.PurgeSentOutbox(ctx); err != nil {
				w.Log.Error("kuyruk temizliği başarısız", "error", err)
			}
		case <-ticker.C:
			for {
				processed, err := w.Tick(ctx)
				if err != nil {
					w.Log.Error("kuyruk işleme hatası", "error", err)
				}
				if err != nil || processed < int(w.Batch) {
					break
				}
			}
		}
	}
}

func (w *Worker) Tick(ctx context.Context) (int, error) {
	rows, err := w.Store.ClaimOutbox(ctx, w.Batch)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if err := w.deliver(ctx, row); err != nil {
			return len(rows), err
		}
	}
	return len(rows), nil
}

func (w *Worker) deliver(ctx context.Context, row db.ChannelOutbox) error {
	sendErr := w.send(ctx, row)
	if sendErr == nil {
		if w.Observer != nil {
			w.Observer.OutboxDelivered()
		}
		return w.Store.MarkOutboxSent(ctx, row.ID)
	}
	message := sendErr.Error()
	if row.Attempts >= w.MaxAttempts {
		if w.Observer != nil {
			w.Observer.OutboxDead()
		}
		w.Log.Warn("mesaj ölü kuyruğa alındı", "outbox_id", row.ID, "attempts", row.Attempts, "error", message)
		return w.Store.MarkOutboxDead(ctx, db.MarkOutboxDeadParams{ID: row.ID, LastError: &message})
	}
	if w.Observer != nil {
		w.Observer.OutboxRetried()
	}
	return w.Store.MarkOutboxRetry(ctx, db.MarkOutboxRetryParams{
		ID: row.ID, NextAttemptAt: time.Now().Add(Delay(w.Backoff, w.MaxBackoff, row.Attempts, row.ID)), LastError: &message,
	})
}

func (w *Worker) send(ctx context.Context, row db.ChannelOutbox) error {
	payload, err := w.Store.OutboxPayload(ctx, db.OutboxPayloadParams{ID: row.ChannelID, RoomTypeID: row.RoomTypeID, StayDate: row.StayDate})
	if err != nil {
		return err
	}
	available := payload.Capacity + payload.OverbookLimit - payload.Sold
	if available < 0 {
		available = 0
	}
	body, err := json.Marshal(ARI{
		Channel: payload.ChannelCode, RoomType: payload.TypeCode, Date: row.StayDate.Format(time.DateOnly),
		Available: available, OutboxID: row.ID,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.BaseURL+"/ari", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := w.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("kanal yanıtı %d", response.StatusCode)
	}
	return nil
}
