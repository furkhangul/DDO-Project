package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"konakpro/internal/auth"
	"konakpro/internal/booking"
	"konakpro/internal/db"
	"konakpro/internal/nightaudit"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

type stayBody struct {
	RoomTypeID   int64    `json:"room_type_id"`
	RatePlanID   int64    `json:"rate_plan_id"`
	CheckIn      string   `json:"check_in"`
	CheckOut     string   `json:"check_out"`
	Adults       int      `json:"adults"`
	ChildrenAges []int    `json:"children_ages"`
	Codes        []string `json:"codes"`
}

func (b stayBody) dates() (time.Time, time.Time, error) {
	from, err := parseDate(b.CheckIn, "check_in")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parseDate(b.CheckOut, "check_out")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if to.Sub(from) > 60*24*time.Hour {
		return time.Time{}, time.Time{}, apperr.Invalid("Konaklama en fazla 60 gece olabilir")
	}
	return from, to, nil
}

func (s *Server) quote(w http.ResponseWriter, r *http.Request) error {
	var body stayBody
	if err := decode(r, &body); err != nil {
		return err
	}
	from, to, err := body.dates()
	if err != nil {
		return err
	}
	result, err := s.Booking.Quote(r.Context(), booking.QuoteRequest{
		RoomTypeID: body.RoomTypeID, RatePlanID: body.RatePlanID, CheckIn: from, CheckOut: to,
		Adults: body.Adults, ChildrenAges: body.ChildrenAges, Codes: body.Codes,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (s *Server) ownedGuest(r *http.Request, p auth.Principal) (*db.Guest, error) {
	if p.Role != db.UserRoleGuest {
		return nil, nil
	}
	guest, err := s.Store.GetGuestByUser(r.Context(), &p.UserID)
	if err != nil {
		return nil, apperr.Forbidden()
	}
	return &guest, nil
}

func (s *Server) requireAccess(r *http.Request, id int64) (db.GetReservationRow, error) {
	row, err := s.Store.GetReservation(r.Context(), id)
	if store.IsNotFound(err) {
		return row, apperr.NotFound("Rezervasyon bulunamadı")
	}
	if err != nil {
		return row, err
	}
	p := principalOf(r)
	if p.Role == db.UserRoleGuest && (row.GuestUserID == nil || *row.GuestUserID != p.UserID) {
		return row, apperr.Forbidden()
	}
	return row, nil
}

func (s *Server) createReservation(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		stayBody
		GuestID       *int64            `json:"guest_id"`
		Guest         *booking.NewGuest `json:"guest"`
		PrefFloor     *int32            `json:"pref_floor"`
		PrefView      *string           `json:"pref_view"`
		AdjacentGroup *string           `json:"adjacent_group"`
		QuoteHash     string            `json:"quote_hash"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	from, to, err := body.dates()
	if err != nil {
		return err
	}
	p := principalOf(r)
	own, err := s.ownedGuest(r, p)
	if err != nil {
		return err
	}
	if own != nil {
		body.GuestID, body.Guest = &own.ID, nil
	}
	out, err := s.Booking.Create(r.Context(), p.Actor(), booking.CreateRequest{
		GuestID: body.GuestID, Guest: body.Guest, RoomTypeID: body.RoomTypeID, RatePlanID: body.RatePlanID,
		CheckIn: from, CheckOut: to, Adults: body.Adults, ChildrenAges: body.ChildrenAges, Codes: body.Codes,
		PrefFloor: body.PrefFloor, PrefView: body.PrefView, AdjacentGroup: body.AdjacentGroup, QuoteHash: body.QuoteHash,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, reservationView(out))
}

func reservationView(r db.Reservation) map[string]any {
	return map[string]any{
		"id": r.ID, "code": r.Code, "status": r.Status, "room_type_id": r.RoomTypeID,
		"check_in": r.CheckIn.Format(time.DateOnly), "check_out": r.CheckOut.Format(time.DateOnly),
		"total_net_kurus": r.TotalNetKurus, "total_tax_kurus": r.TotalTaxKurus, "price_hash": r.PriceHash,
	}
}

func (s *Server) listReservations(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePage(r)
	if err != nil {
		return err
	}
	params := db.ListReservationsParams{Limit: p.Limit, BeforeID: p.Before, Search: optional(r, "q")}
	if raw := optional(r, "status"); raw != nil {
		status := db.ReservationStatus(*raw)
		if !status.Valid() {
			return apperr.Invalid("Geçersiz durum filtresi")
		}
		params.Status = db.NullReservationStatus{ReservationStatus: status, Valid: true}
	}
	if raw := optional(r, "arrival"); raw != nil {
		day, err := parseDate(*raw, "arrival")
		if err != nil {
			return err
		}
		params.Arrival = pgtype.Date{Time: day, Valid: true}
	}
	own, err := s.ownedGuest(r, principalOf(r))
	if err != nil {
		return err
	}
	if own != nil {
		params.GuestID = &own.ID
	}
	rows, err := s.Store.ListReservations(r.Context(), params)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, paged(rows, p.Limit, func(row db.ListReservationsRow) int64 { return row.ID }))
}

func (s *Server) getReservation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	row, err := s.requireAccess(r, id)
	if err != nil {
		return err
	}
	nights, err := s.Store.ListReservationNights(r.Context(), id)
	if err != nil {
		return err
	}
	segments, err := s.Store.ListAssignmentsForReservation(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"id": row.ID, "code": row.Code, "status": row.Status, "guest_id": row.GuestID, "guest_name": row.GuestName,
		"room_type_id": row.RoomTypeID, "type_code": row.TypeCode, "type_name": row.TypeName, "plan_code": row.PlanCode, "plan_name": row.PlanName,
		"check_in": row.CheckIn.Format(time.DateOnly), "check_out": row.CheckOut.Format(time.DateOnly),
		"adults": row.Adults, "children_ages": row.ChildrenAges, "channel_code": row.ChannelCode, "external_ref": row.ExternalRef,
		"pref_floor": row.PrefFloor, "pref_view": row.PrefView, "adjacent_group": row.AdjacentGroup,
		"booked_on": row.BookedOn.Format(time.DateOnly), "total_net_kurus": row.TotalNetKurus, "total_tax_kurus": row.TotalTaxKurus,
		"price_hash": row.PriceHash, "nights": nights, "assignments": segments,
	})
}

func (s *Server) reservationPrice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireAccess(r, id); err != nil {
		return err
	}
	check, err := s.Booking.VerifyPrice(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, check)
}

func (s *Server) modifyReservation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body stayBody
	if err := decode(r, &body); err != nil {
		return err
	}
	from, to, err := body.dates()
	if err != nil {
		return err
	}
	out, err := s.Booking.Modify(r.Context(), principalOf(r).Actor(), id, booking.ModifyRequest{
		RoomTypeID: body.RoomTypeID, CheckIn: from, CheckOut: to, Adults: body.Adults, ChildrenAges: body.ChildrenAges,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, reservationView(out))
}

func (s *Server) cancelReservation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireAccess(r, id); err != nil {
		return err
	}
	if err := s.Booking.Cancel(r.Context(), principalOf(r).Actor(), id); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "cancelled"})
}

func (s *Server) walkReservation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		PartnerHotelID int64  `json:"partner_hotel_id"`
		Reason         string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Reason == "" {
		return apperr.Invalid("Yönlendirme nedeni zorunlu")
	}
	out, err := s.Booking.Walk(r.Context(), principalOf(r).Actor(), id, booking.WalkRequest{PartnerHotelID: body.PartnerHotelID, Reason: body.Reason})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, out)
}

func (s *Server) checkIn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		RoomID *int64 `json:"room_id"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	if err := s.Booking.CheckIn(r.Context(), principalOf(r).Actor(), id, body.RoomID); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "checked_in"})
}

func (s *Server) selfCheckIn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireAccess(r, id); err != nil {
		return err
	}
	if err := s.Booking.CheckIn(r.Context(), principalOf(r).Actor(), id, nil); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "checked_in"})
}

