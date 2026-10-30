package booking

import (
	"context"
	"net/http"
	"time"

	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

func (s *Service) CheckIn(ctx context.Context, actor auditlog.Actor, id int64, roomID *int64) error {
	return s.Store.InTx(ctx, func(q *db.Queries) error {
		r, err := q.LockReservation(ctx, id)
		if err != nil {
			return notFound(err, "Rezervasyon bulunamadı")
		}
		if r.Status != db.ReservationStatusConfirmed {
			return apperr.Conflict("INVALID_STATE", "Yalnızca onaylı rezervasyona giriş yapılabilir")
		}
		state, err := q.GetHotelState(ctx)
		if err != nil {
			return err
		}
		if !r.CheckIn.Equal(state.BusinessDate) {
			return apperr.Conflict("NOT_ARRIVAL_DAY", "Giriş yalnızca rezervasyonun giriş gününde yapılabilir")
		}
		room, err := s.resolveRoom(ctx, q, r, roomID)
		if err != nil {
			return err
		}
		locked, err := q.LockRoom(ctx, room)
		if err != nil {
			return notFound(err, "Oda bulunamadı")
		}
		if locked.HkStatus != db.HkStatusClean && locked.HkStatus != db.HkStatusInspected {
			return apperr.Conflict("ROOM_NOT_READY", "Oda hazır değil: "+locked.Number)
		}
		if err := q.LockReservationAssignments(ctx, id); err != nil {
			return err
		}
		if err := q.UpdateReservationStatus(ctx, db.UpdateReservationStatusParams{ID: id, Status: db.ReservationStatusCheckedIn}); err != nil {
			return err
		}
		return auditlog.Write(ctx, q, actor, "reservation.checked_in", "reservation", id,
			map[string]string{"status": "confirmed"}, map[string]any{"status": "checked_in", "room": locked.Number})
	})
}

func (s *Service) resolveRoom(ctx context.Context, q *db.Queries, r db.Reservation, requested *int64) (int64, error) {
	current, err := q.ListAssignmentsForReservation(ctx, r.ID)
	if err != nil {
		return 0, err
	}
	if requested != nil {
		room, err := q.GetRoom(ctx, *requested)
		if err != nil {
			return 0, notFound(err, "Oda bulunamadı")
		}
		if room.RoomTypeID != r.RoomTypeID {
			return 0, apperr.New(http.StatusUnprocessableEntity, "ROOM_TYPE_MISMATCH", "Oda tipi rezervasyonla uyuşmuyor")
		}
		if len(current) > 0 && current[0].RoomID == room.ID {
			return room.ID, nil
		}
		if err := q.DeleteAssignments(ctx, r.ID); err != nil {
			return 0, err
		}
		return room.ID, assignWhole(ctx, q, r, room.ID)
	}
	if len(current) > 0 {
		return current[0].RoomID, nil
	}
	room, err := q.FindReadyRoom(ctx, db.FindReadyRoomParams{RoomTypeID: r.RoomTypeID, DateFrom: r.CheckIn, DateTo: r.CheckOut})
	if store.IsNotFound(err) {
		return 0, apperr.Conflict("NO_ROOM_AVAILABLE", "Bu konaklama için boş ve hazır oda yok, misafir yönlendirilmeli")
	}
	if err != nil {
		return 0, err
	}
	return room, assignWhole(ctx, q, r, room)
}

func assignWhole(ctx context.Context, q *db.Queries, r db.Reservation, room int64) error {
	err := q.InsertAssignment(ctx, db.InsertAssignmentParams{ReservationID: r.ID, RoomID: room, DateFrom: r.CheckIn, DateTo: r.CheckOut})
	if store.IsExclusion(err) {
		return apperr.Conflict("ROOM_TAKEN", "Oda bu tarihlerde başka bir rezervasyona ayrılmış")
	}
	return err
}

func (s *Service) CheckOut(ctx context.Context, actor auditlog.Actor, id int64) error {
	return s.Store.InTx(ctx, func(q *db.Queries) error {
		r, err := q.LockReservation(ctx, id)
		if err != nil {
			return notFound(err, "Rezervasyon bulunamadı")
		}
		if r.Status != db.ReservationStatusCheckedIn {
			return apperr.Conflict("INVALID_STATE", "Yalnızca konaklayan misafirin çıkışı yapılabilir")
		}
		state, err := q.GetHotelState(ctx)
		if err != nil {
			return err
		}
		if state.BusinessDate.Before(r.CheckOut) {
			if err := shorten(ctx, q, r, state.BusinessDate); err != nil {
				return err
			}
		}
		open, err := q.OpenFolioBalance(ctx, id)
		if err != nil {
			return err
		}
		if open != 0 {
			return apperr.Conflict("FOLIO_NOT_SETTLED", "Çıkış için tüm folyoların bakiyesi sıfır olmalı")
		}
		segments, err := q.ListAssignmentsForReservation(ctx, id)
		if err != nil {
			return err
		}
		if len(segments) > 0 {
			last := segments[len(segments)-1]
			if _, err := q.SetRoomStatus(ctx, db.SetRoomStatusParams{ID: last.RoomID, HkStatus: db.HkStatusDirty}); err != nil {
				return err
			}
			if _, err := q.UpsertTask(ctx, db.UpsertTaskParams{
				RoomID: last.RoomID, TaskType: "cikis_temizligi", BusinessDate: state.BusinessDate, Note: "Çıkış: " + r.Code,
			}); err != nil {
				return err
			}
		}
		if err := q.CloseFolios(ctx, id); err != nil {
			return err
		}
		if err := q.UpdateReservationStatus(ctx, db.UpdateReservationStatusParams{ID: id, Status: db.ReservationStatusCheckedOut}); err != nil {
			return err
		}
		return auditlog.Write(ctx, q, actor, "reservation.checked_out", "reservation", id,
			map[string]string{"status": "checked_in"}, map[string]string{"status": "checked_out"})
	})
}

func shorten(ctx context.Context, q *db.Queries, r db.Reservation, cut time.Time) error {
	if !cut.After(r.CheckIn) {
		return apperr.Conflict("SAME_DAY_CHECKOUT", "Giriş günü çıkış yapılamaz, önce konaklama iptal edilmeli")
	}
	if err := ReleaseNights(ctx, q, r.RoomTypeID, cut, r.CheckOut); err != nil {
		return err
	}
	if err := q.DeleteReservationNightsFrom(ctx, db.DeleteReservationNightsFromParams{ReservationID: r.ID, StayDate: cut}); err != nil {
		return err
	}
	if err := q.TruncateAssignmentAt(ctx, db.TruncateAssignmentAtParams{ReservationID: r.ID, Cut: cut}); err != nil {
		return err
	}
	if err := q.DeleteAssignmentsFrom(ctx, db.DeleteAssignmentsFromParams{ReservationID: r.ID, Cut: cut}); err != nil {
		return err
	}
	return q.ShortenReservation(ctx, db.ShortenReservationParams{ID: r.ID, CheckOut: cut})
}
