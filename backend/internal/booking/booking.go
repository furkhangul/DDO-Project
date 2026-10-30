package booking

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
	"konakpro/internal/pricing"
)

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type Service struct {
	Store *store.Store
}

type NewGuest struct {
	FullName string  `json:"full_name"`
	Email    *string `json:"email"`
	Phone    *string `json:"phone"`
}

type CreateRequest struct {
	GuestID       *int64
	Guest         *NewGuest
	RoomTypeID    int64
	RatePlanID    int64
	CheckIn       time.Time
	CheckOut      time.Time
	Adults        int
	ChildrenAges  []int
	Codes         []string
	PrefFloor     *int32
	PrefView      *string
	AdjacentGroup *string
	QuoteHash     string
	ChannelID     *int64
	ExternalRef   *string
	OTANetKurus   *int64
}

type QuoteRequest struct {
	RoomTypeID   int64
	RatePlanID   int64
	CheckIn      time.Time
	CheckOut     time.Time
	Adults       int
	ChildrenAges []int
	Codes        []string
}

func (s *Service) Quote(ctx context.Context, req QuoteRequest) (pricing.Quote, error) {
	state, err := s.Store.GetHotelState(ctx)
	if err != nil {
		return pricing.Quote{}, err
	}
	return quote(ctx, s.Store.Queries, req, state.BusinessDate)
}

func quote(ctx context.Context, q *db.Queries, req QuoteRequest, businessDate time.Time) (pricing.Quote, error) {
	_, _, result, err := priced(ctx, q, req, businessDate)
	return result, err
}

func priced(ctx context.Context, q *db.Queries, req QuoteRequest, businessDate time.Time) (pricing.Input, pricing.Rules, pricing.Quote, error) {
	rules, err := LoadRules(ctx, q, req.RoomTypeID, req.RatePlanID, req.CheckIn, req.CheckOut, req.Codes)
	if err != nil {
		return pricing.Input{}, pricing.Rules{}, pricing.Quote{}, err
	}
	input := pricing.Input{
		CheckIn:      req.CheckIn,
		CheckOut:     req.CheckOut,
		BookedOn:     businessDate,
		Adults:       req.Adults,
		ChildrenAges: slices.Clone(req.ChildrenAges),
		Codes:        slices.Clone(req.Codes),
	}
	if input.ChildrenAges == nil {
		input.ChildrenAges = []int{}
	}
	if input.Codes == nil {
		input.Codes = []string{}
	}
	result, err := pricing.Calculate(input, rules)
	if err != nil {
		return pricing.Input{}, pricing.Rules{}, pricing.Quote{}, priceError(err)
	}
	return input, rules, result, nil
}

func (s *Service) Create(ctx context.Context, actor auditlog.Actor, req CreateRequest) (db.Reservation, error) {
	var out db.Reservation
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		var err error
		out, err = CreateTx(ctx, q, actor, req)
		return err
	})
	return out, err
}

