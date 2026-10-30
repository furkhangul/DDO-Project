package pricing

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func testRules() Rules {
	return Rules{
		Rate:             Rate{BaseKurus: 300000, ExtraAdultKurus: 80000, ChildKurus: 40000},
		PlanMultiplierBP: 10000,
		BaseOccupancy:    2,
		MaxOccupancy:     4,
		WeekendBP:        12000,
		ChildFreeMaxAge:  6,
		ChildMaxAge:      12,
		KDVBP:            1000,
		AccommodationBP:  200,
		Seasons: []Season{
			{Name: "Yaz", From: d("2027-06-01"), To: d("2027-09-01"), MultiplierBP: 15000, MinStay: 3},
			{Name: "Kış", From: d("2027-01-01"), To: d("2027-04-01"), MultiplierBP: 8000, MinStay: 1},
		},
		EarlyTiers: []EarlyTier{{MinLeadDays: 30, DiscountBP: 1000}, {MinLeadDays: 90, DiscountBP: 1500}},
		Campaigns: []Campaign{
			{Code: "A10", DiscountBP: 1000, ValidFrom: d("2027-01-01"), ValidTo: d("2028-01-01"), MinNights: 1, WithEarlyBooking: true},
			{Code: "B5", DiscountBP: 500, ValidFrom: d("2027-01-01"), ValidTo: d("2028-01-01"), MinNights: 1, WithEarlyBooking: true},
			{Code: "SOLO", DiscountBP: 2000, ValidFrom: d("2027-01-01"), ValidTo: d("2028-01-01"), MinNights: 1},
			{Code: "SOLO3", DiscountBP: 300, ValidFrom: d("2027-01-01"), ValidTo: d("2028-01-01"), MinNights: 1},
			{Code: "FIX", FixedKurus: 100001, ValidFrom: d("2027-01-01"), ValidTo: d("2028-01-01"), MinNights: 2, WithEarlyBooking: true},
		},
		Compatible: [][2]string{{"A10", "B5"}, {"A10", "FIX"}},
	}
}

func baseInput() Input {
	return Input{CheckIn: d("2027-05-04"), CheckOut: d("2027-05-06"), BookedOn: d("2027-05-01"), Adults: 2}
}

func TestPlainWeekdayStay(t *testing.T) {
	q, err := Calculate(baseInput(), testRules())
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Nights) != 2 || q.NetKurus != 600000 || q.KDVKurus != 60000 || q.AccommodationTax != 12000 || q.TotalKurus != 672000 {
		t.Fatalf("beklenmeyen fiyat: %+v", q)
	}
}

func TestWeekendNightsAreFridayAndSaturday(t *testing.T) {
	in := baseInput()
	in.CheckIn, in.CheckOut = d("2027-05-06"), d("2027-05-10")
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{false, true, true, false}
	for i, n := range q.Nights {
		if n.Weekend != want[i] {
			t.Fatalf("gece %d: hafta sonu=%v", i, n.Weekend)
		}
	}
	if q.Nights[1].RoomKurus != 360000 {
		t.Fatalf("hafta sonu fiyatı: %d", q.Nights[1].RoomKurus)
	}
}

func TestSeasonMultiplierAcrossBoundary(t *testing.T) {
	in := baseInput()
	in.CheckIn, in.CheckOut = d("2027-05-30"), d("2027-06-02")
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if q.Nights[1].Season != "" || q.Nights[2].Season != "Yaz" {
		t.Fatalf("sezon sınırı hatalı: %+v", q.Nights)
	}
	if q.Nights[2].RoomKurus != 450000 {
		t.Fatalf("sezon fiyatı: %d", q.Nights[2].RoomKurus)
	}
}

func TestMinStayEnforced(t *testing.T) {
	in := baseInput()
	in.CheckIn, in.CheckOut = d("2027-07-06"), d("2027-07-08")
	if _, err := Calculate(in, testRules()); !errors.Is(err, ErrMinStay) {
		t.Fatalf("hata bekleniyordu: %v", err)
	}
}

