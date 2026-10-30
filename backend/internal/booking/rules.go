package booking

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
	"konakpro/internal/pricing"
)

func LoadRules(ctx context.Context, q *db.Queries, roomTypeID, planID int64, from, to time.Time, codes []string) (pricing.Rules, error) {
	roomType, err := q.GetRoomType(ctx, roomTypeID)
	if err != nil {
		return pricing.Rules{}, notFound(err, "Oda tipi bulunamadı")
	}
	plan, err := q.GetRatePlan(ctx, planID)
	if err != nil {
		return pricing.Rules{}, notFound(err, "Tarife bulunamadı")
	}
	rate, err := q.GetRoomRate(ctx, db.GetRoomRateParams{RoomTypeID: roomTypeID, RatePlanID: planID})
	if err != nil {
		return pricing.Rules{}, notFound(err, "Bu oda tipi için tarife tanımlı değil")
	}
	settings, err := q.GetPricingSettings(ctx)
	if err != nil {
		return pricing.Rules{}, err
	}
	rules := pricing.Rules{
		Rate:             pricing.Rate{BaseKurus: rate.BaseKurus, ExtraAdultKurus: rate.ExtraAdultKurus, ChildKurus: rate.ChildKurus},
		PlanMultiplierBP: int64(plan.MultiplierBp),
		BaseOccupancy:    int(roomType.BaseOccupancy),
		MaxOccupancy:     int(roomType.MaxOccupancy),
		WeekendBP:        int64(settings.WeekendBp),
		ChildFreeMaxAge:  int(settings.ChildFreeMaxAge),
		ChildMaxAge:      int(settings.ChildMaxAge),
		KDVBP:            int64(settings.KdvBp),
		AccommodationBP:  int64(settings.AccommodationBp),
		Seasons:          []pricing.Season{},
		EarlyTiers:       []pricing.EarlyTier{},
		Campaigns:        []pricing.Campaign{},
		Compatible:       [][2]string{},
	}
	seasons, err := q.ListSeasons(ctx)
	if err != nil {
		return pricing.Rules{}, err
	}
	for _, s := range seasons {
		if s.DateFrom.Before(to) && s.DateTo.After(from) {
			rules.Seasons = append(rules.Seasons, pricing.Season{
				Name: s.Name, From: s.DateFrom, To: s.DateTo, MultiplierBP: int64(s.MultiplierBp), MinStay: int(s.MinStay),
			})
		}
	}
	tiers, err := q.ListEarlyTiers(ctx)
	if err != nil {
		return pricing.Rules{}, err
	}
	for _, t := range tiers {
		rules.EarlyTiers = append(rules.EarlyTiers, pricing.EarlyTier{MinLeadDays: int(t.MinLeadDays), DiscountBP: int64(t.DiscountBp)})
	}
	if len(codes) == 0 {
		return rules, nil
	}
	wanted := map[string]bool{}
	for _, code := range codes {
		wanted[strings.ToUpper(strings.TrimSpace(code))] = true
	}
	campaigns, err := q.ListCampaigns(ctx)
	if err != nil {
		return pricing.Rules{}, err
	}
	for _, c := range campaigns {
		if !wanted[c.Code] || !c.Active {
			continue
		}
		if c.MaxUses != nil && c.UsedCount >= *c.MaxUses {
			continue
		}
		rules.Campaigns = append(rules.Campaigns, pricing.Campaign{
			Code: c.Code, DiscountBP: int64(c.DiscountBp), FixedKurus: c.DiscountFixedKurus,
			ValidFrom: c.ValidFrom, ValidTo: c.ValidTo, MinNights: int(c.MinNights), WithEarlyBooking: c.WithEarlyBooking,
		})
	}
	compat, err := q.ListCampaignCompat(ctx)
	if err != nil {
		return pricing.Rules{}, err
	}
	for _, pair := range compat {
		if wanted[pair.CodeA] && wanted[pair.CodeB] {
			rules.Compatible = append(rules.Compatible, [2]string{pair.CodeA, pair.CodeB})
		}
	}
	return rules, nil
}

func priceError(err error) error {
	for _, known := range []struct {
		target error
		code   string
	}{
		{pricing.ErrMinStay, "MIN_STAY"},
		{pricing.ErrOccupancy, "OCCUPANCY_EXCEEDED"},
		{pricing.ErrUnknownCampaign, "CAMPAIGN_UNKNOWN"},
		{pricing.ErrCampaignNotValid, "CAMPAIGN_NOT_VALID"},
		{pricing.ErrCampaignIncompat, "CAMPAIGN_INCOMPATIBLE"},
		{pricing.ErrInvalidStay, "INVALID_STAY"},
		{pricing.ErrChildAge, "INVALID_CHILD_AGE"},
		{pricing.ErrAdultsRequired, "ADULTS_REQUIRED"},
		{pricing.ErrBookingAfterArrive, "ARRIVAL_IN_PAST"},
	} {
		if errors.Is(err, known.target) {
			return apperr.New(http.StatusUnprocessableEntity, known.code, err.Error())
		}
	}
	return err
}

func notFound(err error, message string) error {
	if store.IsNotFound(err) {
		return apperr.NotFound(message)
	}
	return err
}