func CreateTx(ctx context.Context, q *db.Queries, actor auditlog.Actor, req CreateRequest) (db.Reservation, error) {
	state, err := q.GetHotelState(ctx)
	if err != nil {
		return db.Reservation{}, err
	}
	if req.CheckIn.Before(state.BusinessDate) {
		return db.Reservation{}, apperr.New(http.StatusUnprocessableEntity, "ARRIVAL_IN_PAST", "Giriş tarihi otel gününden önce olamaz")
	}
	if !req.CheckOut.After(req.CheckIn) {
		return db.Reservation{}, apperr.New(http.StatusUnprocessableEntity, "INVALID_STAY", "Çıkış tarihi girişten sonra olmalı")
	}

	stay, err := priceStay(ctx, q, req, state.BusinessDate)
	if err != nil {
		return db.Reservation{}, err
	}
	if err := ReserveNights(ctx, q, req.RoomTypeID, req.CheckIn, req.CheckOut); err != nil {
		return db.Reservation{}, err
	}
	guestID, payer, err := resolveGuest(ctx, q, req)
	if err != nil {
		return db.Reservation{}, err
	}
	for _, code := range stay.Applied {
		consumed, err := q.ConsumeCampaign(ctx, code)
		if err != nil {
			return db.Reservation{}, err
		}
		if consumed == 0 {
			return db.Reservation{}, apperr.Conflict("CAMPAIGN_EXHAUSTED", "Kampanya kullanım limiti doldu: "+code)
		}
	}
	reservation, err := q.InsertReservation(ctx, db.InsertReservationParams{
		Code:          newCode(),
		GuestID:       guestID,
		RoomTypeID:    req.RoomTypeID,
		RatePlanID:    req.RatePlanID,
		CheckIn:       req.CheckIn,
		CheckOut:      req.CheckOut,
		Adults:        int32(req.Adults),
		ChildrenAges:  toInt32s(req.ChildrenAges),
		ChannelID:     req.ChannelID,
		ExternalRef:   req.ExternalRef,
		PrefFloor:     req.PrefFloor,
		PrefView:      req.PrefView,
		AdjacentGroup: req.AdjacentGroup,
		BookedOn:      state.BusinessDate,
		TotalNetKurus: stay.Net,
		TotalTaxKurus: stay.Tax,
		PriceInput:    stay.Input,
		PriceRules:    stay.Rules,
		PriceHash:     stay.Hash,
		CreatedBy:     actor.ID,
	})
	if err != nil {
		return db.Reservation{}, err
	}
	if err := writeNights(ctx, q, reservation.ID, stay.Nights); err != nil {
		return db.Reservation{}, err
	}
	for _, code := range stay.Applied {
		if err := q.LinkReservationCampaign(ctx, db.LinkReservationCampaignParams{ReservationID: reservation.ID, Code: code}); err != nil {
			return db.Reservation{}, err
		}
	}
	if _, err := q.CreateFolio(ctx, db.CreateFolioParams{
		ReservationID: reservation.ID, PayerType: "guest", PayerName: payer, IsPrimary: true,
	}); err != nil {
		return db.Reservation{}, err
	}
	return reservation, auditlog.Write(ctx, q, actor, "reservation.created", "reservation", reservation.ID, nil, summary(reservation))
}

type pricedStay struct {
	Net, Tax     int64
	Input, Rules []byte
	Hash         string
	Nights       []db.InsertReservationNightParams
	Applied      []string
}

func priceStay(ctx context.Context, q *db.Queries, req CreateRequest, businessDate time.Time) (pricedStay, error) {
	if req.OTANetKurus != nil {
		return otaStay(ctx, q, req)
	}
	input, rules, result, err := priced(ctx, q, QuoteRequest{
		RoomTypeID: req.RoomTypeID, RatePlanID: req.RatePlanID, CheckIn: req.CheckIn, CheckOut: req.CheckOut,
		Adults: req.Adults, ChildrenAges: req.ChildrenAges, Codes: req.Codes,
	}, businessDate)
	if err != nil {
		return pricedStay{}, err
	}
	if req.QuoteHash != "" && req.QuoteHash != result.Hash {
		return pricedStay{}, apperr.Conflict("PRICE_CHANGED", "Fiyat teklif alındıktan sonra değişti, lütfen yeniden hesaplayın")
	}
	rawInput, err := json.Marshal(input)
	if err != nil {
		return pricedStay{}, err
	}
	rawRules, err := json.Marshal(rules)
	if err != nil {
		return pricedStay{}, err
	}
	stay := pricedStay{
		Net: result.NetKurus, Tax: result.KDVKurus + result.AccommodationTax,
		Input: rawInput, Rules: rawRules, Hash: result.Hash, Applied: result.AppliedCodes,
	}
	for _, n := range result.Nights {
		stay.Nights = append(stay.Nights, db.InsertReservationNightParams{
			StayDate: n.Date, NetKurus: n.NetKurus, KdvKurus: n.KDVKurus, AccomKurus: n.AccommodationTax,
		})
	}
	return stay, nil
}