func (s *Server) checkOut(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Booking.CheckOut(r.Context(), principalOf(r).Actor(), id); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "checked_out"})
}

func (s *Server) moveReservation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		RoomID int64 `json:"room_id"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if err := s.Assign.Move(r.Context(), principalOf(r).Actor(), id, body.RoomID); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "room_id": body.RoomID})
}

func (s *Server) solveAssignments(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Apply bool   `json:"apply"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	from, err := parseDate(body.From, "from")
	if err != nil {
		return err
	}
	to, err := parseDate(body.To, "to")
	if err != nil {
		return err
	}
	plan, err := s.Assign.Solve(r.Context(), principalOf(r).Actor(), from, to, body.Apply)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, plan)
}

func (s *Server) listFolios(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireAccess(r, id); err != nil {
		return err
	}
	views, err := s.Folio.List(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func (s *Server) getFolio(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	view, err := s.Folio.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, view)
}

func (s *Server) addCharge(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		AmountKurus int64  `json:"amount_kurus"`
		Description string `json:"description"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	line, err := s.Folio.Charge(r.Context(), principalOf(r).Actor(), id, body.AmountKurus, body.Description)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, line)
}

func (s *Server) addPayment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		AmountKurus int64  `json:"amount_kurus"`
		Method      string `json:"method"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	line, err := s.Folio.Pay(r.Context(), principalOf(r).Actor(), id, body.AmountKurus, body.Method)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, line)
}

