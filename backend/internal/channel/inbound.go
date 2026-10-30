package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"konakpro/internal/auditlog"
	"konakpro/internal/booking"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

const (
	OutcomeApplied  = "applied"
	OutcomeStale    = "stale"
	OutcomeConflict = "conflict"
	OutcomeRejected = "rejected"
	OutcomeDupe     = "duplicate"
)

type Event struct {
	EventID     string `json:"event_id"`
	ExternalRef string `json:"external_ref"`
	Seq         int64  `json:"seq"`
	Type        string `json:"type"`
	RoomType    string `json:"room_type"`
	RatePlan    string `json:"rate_plan"`
	CheckIn     string `json:"check_in"`
	CheckOut    string `json:"check_out"`
	Adults      int    `json:"adults"`
	GuestName   string `json:"guest_name"`
	NetKurus    int64  `json:"net_kurus"`
}

type Result struct {
	Outcome       string `json:"outcome"`
	Detail        string `json:"detail,omitempty"`
	ReservationID *int64 `json:"reservation_id,omitempty"`
}

type Service struct {
	Store *store.Store
}

func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) Ingest(ctx context.Context, code, signature string, body []byte) (Result, error) {
	channel, err := s.Store.GetChannelByCode(ctx, code)
	if store.IsNotFound(err) {
		return Result{}, apperr.NotFound("Kanal bulunamadı")
	}
	if err != nil {
		return Result{}, err
	}
	if !hmac.Equal([]byte(Sign(channel.Secret, body)), []byte(signature)) {
		return Result{}, apperr.Unauthorized("Kanal imzası geçersiz")
	}
	var event Event
	if err := json.Unmarshal(body, &event); err != nil {
		return Result{}, apperr.Invalid("Olay gövdesi okunamadı")
	}
	if err := event.validate(); err != nil {
		return Result{}, err
	}
	var result Result
	err = s.Store.InTx(ctx, func(q *db.Queries) error {
		var err error
		result, err = process(ctx, q, channel, event, body)
		return err
	})
	return result, err
}

func (e Event) validate() error {
	if e.EventID == "" || e.ExternalRef == "" || e.Seq <= 0 {
		return apperr.Invalid("event_id, external_ref ve seq zorunlu")
	}
	switch e.Type {
	case "cancel":
		return nil
	case "create", "modify":
		if e.Adults < 1 || e.NetKurus < 0 || e.RoomType == "" || e.GuestName == "" {
			return apperr.Invalid("Rezervasyon alanları eksik")
		}
		if _, _, err := e.stay(); err != nil {
			return err
		}
		return nil
	}
	return apperr.Invalid("Bilinmeyen olay tipi")
}

func (e Event) stay() (time.Time, time.Time, error) {
	from, err := time.Parse(time.DateOnly, e.CheckIn)
	if err != nil {
		return time.Time{}, time.Time{}, apperr.Invalid("check_in geçersiz")
	}
	to, err := time.Parse(time.DateOnly, e.CheckOut)
	if err != nil {
		return time.Time{}, time.Time{}, apperr.Invalid("check_out geçersiz")
	}
	return from, to, nil
}

func process(ctx context.Context, q *db.Queries, channel db.Channel, e Event, body []byte) (Result, error) {
	inboxID, err := q.InsertInbox(ctx, db.InsertInboxParams{
		ChannelID: channel.ID, EventID: e.EventID, ExternalRef: e.ExternalRef, Seq: e.Seq, EventType: e.Type, Payload: body,
	})
	if store.IsNotFound(err) {
		prior, err := q.GetInboxOutcome(ctx, db.GetInboxOutcomeParams{ChannelID: channel.ID, EventID: e.EventID})
		if err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeDupe, Detail: prior.Outcome}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if err := q.EnsureChannelBooking(ctx, db.EnsureChannelBookingParams{ChannelID: channel.ID, ExternalRef: e.ExternalRef}); err != nil {
		return Result{}, err
	}
	bookingRow, err := q.LockChannelBooking(ctx, db.LockChannelBookingParams{ChannelID: channel.ID, ExternalRef: e.ExternalRef})
	if err != nil {
		return Result{}, err
	}
	result, next, err := apply(ctx, q, channel, e, bookingRow)
	if err != nil {
		return Result{}, err
	}
	if result.Outcome != OutcomeStale {
		if err := q.UpdateChannelBooking(ctx, db.UpdateChannelBookingParams{
			ChannelID: channel.ID, ExternalRef: e.ExternalRef, LastSeq: e.Seq, State: next.State, ReservationID: next.ReservationID,
		}); err != nil {
			return Result{}, err
		}
	}
	result.ReservationID = next.ReservationID
	if err := q.SetInboxOutcome(ctx, db.SetInboxOutcomeParams{ID: inboxID, Outcome: result.Outcome, Detail: result.Detail}); err != nil {
		return Result{}, err
	}
	return result, auditlog.WriteRef(ctx, q, auditlog.System("kanal:"+channel.Code), "channel.event_"+result.Outcome, "channel_booking", e.ExternalRef, nil,
		map[string]any{"seq": e.Seq, "type": e.Type})
}

