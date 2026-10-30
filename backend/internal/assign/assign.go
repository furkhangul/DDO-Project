package assign

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"konakpro/internal/allocation"
	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

const advisoryKey = 7244002

type Service struct {
	Store *store.Store
	Seed  int64
}

type Plan struct {
	Result     allocation.Result `json:"result"`
	Baseline   int               `json:"baseline_moves"`
	Stays      int               `json:"stays"`
	Rooms      int               `json:"rooms"`
	Applied    bool              `json:"applied"`
	Moves      []PlanMove        `json:"moves"`
	Unassigned []int64           `json:"unassigned"`
}

type PlanMove struct {
	ReservationID int64  `json:"reservation_id"`
	RoomID        int64  `json:"room_id"`
	From          string `json:"from"`
	To            string `json:"to"`
}

func (s *Service) Solve(ctx context.Context, actor auditlog.Actor, from, to time.Time, apply bool) (Plan, error) {
	if !to.After(from) {
		return Plan{}, apperr.Invalid("Bitiş tarihi başlangıçtan sonra olmalı")
	}
	if to.Sub(from) > 120*24*time.Hour {
		return Plan{}, apperr.Invalid("Tahsis ufku en fazla 120 gün olabilir")
	}
	if !apply {
		return s.compute(ctx, s.Store.Queries, from, to)
	}
	var plan Plan
	err := pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", advisoryKey); err != nil {
			return err
		}
		q := s.Store.WithTx(tx)
		if _, err := q.DeleteUnlockedAssignments(ctx, db.DeleteUnlockedAssignmentsParams{DateFrom: from, DateTo: to}); err != nil {
			return err
		}
		var err error
		plan, err = s.compute(ctx, q, from, to)
		if err != nil {
			return err
		}
		pinned := map[int64]bool{}
		current, err := q.ListAssignmentsBetween(ctx, db.ListAssignmentsBetweenParams{DateFrom: from, DateTo: from.AddDate(2, 0, 0)})
		if err != nil {
			return err
		}
		for _, a := range current {
			pinned[a.ReservationID] = true
		}
		for _, segment := range plan.Result.Segments {
			if segment.StayID <= 0 || pinned[segment.StayID] {
				continue
			}
			err := q.InsertAssignment(ctx, db.InsertAssignmentParams{
				ReservationID: segment.StayID, RoomID: segment.RoomID,
				DateFrom: from.AddDate(0, 0, segment.From), DateTo: from.AddDate(0, 0, segment.To),
			})
			if store.IsExclusion(err) {
				return apperr.Conflict("ROOM_TAKEN", "Plan uygulanırken oda çakışması oluştu, yeniden deneyin")
			}
			if err != nil {
				return err
			}
		}
		plan.Applied = true
		return auditlog.WriteRef(ctx, q, actor, "assignment.applied", "assignment", from.Format(time.DateOnly), nil, map[string]any{
			"to": to.Format(time.DateOnly), "moves": plan.Result.Moves, "unassigned": len(plan.Unassigned),
		})
	})
	return plan, err
}

func (s *Service) compute(ctx context.Context, q *db.Queries, from, to time.Time) (Plan, error) {
	input, err := s.load(ctx, q, from, to)
	if err != nil {
		return Plan{}, err
	}
	input.Seed = s.Seed
	result := allocation.Solve(input)
	baseline := allocation.FirstFit(input)

	plan := Plan{Baseline: baseline.Moves, Rooms: len(input.Rooms), Moves: []PlanMove{}, Unassigned: []int64{}}
	for _, stay := range input.Stays {
		if stay.ID > 0 {
			plan.Stays++
		}
	}
	segments := make([]allocation.Segment, 0, len(result.Segments))
	for _, segment := range result.Segments {
		if segment.StayID <= 0 {
			continue
		}
		segments = append(segments, segment)
		plan.Moves = append(plan.Moves, PlanMove{
			ReservationID: segment.StayID, RoomID: segment.RoomID,
			From: from.AddDate(0, 0, segment.From).Format(time.DateOnly),
			To:   from.AddDate(0, 0, segment.To).Format(time.DateOnly),
		})
	}
	result.Segments = segments
	for _, id := range result.Unassigned {
		if id > 0 {
			plan.Unassigned = append(plan.Unassigned, id)
		}
	}
	result.Unassigned = plan.Unassigned
	plan.Result = result
	return plan, nil
}