func TestChildAndExtraAdultPricing(t *testing.T) {
	in := baseInput()
	in.Adults = 2
	in.ChildrenAges = []int{5, 10}
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if q.Nights[0].OccupantKurus != 40000 {
		t.Fatalf("çocuk ücreti: %d", q.Nights[0].OccupantKurus)
	}
	in.ChildrenAges = []int{14}
	q, err = Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if q.Nights[0].OccupantKurus != 80000 {
		t.Fatalf("yetişkin sayılan çocuk: %d", q.Nights[0].OccupantKurus)
	}
}

func TestOccupancyLimit(t *testing.T) {
	in := baseInput()
	in.Adults = 3
	in.ChildrenAges = []int{2, 3}
	if _, err := Calculate(in, testRules()); !errors.Is(err, ErrOccupancy) {
		t.Fatalf("hata bekleniyordu: %v", err)
	}
}

func TestEarlyBookingPicksHighestQualifyingTier(t *testing.T) {
	in := baseInput()
	in.BookedOn = d("2027-01-15")
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if q.EarlyBP != 1500 {
		t.Fatalf("erken indirim: %d", q.EarlyBP)
	}
	in.BookedOn = d("2027-03-20")
	q, _ = Calculate(in, testRules())
	if q.EarlyBP != 1000 {
		t.Fatalf("erken indirim: %d", q.EarlyBP)
	}
}

func TestCompatibleCampaignsStackInCodeOrder(t *testing.T) {
	in := baseInput()
	in.Codes = []string{"b5", " a10 "}
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if q.Nights[0].NetKurus != 256500 {
		t.Fatalf("birleşik indirim: %d", q.Nights[0].NetKurus)
	}
	if q.AppliedCodes[0] != "A10" {
		t.Fatalf("sıra: %v", q.AppliedCodes)
	}
}

func TestIncompatibleCampaignsRejected(t *testing.T) {
	in := baseInput()
	in.Codes = []string{"A10", "SOLO"}
	if _, err := Calculate(in, testRules()); !errors.Is(err, ErrCampaignIncompat) {
		t.Fatalf("hata bekleniyordu: %v", err)
	}
}

func TestDuplicateCodeCountsOnce(t *testing.T) {
	in := baseInput()
	in.Codes = []string{"A10", "a10"}
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if len(q.AppliedCodes) != 1 {
		t.Fatalf("kod iki kez uygulandı: %v", q.AppliedCodes)
	}
}

func TestUnknownAndExpiredCampaign(t *testing.T) {
	in := baseInput()
	in.Codes = []string{"YOK"}
	if _, err := Calculate(in, testRules()); !errors.Is(err, ErrUnknownCampaign) {
		t.Fatalf("hata: %v", err)
	}
	in.Codes = []string{"FIX"}
	in.CheckOut = d("2027-05-05")
	if _, err := Calculate(in, testRules()); !errors.Is(err, ErrCampaignNotValid) {
		t.Fatalf("min gece hatası: %v", err)
	}
}

func TestNonCombinableCampaignGivesGuestTheBetterOption(t *testing.T) {
	in := baseInput()
	in.BookedOn = d("2027-01-15")
	in.Codes = []string{"SOLO"}
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if q.EarlyBP != 0 || len(q.AppliedCodes) != 1 {
		t.Fatalf("yüzde 20 kampanya seçilmeliydi: %+v", q)
	}
	in.Codes = []string{"SOLO3"}
	q, err = Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	if q.EarlyBP != 1500 || len(q.AppliedCodes) != 0 {
		t.Fatalf("erken indirim seçilmeliydi: %+v", q)
	}
}

func TestFixedDiscountSpreadPreservesKurus(t *testing.T) {
	in := baseInput()
	in.CheckOut = d("2027-05-07")
	in.Codes = []string{"FIX"}
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	var cut int64
	for _, n := range q.Nights {
		cut += n.CampaignKurus
	}
	if cut != 100001 {
		t.Fatalf("sabit indirim dağıtımı: %d", cut)
	}
}

func TestFixedDiscountCappedAtPrice(t *testing.T) {
	rules := testRules()
	rules.Rate.BaseKurus = 1000
	in := baseInput()
	in.Codes = []string{"FIX"}
	q, err := Calculate(in, rules)
	if err != nil {
		t.Fatal(err)
	}
	if q.NetKurus != 0 || q.TotalKurus != 0 {
		t.Fatalf("eksi fiyat oluştu: %+v", q)
	}
}