func otaStay(ctx context.Context, q *db.Queries, req CreateRequest) (pricedStay, error) {
	settings, err := q.GetPricingSettings(ctx)
	if err != nil {
		return pricedStay{}, err
	}
	nights := int64(req.CheckOut.Sub(req.CheckIn).Hours() / 24)
	share, rest := *req.OTANetKurus/nights, *req.OTANetKurus%nights
	stay := pricedStay{Hash: "ota"}
	day := req.CheckIn
	for i := int64(0); i < nights; i++ {
		net := share
		if i < rest {
			net++
		}
		kdv, accom := percent(net, int64(settings.KdvBp)), percent(net, int64(settings.AccommodationBp))
		stay.Nights = append(stay.Nights, db.InsertReservationNightParams{StayDate: day, NetKurus: net, KdvKurus: kdv, AccomKurus: accom})
		stay.Net += net
		stay.Tax += kdv + accom
		day = day.AddDate(0, 0, 1)
	}
	return stay, nil
}

func percent(amount, bp int64) int64 {
	return (amount*bp + 5000) / 10000
}

func writeNights(ctx context.Context, q *db.Queries, reservationID int64, nights []db.InsertReservationNightParams) error {
	for _, n := range nights {
		n.ReservationID = reservationID
		if err := q.InsertReservationNight(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

func resolveGuest(ctx context.Context, q *db.Queries, req CreateRequest) (int64, string, error) {
	if req.GuestID != nil {
		guest, err := q.GetGuest(ctx, *req.GuestID)
		if err != nil {
			return 0, "", notFound(err, "Misafir bulunamadı")
		}
		return guest.ID, guest.FullName, nil
	}
	if req.Guest == nil || strings.TrimSpace(req.Guest.FullName) == "" {
		return 0, "", apperr.Invalid("Misafir adı zorunlu")
	}
	guest, err := q.CreateGuest(ctx, db.CreateGuestParams{FullName: strings.TrimSpace(req.Guest.FullName), Email: req.Guest.Email, Phone: req.Guest.Phone})
	if err != nil {
		return 0, "", err
	}
	return guest.ID, guest.FullName, nil
}

func ReserveNights(ctx context.Context, q *db.Queries, roomTypeID int64, from, to time.Time) error {
	if err := q.EnsureInventory(ctx, db.EnsureInventoryParams{RoomTypeID: roomTypeID, DateFrom: from, DateTo: to}); err != nil {
		return err
	}
	rows, err := q.LockInventory(ctx, db.LockInventoryParams{RoomTypeID: roomTypeID, StayDate: from, StayDate_2: to})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Sold >= row.Capacity+row.OverbookLimit {
			return apperr.Conflict("SOLD_OUT", fmt.Sprintf("%s gecesi için müsaitlik yok", row.StayDate.Format(time.DateOnly)))
		}
	}
	_, err = q.ConsumeInventory(ctx, db.ConsumeInventoryParams{RoomTypeID: roomTypeID, StayDate: from, StayDate_2: to})
	return err
}

func ReleaseNights(ctx context.Context, q *db.Queries, roomTypeID int64, from, to time.Time) error {
	if !to.After(from) {
		return nil
	}
	if _, err := q.LockInventory(ctx, db.LockInventoryParams{RoomTypeID: roomTypeID, StayDate: from, StayDate_2: to}); err != nil {
		return err
	}
	_, err := q.ReleaseInventory(ctx, db.ReleaseInventoryParams{RoomTypeID: roomTypeID, StayDate: from, StayDate_2: to})
	return err
}

func (s *Service) Cancel(ctx context.Context, actor auditlog.Actor, id int64) error {
	return s.Store.InTx(ctx, func(q *db.Queries) error {
		return CancelTx(ctx, q, actor, id)
	})
}

func CancelTx(ctx context.Context, q *db.Queries, actor auditlog.Actor, id int64) error {
	r, err := q.LockReservation(ctx, id)
	if err != nil {
		return notFound(err, "Rezervasyon bulunamadı")
	}
	if r.Status != db.ReservationStatusConfirmed {
		return apperr.Conflict("INVALID_STATE", "Yalnızca onaylı rezervasyon iptal edilebilir")
	}
	balance, err := q.OpenFolioBalance(ctx, id)
	if err != nil {
		return err
	}
	if balance != 0 {
		return apperr.Conflict("FOLIO_NOT_SETTLED", "İptal öncesi folyo bakiyesi sıfırlanmalı")
	}
	if err := vacate(ctx, q, r); err != nil {
		return err
	}
	if err := q.ReleaseCampaigns(ctx, id); err != nil {
		return err
	}
	if err := q.UpdateReservationStatus(ctx, db.UpdateReservationStatusParams{ID: id, Status: db.ReservationStatusCancelled}); err != nil {
		return err
	}
	return auditlog.Write(ctx, q, actor, "reservation.cancelled", "reservation", id, summary(r), map[string]string{"status": "cancelled"})
}

func vacate(ctx context.Context, q *db.Queries, r db.Reservation) error {
	if err := ReleaseNights(ctx, q, r.RoomTypeID, r.CheckIn, r.CheckOut); err != nil {
		return err
	}
	return q.DeleteAssignments(ctx, r.ID)
}

type ModifyRequest struct {
	RoomTypeID   int64
	CheckIn      time.Time
	CheckOut     time.Time
	Adults       int
	ChildrenAges []int
	OTANetKurus  *int64
}

func (s *Service) Modify(ctx context.Context, actor auditlog.Actor, id int64, req ModifyRequest) (db.Reservation, error) {
	var out db.Reservation
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		var err error
		out, err = ModifyTx(ctx, q, actor, id, req)
		return err
	})
	return out, err
}