func apply(ctx context.Context, q *db.Queries, channel db.Channel, e Event, row db.ChannelBooking) (Result, db.ChannelBooking, error) {
	actor := auditlog.System("kanal:" + channel.Code)
	if e.Seq <= row.LastSeq {
		return Result{Outcome: OutcomeStale, Detail: fmt.Sprintf("son sıra %d", row.LastSeq)}, row, nil
	}
	if e.Type == "cancel" {
		row.State = "cancelled"
		if row.ReservationID == nil {
			return Result{Outcome: OutcomeApplied, Detail: "rezervasyon gelmeden iptal işaretlendi"}, row, nil
		}
		if err := booking.CancelTx(ctx, q, actor, *row.ReservationID); err != nil {
			return rejection(err, row)
		}
		return Result{Outcome: OutcomeApplied}, row, nil
	}
	if row.State == "cancelled" {
		return Result{Outcome: OutcomeStale, Detail: "iptal edilmiş rezervasyon"}, row, nil
	}
	from, to, err := e.stay()
	if err != nil {
		return Result{}, row, err
	}
	roomType, plan, err := lookup(ctx, q, e)
	if err != nil {
		return rejection(err, row)
	}
	if row.ReservationID != nil {
		net := e.NetKurus
		_, err := booking.ModifyTx(ctx, q, actor, *row.ReservationID, booking.ModifyRequest{
			RoomTypeID: roomType, CheckIn: from, CheckOut: to, Adults: e.Adults, OTANetKurus: &net,
		})
		if err != nil {
			return outcomeFor(err, row)
		}
		row.State = "active"
		return Result{Outcome: OutcomeApplied, Detail: "değişiklik işlendi"}, row, nil
	}
	net, ref, channelID := e.NetKurus, e.ExternalRef, channel.ID
	reservation, err := booking.CreateTx(ctx, q, actor, booking.CreateRequest{
		Guest: &booking.NewGuest{FullName: e.GuestName}, RoomTypeID: roomType, RatePlanID: plan,
		CheckIn: from, CheckOut: to, Adults: e.Adults, ChannelID: &channelID, ExternalRef: &ref, OTANetKurus: &net,
	})
	if err != nil {
		return outcomeFor(err, row)
	}
	id := reservation.ID
	row.State, row.ReservationID = "active", &id
	return Result{Outcome: OutcomeApplied}, row, nil
}

func lookup(ctx context.Context, q *db.Queries, e Event) (int64, int64, error) {
	types, err := q.ListRoomTypes(ctx)
	if err != nil {
		return 0, 0, err
	}
	var roomType int64
	for _, t := range types {
		if t.Code == e.RoomType {
			roomType = t.ID
		}
	}
	if roomType == 0 {
		return 0, 0, apperr.Invalid("Bilinmeyen oda tipi: " + e.RoomType)
	}
	code := e.RatePlan
	if code == "" {
		code = "BAR"
	}
	plan, err := q.GetRatePlanByCode(ctx, code)
	if err != nil {
		return 0, 0, apperr.Invalid("Bilinmeyen tarife: " + code)
	}
	return roomType, plan.ID, nil
}

func outcomeFor(err error, row db.ChannelBooking) (Result, db.ChannelBooking, error) {
	var app *apperr.Error
	if errors.As(err, &app) && app.Code == "SOLD_OUT" {
		row.State = "conflict"
		return Result{Outcome: OutcomeConflict, Detail: app.Message}, row, nil
	}
	return rejection(err, row)
}

func rejection(err error, row db.ChannelBooking) (Result, db.ChannelBooking, error) {
	var app *apperr.Error
	if errors.As(err, &app) && app.Status != http.StatusInternalServerError {
		return Result{Outcome: OutcomeRejected, Detail: app.Message}, row, nil
	}
	return Result{}, row, err
}
