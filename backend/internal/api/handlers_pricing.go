package api

import (
	"net/http"
	"time"

	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/folio"
	"konakpro/internal/housekeeping"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/pricing"
)

func foliosplit(target *int64, payerType, payerName string, taxNumber *string, lines []int64, amount int64, percent int) folio.SplitRequest {
	return folio.SplitRequest{
		TargetFolioID: target, PayerType: payerType, PayerName: payerName, TaxNumber: taxNumber,
		LineIDs: lines, AmountKurus: amount, Percent: percent,
	}
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RoomID     int64  `json:"room_id"`
		TaskType   string `json:"task_type"`
		AssignedTo *int64 `json:"assigned_to"`
		Note       string `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	out, err := s.Housekeeping.Create(r.Context(), principalOf(r).Actor(), housekeeping.CreateRequest{
		RoomID: body.RoomID, TaskType: body.TaskType, AssignedTo: body.AssignedTo, Note: body.Note,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		Status     string `json:"status"`
		AssignedTo *int64 `json:"assigned_to"`
		Note       string `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	out, err := s.Housekeeping.Update(r.Context(), principalOf(r).Actor(), id, housekeeping.UpdateRequest{
		Status: body.Status, AssignedTo: body.AssignedTo, Note: body.Note,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) pricingOverview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	settings, err := s.Store.GetPricingSettings(ctx)
	if err != nil {
		return err
	}
	plans, err := s.Store.ListRatePlans(ctx)
	if err != nil {
		return err
	}
	rates, err := s.Store.ListRoomRates(ctx)
	if err != nil {
		return err
	}
	seasons, err := s.Store.ListSeasons(ctx)
	if err != nil {
		return err
	}
	tiers, err := s.Store.ListEarlyTiers(ctx)
	if err != nil {
		return err
	}
	campaigns, err := s.Store.ListCampaigns(ctx)
	if err != nil {
		return err
	}
	compat, err := s.Store.ListCampaignCompat(ctx)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"settings": settings, "plans": plans, "rates": rates, "seasons": seasons,
		"early_tiers": tiers, "campaigns": campaigns, "compatible": compat,
	})
}

func (s *Server) audited(r *http.Request, action, entity string, id int64, after any, fn func(q *db.Queries) error) error {
	return s.Store.InTx(r.Context(), func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}
		return auditlog.Write(r.Context(), q, principalOf(r).Actor(), action, entity, id, nil, after)
	})
}