func ModifyTx(ctx context.Context, q *db.Queries, actor auditlog.Actor, id int64, req ModifyRequest) (db.Reservation, error) {
	r, err := q.LockReservation(ctx, id)
	if err != nil {
		return db.Reservation{}, notFound(err, "Rezervasyon bulunamadı")
	}
	if r.Status != db.ReservationStatusConfirmed {
		return db.Reservation{}, apperr.Conflict("INVALID_STATE", "Yalnızca onaylı rezervasyon değiştirilebilir")
	}
	state, err := q.GetHotelState(ctx)
	if err != nil {
		return db.Reservation{}, err
	}
	if req.CheckIn.Before(state.BusinessDate) {
		return db.Reservation{}, apperr.New(http.StatusUnprocessableEntity, "ARRIVAL_IN_PAST", "Giriş tarihi otel gününden önce olamaz")
	}
	if !req.CheckOut.After(req.CheckIn) {
		return db.Reservation{}, apperr.New(http.StatusUnprocessableEntity, "INVALID_STAY", "Çıkış tarihi girişten sonra olmalı")
	}
	stay, err := priceStay(ctx, q, CreateRequest{
		RoomTypeID: req.RoomTypeID, RatePlanID: r.RatePlanID, CheckIn: req.CheckIn, CheckOut: req.CheckOut,
		Adults: req.Adults, ChildrenAges: req.ChildrenAges, OTANetKurus: req.OTANetKurus,
	}, state.BusinessDate)
	if err != nil {
		return db.Reservation{}, err
	}
	if err := vacate(ctx, q, r); err != nil {
		return db.Reservation{}, err
	}
	if err := ReserveNights(ctx, q, req.RoomTypeID, req.CheckIn, req.CheckOut); err != nil {
		return db.Reservation{}, err
	}
	if err := q.DeleteReservationNights(ctx, id); err != nil {
		return db.Reservation{}, err
	}
	if err := writeNights(ctx, q, id, stay.Nights); err != nil {
		return db.Reservation{}, err
	}
	updated, err := q.UpdateReservationStay(ctx, db.UpdateReservationStayParams{
		ID: id, RoomTypeID: req.RoomTypeID, CheckIn: req.CheckIn, CheckOut: req.CheckOut,
		Adults: int32(req.Adults), ChildrenAges: toInt32s(req.ChildrenAges),
		TotalNetKurus: stay.Net, TotalTaxKurus: stay.Tax, PriceInput: stay.Input, PriceRules: stay.Rules, PriceHash: stay.Hash,
	})
	if err != nil {
		return db.Reservation{}, err
	}
	return updated, auditlog.Write(ctx, q, actor, "reservation.modified", "reservation", id, summary(r), summary(updated))
}

