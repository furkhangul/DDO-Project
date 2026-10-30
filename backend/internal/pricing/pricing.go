package pricing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const bpScale = 10000

var (
	ErrInvalidStay        = errors.New("geçersiz konaklama aralığı")
	ErrOccupancy          = errors.New("kişi sayısı oda kapasitesini aşıyor")
	ErrMinStay            = errors.New("minimum konaklama süresi karşılanmıyor")
	ErrUnknownCampaign    = errors.New("kampanya kodu geçersiz")
	ErrCampaignNotValid   = errors.New("kampanya bu konaklama için geçerli değil")
	ErrCampaignIncompat   = errors.New("kampanya kodları birlikte kullanılamaz")
	ErrChildAge           = errors.New("çocuk yaşı geçersiz")
	ErrAdultsRequired     = errors.New("en az bir yetişkin gerekli")
	ErrBookingAfterArrive = errors.New("rezervasyon tarihi girişten sonra olamaz")
)

type Rate struct {
	BaseKurus       int64 `json:"base_kurus"`
	ExtraAdultKurus int64 `json:"extra_adult_kurus"`
	ChildKurus      int64 `json:"child_kurus"`
}

type Season struct {
	Name         string    `json:"name"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	MultiplierBP int64     `json:"multiplier_bp"`
	MinStay      int       `json:"min_stay"`
}

type EarlyTier struct {
	MinLeadDays int   `json:"min_lead_days"`
	DiscountBP  int64 `json:"discount_bp"`
}

type Campaign struct {
	Code             string    `json:"code"`
	DiscountBP       int64     `json:"discount_bp"`
	FixedKurus       int64     `json:"fixed_kurus"`
	ValidFrom        time.Time `json:"valid_from"`
	ValidTo          time.Time `json:"valid_to"`
	MinNights        int       `json:"min_nights"`
	WithEarlyBooking bool      `json:"with_early_booking"`
}

type Rules struct {
	Rate             Rate        `json:"rate"`
	PlanMultiplierBP int64       `json:"plan_multiplier_bp"`
	BaseOccupancy    int         `json:"base_occupancy"`
	MaxOccupancy     int         `json:"max_occupancy"`
	WeekendBP        int64       `json:"weekend_bp"`
	ChildFreeMaxAge  int         `json:"child_free_max_age"`
	ChildMaxAge      int         `json:"child_max_age"`
	KDVBP            int64       `json:"kdv_bp"`
	AccommodationBP  int64       `json:"accommodation_bp"`
	Seasons          []Season    `json:"seasons"`
	EarlyTiers       []EarlyTier `json:"early_tiers"`
	Campaigns        []Campaign  `json:"campaigns"`
	Compatible       [][2]string `json:"compatible"`
}

type Input struct {
	CheckIn      time.Time `json:"check_in"`
	CheckOut     time.Time `json:"check_out"`
	BookedOn     time.Time `json:"booked_on"`
	Adults       int       `json:"adults"`
	ChildrenAges []int     `json:"children_ages"`
	Codes        []string  `json:"codes"`
}

type Night struct {
	Date             time.Time `json:"date"`
	Season           string    `json:"season"`
	SeasonBP         int64     `json:"season_bp"`
	Weekend          bool      `json:"weekend"`
	RoomKurus        int64     `json:"room_kurus"`
	OccupantKurus    int64     `json:"occupant_kurus"`
	GrossKurus       int64     `json:"gross_kurus"`
	EarlyKurus       int64     `json:"early_discount_kurus"`
	CampaignKurus    int64     `json:"campaign_discount_kurus"`
	NetKurus         int64     `json:"net_kurus"`
	KDVKurus         int64     `json:"kdv_kurus"`
	AccommodationTax int64     `json:"accommodation_tax_kurus"`
	TotalKurus       int64     `json:"total_kurus"`
}

type Quote struct {
	Nights           []Night  `json:"nights"`
	GrossKurus       int64    `json:"gross_kurus"`
	DiscountKurus    int64    `json:"discount_kurus"`
	NetKurus         int64    `json:"net_kurus"`
	KDVKurus         int64    `json:"kdv_kurus"`
	AccommodationTax int64    `json:"accommodation_tax_kurus"`
	TotalKurus       int64    `json:"total_kurus"`
	EarlyBP          int64    `json:"early_discount_bp"`
	AppliedCodes     []string `json:"applied_codes"`
	Notes            []string `json:"notes"`
	Hash             string   `json:"hash"`
}

func Calculate(in Input, rules Rules) (Quote, error) {
	nights := int(in.CheckOut.Sub(in.CheckIn).Hours() / 24)
	if nights < 1 {
		return Quote{}, ErrInvalidStay
	}
	if in.BookedOn.After(in.CheckIn) {
		return Quote{}, ErrBookingAfterArrive
	}
	if in.Adults < 1 {
		return Quote{}, ErrAdultsRequired
	}
	for _, age := range in.ChildrenAges {
		if age < 0 || age > 17 {
			return Quote{}, ErrChildAge
		}
	}
	if in.Adults+len(in.ChildrenAges) > rules.MaxOccupancy {
		return Quote{}, ErrOccupancy
	}
	if required := requiredMinStay(in, rules); nights < required {
		return Quote{}, fmt.Errorf("%w: en az %d gece", ErrMinStay, required)
	}
	campaigns, err := resolveCampaigns(in, rules, nights)
	if err != nil {
		return Quote{}, err
	}
	earlyBP := earlyDiscount(in, rules)

	var best Quote
	switch {
	case earlyBP == 0 || len(campaigns) == 0 || allowEarly(campaigns):
		best = evaluate(in, rules, earlyBP, campaigns)
	default:
		withEarly := evaluate(in, rules, earlyBP, nil)
		withCodes := evaluate(in, rules, 0, campaigns)
		if withCodes.NetKurus <= withEarly.NetKurus {
			best = withCodes
			best.Notes = append(best.Notes, "Kampanya erken rezervasyonla birleşemediği için kampanya uygulandı")
		} else {
			best = withEarly
			best.Notes = append(best.Notes, "Erken rezervasyon indirimi kampanyadan avantajlı olduğu için uygulandı")
		}
	}
	best.Hash = fingerprint(in, rules, best)
	return best, nil
}

func requiredMinStay(in Input, rules Rules) int {
	required := 1
	for day := in.CheckIn; day.Before(in.CheckOut); day = day.AddDate(0, 0, 1) {
		if s := seasonFor(day, rules.Seasons); s != nil && s.MinStay > required {
			required = s.MinStay
		}
	}
	return required
}

func resolveCampaigns(in Input, rules Rules, nights int) ([]Campaign, error) {
	codes := make([]string, 0, len(in.Codes))
	for _, code := range in.Codes {
		normalized := strings.ToUpper(strings.TrimSpace(code))
		if normalized != "" && !slices.Contains(codes, normalized) {
			codes = append(codes, normalized)
		}
	}
	slices.Sort(codes)

	resolved := make([]Campaign, 0, len(codes))
	for _, code := range codes {
		index := slices.IndexFunc(rules.Campaigns, func(c Campaign) bool { return c.Code == code })
		if index < 0 {
			return nil, fmt.Errorf("%w: %s", ErrUnknownCampaign, code)
		}
		c := rules.Campaigns[index]
		if in.CheckIn.Before(c.ValidFrom) || in.CheckOut.After(c.ValidTo) || nights < c.MinNights {
			return nil, fmt.Errorf("%w: %s", ErrCampaignNotValid, code)
		}
		resolved = append(resolved, c)
	}
	for i := range resolved {
		for j := i + 1; j < len(resolved); j++ {
			if !compatible(rules.Compatible, resolved[i].Code, resolved[j].Code) {
				return nil, fmt.Errorf("%w: %s + %s", ErrCampaignIncompat, resolved[i].Code, resolved[j].Code)
			}
		}
	}
	return resolved, nil
}

func compatible(pairs [][2]string, a, b string) bool {
	return slices.ContainsFunc(pairs, func(p [2]string) bool {
		return (p[0] == a && p[1] == b) || (p[0] == b && p[1] == a)
	})
}

func allowEarly(campaigns []Campaign) bool {
	return !slices.ContainsFunc(campaigns, func(c Campaign) bool { return !c.WithEarlyBooking })
}

func earlyDiscount(in Input, rules Rules) int64 {
	lead := int(in.CheckIn.Sub(in.BookedOn).Hours() / 24)
	var bestLead int
	var discount int64
	for _, tier := range rules.EarlyTiers {
		if lead >= tier.MinLeadDays && tier.MinLeadDays > bestLead {
			bestLead, discount = tier.MinLeadDays, tier.DiscountBP
		}
	}
	return discount
}

func seasonFor(day time.Time, seasons []Season) *Season {
	for i := range seasons {
		if !day.Before(seasons[i].From) && day.Before(seasons[i].To) {
			return &seasons[i]
		}
	}
	return nil
}

func occupantCharge(in Input, rules Rules) int64 {
	var total int64
	adultSlots := in.Adults
	for _, age := range in.ChildrenAges {
		switch {
		case age <= rules.ChildFreeMaxAge:
		case age <= rules.ChildMaxAge:
			total += rules.Rate.ChildKurus
		default:
			adultSlots++
		}
	}
	if extra := adultSlots - rules.BaseOccupancy; extra > 0 {
		total += int64(extra) * rules.Rate.ExtraAdultKurus
	}
	return total
}

func evaluate(in Input, rules Rules, earlyBP int64, campaigns []Campaign) Quote {
	occupant := occupantCharge(in, rules)
	lines := make([]Night, 0, 8)
	for day := in.CheckIn; day.Before(in.CheckOut); day = day.AddDate(0, 0, 1) {
		night := Night{Date: day, SeasonBP: bpScale, Weekend: isWeekendNight(day)}
		room := applyBP(rules.Rate.BaseKurus, rules.PlanMultiplierBP)
		if s := seasonFor(day, rules.Seasons); s != nil {
			night.Season, night.SeasonBP = s.Name, s.MultiplierBP
			room = applyBP(room, s.MultiplierBP)
		}
		if night.Weekend {
			room = applyBP(room, rules.WeekendBP)
		}
		night.RoomKurus = room
		night.OccupantKurus = occupant
		night.GrossKurus = room + occupant
		night.EarlyKurus = applyBP(night.GrossKurus, earlyBP)
		night.NetKurus = night.GrossKurus - night.EarlyKurus
		lines = append(lines, night)
	}

	applied := make([]string, 0, len(campaigns))
	for _, c := range campaigns {
		applied = append(applied, c.Code)
		if c.DiscountBP > 0 {
			for i := range lines {
				cut := applyBP(lines[i].NetKurus, c.DiscountBP)
				lines[i].NetKurus -= cut
				lines[i].CampaignKurus += cut
			}
			continue
		}
		spreadFixed(lines, c.FixedKurus)
	}

	quote := Quote{Nights: lines, EarlyBP: earlyBP, AppliedCodes: applied, Notes: []string{}}
	for i := range quote.Nights {
		n := &quote.Nights[i]
		n.KDVKurus = applyBP(n.NetKurus, rules.KDVBP)
		n.AccommodationTax = applyBP(n.NetKurus, rules.AccommodationBP)
		n.TotalKurus = n.NetKurus + n.KDVKurus + n.AccommodationTax
		quote.GrossKurus += n.GrossKurus
		quote.DiscountKurus += n.EarlyKurus + n.CampaignKurus
		quote.NetKurus += n.NetKurus
		quote.KDVKurus += n.KDVKurus
		quote.AccommodationTax += n.AccommodationTax
		quote.TotalKurus += n.TotalKurus
	}
	return quote
}

func spreadFixed(lines []Night, fixed int64) {
	var base int64
	for _, n := range lines {
		base += n.NetKurus
	}
	if base == 0 {
		return
	}
	if fixed > base {
		fixed = base
	}
	remaining := fixed
	shares := make([]int64, len(lines))
	for i, n := range lines {
		shares[i] = fixed * n.NetKurus / base
		remaining -= shares[i]
	}
	for i := 0; remaining > 0; i = (i + 1) % len(lines) {
		if shares[i] < lines[i].NetKurus {
			shares[i]++
			remaining--
		}
	}
	for i := range lines {
		lines[i].NetKurus -= shares[i]
		lines[i].CampaignKurus += shares[i]
	}
}

func isWeekendNight(day time.Time) bool {
	return day.Weekday() == time.Friday || day.Weekday() == time.Saturday
}

func applyBP(amount, bp int64) int64 {
	return (amount*bp + bpScale/2) / bpScale
}

func fingerprint(in Input, rules Rules, q Quote) string {
	payload, err := json.Marshal(struct {
		Input Input
		Rules Rules
		Lines []Night
	}{in, rules, q.Nights})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