func (s *Server) updatePricingSettings(w http.ResponseWriter, r *http.Request) error {
	var body db.UpdatePricingSettingsParams
	if err := decode(r, &body); err != nil {
		return err
	}
	var out db.PricingSetting
	err := s.audited(r, "pricing.settings_updated", "pricing_settings", 1, body, func(q *db.Queries) error {
		var err error
		out, err = q.UpdatePricingSettings(r.Context(), body)
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) upsertRate(w http.ResponseWriter, r *http.Request) error {
	var body db.UpsertRoomRateParams
	if err := decode(r, &body); err != nil {
		return err
	}
	var out db.RoomRate
	err := s.audited(r, "pricing.rate_updated", "room_rate", body.RoomTypeID, body, func(q *db.Queries) error {
		var err error
		out, err = q.UpsertRoomRate(r.Context(), body)
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSeason(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name         string `json:"name"`
		From         string `json:"from"`
		To           string `json:"to"`
		MultiplierBP int32  `json:"multiplier_bp"`
		MinStay      int32  `json:"min_stay"`
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
	if !to.After(from) {
		return apperr.Invalid("Sezon bitişi başlangıçtan sonra olmalı")
	}
	var id int64
	err = s.audited(r, "pricing.season_created", "season", 0, body, func(q *db.Queries) error {
		var err error
		id, err = q.CreateSeason(r.Context(), db.CreateSeasonParams{
			Name: body.Name, DateFrom: from, DateTo: to, MultiplierBp: body.MultiplierBP, MinStay: body.MinStay,
		})
		return err
	})
	if err != nil {
		return seasonConflict(err)
	}
	return writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func seasonConflict(err error) error {
	if translate(err).Code == "CONSTRAINT_CONFLICT" {
		return apperr.Conflict("SEASON_OVERLAP", "Sezon tarihleri mevcut bir sezonla çakışıyor")
	}
	return err
}

func (s *Server) deleteSeason(w http.ResponseWriter, r *http.Request) error {
	return s.deleteBy(w, r, "pricing.season_deleted", "season", func(q *db.Queries, id int64) (int64, error) {
		return q.DeleteSeason(r.Context(), id)
	})
}

func (s *Server) deleteEarlyTier(w http.ResponseWriter, r *http.Request) error {
	return s.deleteBy(w, r, "pricing.tier_deleted", "early_tier", func(q *db.Queries, id int64) (int64, error) {
		return q.DeleteEarlyTier(r.Context(), id)
	})
}

func (s *Server) deleteBy(w http.ResponseWriter, r *http.Request, action, entity string, remove func(*db.Queries, int64) (int64, error)) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	err = s.audited(r, action, entity, id, nil, func(q *db.Queries) error {
		changed, err := remove(q, id)
		if err != nil {
			return err
		}
		if changed == 0 {
			return apperr.NotFound("Kayıt bulunamadı")
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) upsertEarlyTier(w http.ResponseWriter, r *http.Request) error {
	var body db.UpsertEarlyTierParams
	if err := decode(r, &body); err != nil {
		return err
	}
	var out db.EarlyBookingTier
	err := s.audited(r, "pricing.tier_updated", "early_tier", int64(body.MinLeadDays), body, func(q *db.Queries) error {
		var err error
		out, err = q.UpsertEarlyTier(r.Context(), body)
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Code             string `json:"code"`
		Name             string `json:"name"`
		DiscountBP       int32  `json:"discount_bp"`
		FixedKurus       int64  `json:"discount_fixed_kurus"`
		ValidFrom        string `json:"valid_from"`
		ValidTo          string `json:"valid_to"`
		MinNights        int32  `json:"min_nights"`
		WithEarlyBooking bool   `json:"with_early_booking"`
		MaxUses          *int32 `json:"max_uses"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	from, err := parseDate(body.ValidFrom, "valid_from")
	if err != nil {
		return err
	}
	to, err := parseDate(body.ValidTo, "valid_to")
	if err != nil {
		return err
	}
	if body.MinNights == 0 {
		body.MinNights = 1
	}
	var id int64
	err = s.audited(r, "pricing.campaign_created", "campaign", 0, body, func(q *db.Queries) error {
		var err error
		id, err = q.CreateCampaign(r.Context(), db.CreateCampaignParams{
			Code: body.Code, Name: body.Name, DiscountBp: body.DiscountBP, DiscountFixedKurus: body.FixedKurus,
			ValidFrom: from, ValidTo: to, MinNights: body.MinNights, WithEarlyBooking: body.WithEarlyBooking, MaxUses: body.MaxUses,
		})
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) setCampaignActive(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	err = s.audited(r, "pricing.campaign_toggled", "campaign", id, body, func(q *db.Queries) error {
		changed, err := q.SetCampaignActive(r.Context(), db.SetCampaignActiveParams{ID: id, Active: body.Active})
		if err != nil {
			return err
		}
		if changed == 0 {
			return apperr.NotFound("Kampanya bulunamadı")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": body.Active})
}

func compatPair(r *http.Request) (int64, int64, error) {
	var body struct {
		CampaignA int64 `json:"campaign_a"`
		CampaignB int64 `json:"campaign_b"`
	}
	if err := decode(r, &body); err != nil {
		return 0, 0, err
	}
	if body.CampaignA == body.CampaignB {
		return 0, 0, apperr.Invalid("Bir kampanya kendisiyle eşleştirilemez")
	}
	if body.CampaignA > body.CampaignB {
		return body.CampaignB, body.CampaignA, nil
	}
	return body.CampaignA, body.CampaignB, nil
}

func (s *Server) addCompat(w http.ResponseWriter, r *http.Request) error {
	a, b, err := compatPair(r)
	if err != nil {
		return err
	}
	err = s.audited(r, "pricing.compat_added", "campaign_compat", a, map[string]int64{"campaign_b": b}, func(q *db.Queries) error {
		return q.AddCampaignCompat(r.Context(), db.AddCampaignCompatParams{CampaignA: a, CampaignB: b})
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, map[string]int64{"campaign_a": a, "campaign_b": b})
}

func (s *Server) removeCompat(w http.ResponseWriter, r *http.Request) error {
	a, b, err := compatPair(r)
	if err != nil {
		return err
	}
	err = s.audited(r, "pricing.compat_removed", "campaign_compat", a, map[string]int64{"campaign_b": b}, func(q *db.Queries) error {
		_, err := q.RemoveCampaignCompat(r.Context(), db.RemoveCampaignCompatParams{CampaignA: a, CampaignB: b})
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) priceSuggestions(w http.ResponseWriter, r *http.Request) error {
	state, err := s.Store.GetHotelState(r.Context())
	if err != nil {
		return err
	}
	to := state.BusinessDate.AddDate(0, 0, 14)
	rows, err := s.Store.ListAvailability(r.Context(), db.ListAvailabilityParams{StayDate: state.BusinessDate, StayDate_2: to})
	if err != nil {
		return err
	}
	items := make([]pricing.Suggestion, 0, len(rows))
	for _, row := range rows {
		items = append(items, pricing.Suggest(row.StayDate.Format(time.DateOnly), row.TypeCode, int(row.Sold), int(row.Capacity)))
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