type PriceCheck struct {
	Stored   []db.ReservationNight `json:"stored_nights"`
	Replayed *pricing.Quote        `json:"replayed"`
	Hash     string                `json:"stored_hash"`
	Matches  bool                  `json:"matches"`
}

func (s *Service) VerifyPrice(ctx context.Context, id int64) (PriceCheck, error) {
	r, err := s.Store.GetReservation(ctx, id)
	if err != nil {
		return PriceCheck{}, notFound(err, "Rezervasyon bulunamadı")
	}
	nights, err := s.Store.ListReservationNights(ctx, id)
	if err != nil {
		return PriceCheck{}, err
	}
	check := PriceCheck{Stored: nights, Hash: r.PriceHash}
	if len(r.PriceInput) == 0 || len(r.PriceRules) == 0 {
		return check, nil
	}
	var input pricing.Input
	var rules pricing.Rules
	if err := json.Unmarshal(r.PriceInput, &input); err != nil {
		return PriceCheck{}, err
	}
	if err := json.Unmarshal(r.PriceRules, &rules); err != nil {
		return PriceCheck{}, err
	}
	replayed, err := pricing.Calculate(input, rules)
	if err != nil {
		return PriceCheck{}, err
	}
	check.Replayed = &replayed
	check.Matches = replayed.Hash == r.PriceHash && replayed.NetKurus == r.TotalNetKurus
	return check, nil
}

type WalkRequest struct {
	PartnerHotelID int64
	Reason         string
}

func (s *Service) Walk(ctx context.Context, actor auditlog.Actor, id int64, req WalkRequest) (db.Walk, error) {
	var out db.Walk
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		r, err := q.LockReservation(ctx, id)
		if err != nil {
			return notFound(err, "Rezervasyon bulunamadı")
		}
		if r.Status != db.ReservationStatusConfirmed {
			return apperr.Conflict("INVALID_STATE", "Yalnızca gelmemiş onaylı misafir yönlendirilebilir")
		}
		overbooked, err := q.CountOverbookedNightsForStay(ctx, db.CountOverbookedNightsForStayParams{
			RoomTypeID: r.RoomTypeID, StayDate: r.CheckIn, StayDate_2: r.CheckOut,
		})
		if err != nil {
			return err
		}
		if overbooked == 0 {
			return apperr.Conflict("NOT_OVERBOOKED", "Bu konaklamanın gecelerinde fazla satış yok")
		}
		partner, err := q.GetPartnerHotel(ctx, req.PartnerHotelID)
		if err != nil {
			return notFound(err, "Anlaşmalı otel bulunamadı")
		}
		if actor.ID == nil {
			return apperr.Forbidden()
		}
		nights := int32(r.CheckOut.Sub(r.CheckIn).Hours() / 24)
		out, err = q.InsertWalk(ctx, db.InsertWalkParams{
			ReservationID: id, PartnerHotelID: partner.ID, Nights: nights,
			CostKurus: partner.NightlyCostKurus * int64(nights), Reason: req.Reason, WalkedBy: *actor.ID,
		})
		if err != nil {
			return err
		}
		if err := vacate(ctx, q, r); err != nil {
			return err
		}
		if err := q.UpdateReservationStatus(ctx, db.UpdateReservationStatusParams{ID: id, Status: db.ReservationStatusWalked}); err != nil {
			return err
		}
		return auditlog.Write(ctx, q, actor, "reservation.walked", "reservation", id, summary(r), out)
	})
	return out, err
}

func summary(r db.Reservation) map[string]any {
	return map[string]any{
		"code": r.Code, "status": r.Status, "room_type_id": r.RoomTypeID,
		"check_in": r.CheckIn.Format(time.DateOnly), "check_out": r.CheckOut.Format(time.DateOnly),
		"total_net_kurus": r.TotalNetKurus,
	}
}

func newCode() string {
	raw := make([]byte, 7)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	var b strings.Builder
	b.WriteString("KP")
	for _, value := range raw {
		b.WriteByte(codeAlphabet[int(value)%len(codeAlphabet)])
	}
	return b.String()
}

func toInt32s(values []int) []int32 {
	out := make([]int32, len(values))
	for i, v := range values {
		out[i] = int32(v)
	}
	return out
}
