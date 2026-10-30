package api

import (
	"net/http"
	"time"

	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
)

type roomTypeBody struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	BaseOccupancy int32  `json:"base_occupancy"`
	MaxOccupancy  int32  `json:"max_occupancy"`
	OverbookPct   int32  `json:"overbook_pct"`
	Active        *bool  `json:"active"`
}

func (s *Server) listRoomTypes(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.ListRoomTypes(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) createRoomType(w http.ResponseWriter, r *http.Request) error {
	var body roomTypeBody
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Name == "" {
		return apperr.Invalid("Oda tipi adı zorunlu")
	}
	var out db.RoomType
	err := s.Store.InTx(r.Context(), func(q *db.Queries) error {
		var err error
		out, err = q.CreateRoomType(r.Context(), db.CreateRoomTypeParams{
			Code: body.Code, Name: body.Name, Description: body.Description,
			BaseOccupancy: body.BaseOccupancy, MaxOccupancy: body.MaxOccupancy, OverbookPct: body.OverbookPct,
		})
		if err != nil {
			return err
		}
		return auditlog.Write(r.Context(), q, principalOf(r).Actor(), "room_type.created", "room_type", out.ID, nil, out)
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateRoomType(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body roomTypeBody
	if err := decode(r, &body); err != nil {
		return err
	}
	var out db.RoomType
	err = s.Store.InTx(r.Context(), func(q *db.Queries) error {
		before, err := q.GetRoomType(r.Context(), id)
		if err != nil {
			return apperr.NotFound("Oda tipi bulunamadı")
		}
		active := before.Active
		if body.Active != nil {
			active = *body.Active
		}
		out, err = q.UpdateRoomType(r.Context(), db.UpdateRoomTypeParams{
			ID: id, Name: body.Name, Description: body.Description, BaseOccupancy: body.BaseOccupancy,
			MaxOccupancy: body.MaxOccupancy, OverbookPct: body.OverbookPct, Active: active,
		})
		if err != nil {
			return err
		}
		if err := s.syncInventory(r, q, id); err != nil {
			return err
		}
		return auditlog.Write(r.Context(), q, principalOf(r).Actor(), "room_type.updated", "room_type", id, before, out)
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) syncInventory(r *http.Request, q *db.Queries, typeID int64) error {
	state, err := q.GetHotelState(r.Context())
	if err != nil {
		return err
	}
	_, err = q.SyncInventoryLimits(r.Context(), db.SyncInventoryLimitsParams{ID: typeID, StayDate: state.BusinessDate})
	return err
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.ListRooms(r.Context())
	if err != nil {
		return err
	}
	adjacency, err := s.Store.ListAdjacency(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": rows, "adjacency": adjacency})
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Number     string `json:"number"`
		RoomTypeID int64  `json:"room_type_id"`
		Floor      int32  `json:"floor"`
		View       string `json:"view"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Number == "" {
		return apperr.Invalid("Oda numarası zorunlu")
	}
	var out db.Room
	err := s.Store.InTx(r.Context(), func(q *db.Queries) error {
		var err error
		out, err = q.CreateRoom(r.Context(), db.CreateRoomParams{Number: body.Number, RoomTypeID: body.RoomTypeID, Floor: body.Floor, View: body.View})
		if err != nil {
			return err
		}
		if err := s.syncInventory(r, q, body.RoomTypeID); err != nil {
			return err
		}
		return auditlog.Write(r.Context(), q, principalOf(r).Actor(), "room.created", "room", out.ID, nil, out)
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateRoom(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		Floor  int32  `json:"floor"`
		View   string `json:"view"`
		Active bool   `json:"active"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	var out db.Room
	err = s.Store.InTx(r.Context(), func(q *db.Queries) error {
		before, err := q.LockRoom(r.Context(), id)
		if err != nil {
			return apperr.NotFound("Oda bulunamadı")
		}
		out, err = q.UpdateRoom(r.Context(), db.UpdateRoomParams{ID: id, Floor: body.Floor, View: body.View, Active: body.Active})
		if err != nil {
			return err
		}
		if err := s.syncInventory(r, q, before.RoomTypeID); err != nil {
			return err
		}
		return auditlog.Write(r.Context(), q, principalOf(r).Actor(), "room.updated", "room", id, before, out)
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) addAdjacency(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RoomA int64 `json:"room_a"`
		RoomB int64 `json:"room_b"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.RoomA == body.RoomB {
		return apperr.Invalid("Oda kendisine bitişik olamaz")
	}
	if body.RoomA > body.RoomB {
		body.RoomA, body.RoomB = body.RoomB, body.RoomA
	}
	if err := s.Store.AddAdjacency(r.Context(), db.AddAdjacencyParams{RoomA: body.RoomA, RoomB: body.RoomB}); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, body)
}

func (s *Server) setRoomStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		Status db.HkStatus `json:"status"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	out, err := s.Housekeeping.SetRoomStatus(r.Context(), principalOf(r).Actor(), id, body.Status)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) availability(w http.ResponseWriter, r *http.Request) error {
	from, to, err := queryRange(r)
	if err != nil {
		return err
	}
	types, err := s.Store.ListRoomTypes(r.Context())
	if err != nil {
		return err
	}
	for _, t := range types {
		if err := s.Store.EnsureInventory(r.Context(), db.EnsureInventoryParams{RoomTypeID: t.ID, DateFrom: from, DateTo: to}); err != nil {
			return err
		}
	}
	rows, err := s.Store.ListAvailability(r.Context(), db.ListAvailabilityParams{StayDate: from, StayDate_2: to})
	if err != nil {
		return err
	}
	type night struct {
		Date      string `json:"date"`
		RoomType  string `json:"room_type"`
		TypeName  string `json:"type_name"`
		TypeID    int64  `json:"room_type_id"`
		Capacity  int32  `json:"capacity"`
		Limit     int32  `json:"overbook_limit"`
		Sold      int32  `json:"sold"`
		Available int32  `json:"available"`
	}
	items := make([]night, 0, len(rows))
	for _, row := range rows {
		available := row.Capacity + row.OverbookLimit - row.Sold
		if available < 0 {
			available = 0
		}
		items = append(items, night{
			Date: row.StayDate.Format(time.DateOnly), RoomType: row.TypeCode, TypeName: row.TypeName, TypeID: row.RoomTypeID,
			Capacity: row.Capacity, Limit: row.OverbookLimit, Sold: row.Sold, Available: available,
		})
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) ratePlans(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.ListRatePlans(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request) error {
	from, to, err := queryRange(r)
	if err != nil {
		return err
	}
	rooms, err := s.Store.ListRooms(r.Context())
	if err != nil {
		return err
	}
	segments, err := s.Store.CalendarSegments(r.Context(), db.CalendarSegmentsParams{DateFrom: from, DateTo: to})
	if err != nil {
		return err
	}
	unassigned, err := s.Store.CalendarUnassigned(r.Context(), db.CalendarUnassignedParams{DateFrom: from, DateTo: to})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms, "segments": segments, "unassigned": unassigned})
}

func (s *Server) overbooking(w http.ResponseWriter, r *http.Request) error {
	state, err := s.Store.GetHotelState(r.Context())
	if err != nil {
		return err
	}
	nights, err := s.Store.ListOverbookedNights(r.Context(), state.BusinessDate)
	if err != nil {
		return err
	}
	candidates, err := s.Store.ListWalkCandidates(r.Context())
	if err != nil {
		return err
	}
	partners, err := s.Store.ListPartnerHotels(r.Context())
	if err != nil {
		return err
	}
	walks, err := s.Store.ListWalks(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"nights": nights, "candidates": candidates, "partners": partners, "walks": walks})
}
