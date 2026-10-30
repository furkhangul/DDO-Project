package housekeeping

import (
	"context"

	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

type Service struct {
	Store *store.Store
}

type CreateRequest struct {
	RoomID     int64
	TaskType   string
	AssignedTo *int64
	Note       string
}

type UpdateRequest struct {
	Status     string
	AssignedTo *int64
	Note       string
}

func (s *Service) Create(ctx context.Context, actor auditlog.Actor, req CreateRequest) (db.HkTask, error) {
	var out db.HkTask
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		state, err := q.GetHotelState(ctx)
		if err != nil {
			return err
		}
		out, err = q.UpsertTask(ctx, db.UpsertTaskParams{
			RoomID: req.RoomID, TaskType: req.TaskType, BusinessDate: state.BusinessDate, AssignedTo: req.AssignedTo, Note: req.Note,
		})
		if store.IsForeignKey(err) {
			return apperr.NotFound("Oda bulunamadı")
		}
		if store.IsCheck(err) {
			return apperr.Invalid("Geçersiz görev tipi")
		}
		if err != nil {
			return err
		}
		return auditlog.Write(ctx, q, actor, "housekeeping.task_created", "hk_task", out.ID, nil, out)
	})
	return out, err
}

func (s *Service) Update(ctx context.Context, actor auditlog.Actor, id int64, req UpdateRequest) (db.HkTask, error) {
	var out db.HkTask
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		before, err := q.GetTask(ctx, id)
		if err != nil {
			return apperr.NotFound("Görev bulunamadı")
		}
		out, err = q.UpdateTask(ctx, db.UpdateTaskParams{ID: id, Status: req.Status, AssignedTo: req.AssignedTo, Note: req.Note})
		if store.IsCheck(err) {
			return apperr.Invalid("Geçersiz görev durumu")
		}
		if err != nil {
			return err
		}
		if req.Status == "tamamlandi" && before.Status != "tamamlandi" {
			if err := applyRoomEffect(ctx, q, before); err != nil {
				return err
			}
		}
		return auditlog.Write(ctx, q, actor, "housekeeping.task_updated", "hk_task", id, before, out)
	})
	return out, err
}

func applyRoomEffect(ctx context.Context, q *db.Queries, task db.HkTask) error {
	var next db.HkStatus
	switch task.TaskType {
	case "cikis_temizligi", "bakim":
		next = db.HkStatusClean
	case "kontrol":
		next = db.HkStatusInspected
	default:
		return nil
	}
	_, err := q.SetRoomStatus(ctx, db.SetRoomStatusParams{ID: task.RoomID, HkStatus: next})
	return err
}

func (s *Service) SetRoomStatus(ctx context.Context, actor auditlog.Actor, id int64, status db.HkStatus) (db.Room, error) {
	if !status.Valid() {
		return db.Room{}, apperr.Invalid("Geçersiz oda durumu")
	}
	var out db.Room
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		before, err := q.LockRoom(ctx, id)
		if err != nil {
			return apperr.NotFound("Oda bulunamadı")
		}
		out, err = q.SetRoomStatus(ctx, db.SetRoomStatusParams{ID: id, HkStatus: status})
		if err != nil {
			return err
		}
		return auditlog.Write(ctx, q, actor, "room.status_changed", "room", id, map[string]any{"hk_status": before.HkStatus}, map[string]any{"hk_status": status})
	})
	return out, err
}