func TestTotalsEqualSumOfNights(t *testing.T) {
	in := baseInput()
	in.CheckIn, in.CheckOut = d("2027-06-28"), d("2027-07-05")
	in.BookedOn = d("2027-01-01")
	in.Adults = 3
	in.ChildrenAges = []int{9}
	in.Codes = []string{"A10", "FIX"}
	q, err := Calculate(in, testRules())
	if err != nil {
		t.Fatal(err)
	}
	var net, kdv, accom, total int64
	for _, n := range q.Nights {
		net += n.NetKurus
		kdv += n.KDVKurus
		accom += n.AccommodationTax
		total += n.TotalKurus
	}
	if net != q.NetKurus || kdv != q.KDVKurus || accom != q.AccommodationTax || total != q.TotalKurus {
		t.Fatalf("toplamlar uyuşmuyor: %+v", q)
	}
	if q.GrossKurus-q.DiscountKurus != q.NetKurus {
		t.Fatalf("brüt-indirim=net bozuldu: %+v", q)
	}
}

func TestSnapshotReplayIsDeterministicAfterRuleChange(t *testing.T) {
	in := baseInput()
	in.Codes = []string{"A10"}
	rules := testRules()
	original, err := Calculate(in, rules)
	if err != nil {
		t.Fatal(err)
	}
	rawRules, _ := json.Marshal(rules)
	rawInput, _ := json.Marshal(in)

	rules.Rate.BaseKurus = 999999
	rules.KDVBP = 2000

	var storedRules Rules
	var storedInput Input
	if err := json.Unmarshal(rawRules, &storedRules); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawInput, &storedInput); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		replayed, err := Calculate(storedInput, storedRules)
		if err != nil {
			t.Fatal(err)
		}
		if replayed.Hash != original.Hash || replayed.TotalKurus != original.TotalKurus {
			t.Fatalf("tekrar hesap farklı: %s != %s", replayed.Hash, original.Hash)
		}
	}
}

func TestChangedRulesChangeHash(t *testing.T) {
	rules := testRules()
	a, _ := Calculate(baseInput(), rules)
	rules.Rate.BaseKurus++
	b, _ := Calculate(baseInput(), rules)
	if a.Hash == b.Hash {
		t.Fatal("hash değişmeliydi")
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Input)
		want   error
	}{
		{"ters tarih", func(i *Input) { i.CheckOut = i.CheckIn }, ErrInvalidStay},
		{"yetişkinsiz", func(i *Input) { i.Adults = 0 }, ErrAdultsRequired},
		{"yaş", func(i *Input) { i.ChildrenAges = []int{19} }, ErrChildAge},
		{"geçmiş", func(i *Input) { i.BookedOn = i.CheckIn.AddDate(0, 0, 1) }, ErrBookingAfterArrive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := baseInput()
			c.mutate(&in)
			if _, err := Calculate(in, testRules()); !errors.Is(err, c.want) {
				t.Fatalf("hata: %v", err)
			}
		})
	}
}

func FuzzTotalsNeverDrift(f *testing.F) {
	f.Add(int64(123456), uint8(5), int64(1000))
	f.Fuzz(func(t *testing.T, base int64, nights uint8, fixed int64) {
		if base <= 0 || base > 1e10 || fixed < 0 || fixed > 1e10 {
			return
		}
		rules := testRules()
		rules.Rate.BaseKurus = base
		rules.Campaigns[4].FixedKurus = fixed
		rules.Seasons = nil
		in := baseInput()
		in.CheckOut = in.CheckIn.AddDate(0, 0, int(nights%30)+1)
		in.Codes = nil
		if nights%30 >= 1 && fixed > 0 {
			in.Codes = []string{"FIX"}
		}
		q, err := Calculate(in, rules)
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, n := range q.Nights {
			if n.NetKurus < 0 {
				t.Fatalf("eksi gece: %+v", n)
			}
			sum += n.TotalKurus
		}
		if sum != q.TotalKurus {
			t.Fatalf("%d != %d", sum, q.TotalKurus)
		}
	})
}