func (s *Server) addRefund(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		PaymentLineID int64 `json:"payment_line_id"`
		AmountKurus   int64 `json:"amount_kurus"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	line, err := s.Folio.Refund(r.Context(), principalOf(r).Actor(), id, body.PaymentLineID, body.AmountKurus)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, line)
}

func (s *Server) splitFolio(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		TargetFolioID *int64  `json:"target_folio_id"`
		PayerType     string  `json:"payer_type"`
		PayerName     string  `json:"payer_name"`
		TaxNumber     *string `json:"tax_number"`
		LineIDs       []int64 `json:"line_ids"`
		AmountKurus   int64   `json:"amount_kurus"`
		Percent       int     `json:"percent"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	view, err := s.Folio.Split(r.Context(), principalOf(r).Actor(), id, foliosplit(body.TargetFolioID, body.PayerType, body.PayerName, body.TaxNumber, body.LineIDs, body.AmountKurus, body.Percent))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, view)
}

func (s *Server) folioIntegrity(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	row, err := s.Store.CheckFolioIntegrity(r.Context(), id)
	if store.IsNotFound(err) {
		return apperr.NotFound("Folyo bulunamadı")
	}
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"folio_id": id, "balance_kurus": row.BalanceKurus, "lines_total_kurus": row.LinesTotalKurus,
		"balanced": row.BalanceKurus == row.LinesTotalKurus,
	})
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) error {
	state, err := s.Store.GetHotelState(r.Context())
	if err != nil {
		return err
	}
	rows, err := s.Store.ListTasks(r.Context(), state.BusinessDate)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"business_date": state.BusinessDate.Format(time.DateOnly), "items": rows})
}

func (s *Server) kpi(w http.ResponseWriter, r *http.Request) error {
	from, err := parseDate(r.URL.Query().Get("from"), "from")
	if err != nil {
		return err
	}
	to, err := parseDate(r.URL.Query().Get("to"), "to")
	if err != nil {
		return err
	}
	report, err := s.Reports.KPI(r.Context(), from, to, r.URL.Query().Get("bucket") == "month")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, report)
}

func (s *Server) runAudit(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		CrashAfter  int  `json:"crash_after"`
		HardExit    bool `json:"hard_exit"`
		ItemDelayMS int  `json:"item_delay_ms"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	report, err := s.NightAudit.Run(detached(r), principalOf(r).Actor(), nightaudit.Fault{
		CrashAfter: body.CrashAfter, HardExit: body.HardExit, ItemDelay: time.Duration(body.ItemDelayMS) * time.Millisecond,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, report)
}