func (s *Service) load(ctx context.Context, q *db.Queries, from, to time.Time) (allocation.Input, error) {
	rooms, err := q.ListRooms(ctx)
	if err != nil {
		return allocation.Input{}, err
	}
	input := allocation.Input{}
	for _, room := range rooms {
		if room.HkStatus == db.HkStatusOutOfOrder {
			continue
		}
		input.Rooms = append(input.Rooms, allocation.Room{ID: room.ID, TypeID: room.RoomTypeID, Floor: int(room.Floor), View: room.View})
	}
	adjacency, err := q.ListAdjacency(ctx)
	if err != nil {
		return allocation.Input{}, err
	}
	for _, pair := range adjacency {
		input.Adjacent = append(input.Adjacent, [2]int64{pair.RoomA, pair.RoomB})
	}
	rows, err := q.ListStaysForSolver(ctx, db.ListStaysForSolverParams{DateFrom: from, DateTo: to})
	if err != nil {
		return allocation.Input{}, err
	}
	end := to
	for _, row := range rows {
		if row.CheckOut.After(end) {
			end = row.CheckOut
		}
	}
	held, err := q.ListAssignmentsBetween(ctx, db.ListAssignmentsBetweenParams{DateFrom: from, DateTo: end})
	if err != nil {
		return allocation.Input{}, err
	}
	typeOf := map[int64]int64{}
	for _, room := range rooms {
		typeOf[room.ID] = room.RoomTypeID
	}
	holding := map[int64]bool{}
	for _, a := range held {
		holding[a.ReservationID] = true
		start, stop := dayIndex(from, a.DateFrom), dayIndex(from, a.DateTo)
		input.Stays = append(input.Stays, allocation.Stay{ID: -a.ID, TypeID: typeOf[a.RoomID], From: start, To: stop, PinRoom: a.RoomID})
	}
	for _, row := range rows {
		if holding[row.ID] {
			continue
		}
		stay := allocation.Stay{
			ID: row.ID, TypeID: row.RoomTypeID, From: dayIndex(from, row.CheckIn), To: dayIndex(from, row.CheckOut),
		}
		if row.PrefFloor != nil {
			floor := int(*row.PrefFloor)
			stay.PrefFloor = &floor
		}
		if row.PrefView != nil {
			stay.PrefView = *row.PrefView
		}
		if row.AdjacentGroup != nil {
			stay.Group = *row.AdjacentGroup
		}
		input.Stays = append(input.Stays, stay)
	}
	return input, nil
}

func dayIndex(origin, day time.Time) int {
	return int(day.Sub(origin).Hours() / 24)
}

func (s *Service) Move(ctx context.Context, actor auditlog.Actor, reservationID, roomID int64) error {
	return s.Store.InTx(ctx, func(q *db.Queries) error {
		r, err := q.LockReservation(ctx, reservationID)
		if err != nil {
			return apperr.NotFound("Rezervasyon bulunamadı")
		}
		room, err := q.LockRoom(ctx, roomID)
		if err != nil {
			return apperr.NotFound("Oda bulunamadı")
		}
		if room.RoomTypeID != r.RoomTypeID {
			return apperr.New(http.StatusUnprocessableEntity, "ROOM_TYPE_MISMATCH", "Oda tipi rezervasyonla uyuşmuyor")
		}
		state, err := q.GetHotelState(ctx)
		if err != nil {
			return err
		}
		before, err := q.ListAssignmentsForReservation(ctx, reservationID)
		if err != nil {
			return err
		}
		start := r.CheckIn
		switch r.Status {
		case db.ReservationStatusConfirmed:
			err = q.DeleteAssignments(ctx, reservationID)
		case db.ReservationStatusCheckedIn:
			if room.HkStatus != db.HkStatusClean && room.HkStatus != db.HkStatusInspected {
				return apperr.Conflict("ROOM_NOT_READY", "Hedef oda hazır değil: "+room.Number)
			}
			if state.BusinessDate.After(r.CheckIn) {
				start = state.BusinessDate
				if err := q.TruncateAssignmentAt(ctx, db.TruncateAssignmentAtParams{ReservationID: reservationID, Cut: start}); err != nil {
					return err
				}
				err = q.DeleteAssignmentsFrom(ctx, db.DeleteAssignmentsFromParams{ReservationID: reservationID, Cut: start})
			} else {
				err = q.DeleteAssignments(ctx, reservationID)
			}
		default:
			return apperr.Conflict("INVALID_STATE", "Bu rezervasyon taşınamaz")
		}
		if err != nil {
			return err
		}
		err = q.InsertAssignment(ctx, db.InsertAssignmentParams{ReservationID: reservationID, RoomID: roomID, DateFrom: start, DateTo: r.CheckOut, Locked: true})
		if store.IsExclusion(err) {
			return apperr.Conflict("ROOM_TAKEN", "Oda bu tarihlerde dolu")
		}
		if err != nil {
			return err
		}
		return auditlog.Write(ctx, q, actor, "assignment.moved", "reservation", reservationID, before, map[string]any{"room": room.Number, "from": start.Format(time.DateOnly)})
	})
}
