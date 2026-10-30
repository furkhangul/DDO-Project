package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"konakpro/internal/channel"
	"konakpro/internal/seed"
)

const api = "/api/v1"

type suite struct {
	api       *client
	simulator *client
	secret    string
	run       string
	tokens    map[string]string
	refresh   string
	typeID    int64
	planID    int64
	roomIDs   []int64
	resID     int64
	folioID   int64
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func newSuite() *suite {
	return &suite{
		api:       newClient(env("API_URL", "http://localhost:8080")),
		simulator: newClient(env("SIMULATOR_URL", "http://localhost:9090")),
		secret:    env("CHANNEL_SECRET", ""),
		run:       strconv.FormatInt(time.Now().Unix()%1000000, 36),
		tokens:    map[string]string{},
	}
}

func (s *suite) login() error {
	for role, email := range map[string]string{
		"reception": "resepsiyon@konakpro.test", "reservations": "rezervasyon@konakpro.test", "housekeeping": "kathizmeti@konakpro.test",
		"revenue": "gelir@konakpro.test", "manager": "mudur@konakpro.test", "guest": "misafir@konakpro.test",
	} {
		var body struct {
			Tokens struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			} `json:"tokens"`
		}
		if err := s.api.expect(http.StatusOK, http.MethodPost, api+"/auth/login", "", map[string]string{"email": email, "password": seed.DemoPassword}, &body); err != nil {
			return err
		}
		s.tokens[role] = body.Tokens.AccessToken
		if role == "manager" {
			s.refresh = body.Tokens.RefreshToken
		}
	}
	return nil
}

func (s *suite) businessDate() (time.Time, error) {
	var body struct {
		BusinessDate string `json:"business_date"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, "/health", "", nil, &body); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.DateOnly, body.BusinessDate)
}

func day(t time.Time) string { return t.Format(time.DateOnly) }

func (s *suite) booking(from, to time.Time, name string) map[string]any {
	return map[string]any{
		"room_type_id": s.typeID, "rate_plan_id": s.planID, "check_in": day(from), "check_out": day(to), "adults": 2,
		"guest": map[string]string{"full_name": name},
	}
}

func (s *suite) book(from, to time.Time) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := s.api.expect(http.StatusCreated, http.MethodPost, api+"/reservations", s.tokens["reservations"], s.booking(from, to, "Doğrulama Misafiri"), &out)
	return out.ID, err
}

var checks = []check{
	{"HEALTH", "GET /health veritabanı durumunu döner", (*suite).checkHealth},
	{"AUTH-1", "Yanlış parola 401 ile reddedilir", (*suite).checkWrongPassword},
	{"AUTH-2", "Oturumsuz istek 401 ile reddedilir", (*suite).checkAnonymous},
	{"AUTH-3", "Refresh token döner, tekrar kullanımı oturumu kapatır", (*suite).checkRefresh},
	{"RBAC-1", "Kat hizmetleri raporları, resepsiyon denetimi çalıştıramaz", (*suite).checkRoleLimits},
	{"RBAC-2", "Misafir başkasının rezervasyonunu göremez", (*suite).checkOwnership},
	{"F-1", "Oda tipi, oda, kat, manzara, bitişiklik yönetimi", (*suite).checkRooms},
	{"F-2", "Rezervasyon, müsaitlik ve takvim", (*suite).checkReservations},
	{"F-3", "Sezon, tarife ve kampanya tanımları", (*suite).checkPricingSetup},
	{"F-4", "Check-in, oda tahsisi, check-out", (*suite).checkFrontDesk},
	{"F-5", "Folyo: harcama, ödeme, bölme", (*suite).checkFolio},
	{"F-6", "Kat hizmetleri görevleri odayı temizler", (*suite).checkHousekeeping},
	{"F-8", "Doluluk, ADR, RevPAR raporları", (*suite).checkReports},
	{"T-1", "300 eşzamanlı rezervasyon, overbooking ve walk", (*suite).checkConcurrency},
	{"T-2", "Fiyat motoru kuralları ve fiyat garantisi", (*suite).checkPriceEngine},
	{"T-3", "120 oda / 400+ rezervasyon tahsisi 10 sn altında", (*suite).checkSolver},
	{"T-5", "Kanal: tekrar, sırasız olay, kuyruk ve ölü mektup", (*suite).checkChannel},
	{"IDEMP", "Idempotency-Key aynı sonucu döndürür", (*suite).checkIdempotency},
	{"API", "Hata biçimi, keyset sayfalama, denetim kaydı, metrikler", (*suite).checkAPIContract},
	{"T-4", "Gece denetimi çökme sonrası çift ücret yazmadan sürer", (*suite).checkNightAudit},
	{"F-7", "Gece denetimi otel gününü ilerletir", (*suite).checkAuditAdvanced},
}

func (s *suite) checkHealth() error {
	var body map[string]string
	if err := s.api.expect(http.StatusOK, http.MethodGet, "/health", "", nil, &body); err != nil {
		return err
	}
	if body["database"] != "ok" {
		return fmt.Errorf("veritabanı durumu: %v", body)
	}
	return nil
}

func (s *suite) checkWrongPassword() error {
	return s.api.expect(http.StatusUnauthorized, http.MethodPost, api+"/auth/login", "", map[string]string{"email": "mudur@konakpro.test", "password": "yanlis"}, nil)
}

func (s *suite) checkAnonymous() error {
	for _, path := range []string{"/reservations", "/dashboard", "/reports/kpi?from=2026-01-01&to=2026-02-01", "/audit-log"} {
		if err := s.api.expect(http.StatusUnauthorized, http.MethodGet, api+path, "", nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *suite) checkRefresh() error {
	var first struct {
		Tokens struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodPost, api+"/auth/refresh", "", map[string]string{"refresh_token": s.refresh}, &first); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusUnauthorized, http.MethodPost, api+"/auth/refresh", "", map[string]string{"refresh_token": s.refresh}, nil); err != nil {
		return fmt.Errorf("eski token kabul edildi: %w", err)
	}
	return s.api.expect(http.StatusUnauthorized, http.MethodPost, api+"/auth/refresh", "", map[string]string{"refresh_token": first.Tokens.RefreshToken}, nil)
}

func (s *suite) checkRoleLimits() error {
	limits := []struct {
		role, method, path string
	}{
		{"housekeeping", http.MethodGet, "/reports/kpi?from=2026-01-01&to=2026-02-01"},
		{"housekeeping", http.MethodGet, "/reservations"},
		{"reception", http.MethodPost, "/night-audit/run"},
		{"reservations", http.MethodGet, "/audit-log"},
		{"guest", http.MethodGet, "/dashboard"},
		{"guest", http.MethodGet, "/rooms"},
		{"revenue", http.MethodPost, "/rooms"},
	}
	for _, limit := range limits {
		if err := s.api.expect(http.StatusForbidden, limit.method, api+limit.path, s.tokens[limit.role], nil, nil); err != nil {
			return fmt.Errorf("%s: %w", limit.role, err)
		}
	}
	return nil
}

func (s *suite) checkOwnership() error {
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/reservations?limit=20", s.tokens["manager"], nil, &list); err != nil {
		return err
	}
	var own struct {
		Items []struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		} `json:"items"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/reservations", s.tokens["guest"], nil, &own); err != nil {
		return err
	}
	mine := map[int64]bool{}
	for _, item := range own.Items {
		mine[item.ID] = true
	}
	if len(mine) == 0 {
		return fmt.Errorf("misafirin kendi rezervasyonu listelenmedi")
	}
	for _, item := range list.Items {
		if !mine[item.ID] {
			return s.api.expect(http.StatusForbidden, http.MethodGet, fmt.Sprintf("%s/reservations/%d", api, item.ID), s.tokens["guest"], nil, nil)
		}
	}
	return fmt.Errorf("karşılaştırılacak başka rezervasyon bulunamadı")
}

func (s *suite) checkRooms() error {
	token := s.tokens["manager"]
	var created struct {
		ID int64 `json:"id"`
	}
	code := "V" + s.run
	if len(code) > 16 {
		code = code[:16]
	}
	err := s.api.expect(http.StatusCreated, http.MethodPost, api+"/room-types", token, map[string]any{
		"code": upper(code), "name": "Doğrulama " + s.run, "base_occupancy": 2, "max_occupancy": 4, "overbook_pct": 10,
	}, &created)
	if err != nil {
		return err
	}
	s.typeID = created.ID
	for i := 1; i <= 10; i++ {
		var room struct {
			ID int64 `json:"id"`
		}
		if err := s.api.expect(http.StatusCreated, http.MethodPost, api+"/rooms", token, map[string]any{
			"number": fmt.Sprintf("%s-%02d", s.run, i), "room_type_id": s.typeID, "floor": 7, "view": "deniz",
		}, &room); err != nil {
			return err
		}
		s.roomIDs = append(s.roomIDs, room.ID)
	}
	if err := s.api.expect(http.StatusCreated, http.MethodPost, api+"/rooms/adjacency", token, map[string]any{"room_a": s.roomIDs[0], "room_b": s.roomIDs[1]}, nil); err != nil {
		return err
	}
	var rooms struct {
		Items []struct {
			ID    int64  `json:"id"`
			Floor int32  `json:"floor"`
			View  string `json:"view"`
		} `json:"items"`
		Adjacency []struct {
			RoomA int64 `json:"room_a"`
		} `json:"adjacency"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/rooms", token, nil, &rooms); err != nil {
		return err
	}
	if len(rooms.Items) < 130 || len(rooms.Adjacency) == 0 {
		return fmt.Errorf("oda=%d bitişik=%d", len(rooms.Items), len(rooms.Adjacency))
	}
	var plans struct {
		Items []struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		} `json:"items"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/rate-plans", token, nil, &plans); err != nil {
		return err
	}
	s.planID = plans.Items[0].ID
	return s.api.expect(http.StatusOK, http.MethodPut, api+"/pricing/rates", s.tokens["revenue"], map[string]any{
		"room_type_id": s.typeID, "rate_plan_id": s.planID, "base_kurus": 300000, "extra_adult_kurus": 80000, "child_kurus": 40000,
	}, nil)
}

func upper(value string) string {
	out := []byte(value)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}

func (s *suite) checkReservations() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	from := today.AddDate(0, 0, 40)
	id, err := s.book(from, from.AddDate(0, 0, 2))
	if err != nil {
		return err
	}
	var availability struct {
		Items []struct {
			TypeID int64 `json:"room_type_id"`
			Sold   int32 `json:"sold"`
		} `json:"items"`
	}
	path := fmt.Sprintf("%s/availability?from=%s&to=%s", api, day(from), day(from.AddDate(0, 0, 1)))
	if err := s.api.expect(http.StatusOK, http.MethodGet, path, s.tokens["guest"], nil, &availability); err != nil {
		return err
	}
	found := false
	for _, item := range availability.Items {
		found = found || (item.TypeID == s.typeID && item.Sold == 1)
	}
	if !found {
		return fmt.Errorf("müsaitlik satışı yansıtmadı")
	}
	calendar := fmt.Sprintf("%s/calendar?from=%s&to=%s", api, day(today), day(today.AddDate(0, 0, 14)))
	var board struct {
		Rooms    []json.RawMessage `json:"rooms"`
		Segments []json.RawMessage `json:"segments"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, calendar, s.tokens["reception"], nil, &board); err != nil {
		return err
	}
	if len(board.Rooms) == 0 || len(board.Segments) == 0 {
		return fmt.Errorf("takvim boş: oda=%d segment=%d", len(board.Rooms), len(board.Segments))
	}
	return s.api.expect(http.StatusOK, http.MethodPost, fmt.Sprintf("%s/reservations/%d/cancel", api, id), s.tokens["reservations"], nil, nil)
}

func (s *suite) checkPricingSetup() error {
	var overview struct {
		Seasons   []json.RawMessage `json:"seasons"`
		Plans     []json.RawMessage `json:"plans"`
		Campaigns []json.RawMessage `json:"campaigns"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/pricing", s.tokens["revenue"], nil, &overview); err != nil {
		return err
	}
	if len(overview.Seasons) == 0 || len(overview.Plans) == 0 || len(overview.Campaigns) == 0 {
		return fmt.Errorf("fiyat tanımları eksik")
	}
	var created struct {
		ID int64 `json:"id"`
	}
	err := s.api.expect(http.StatusCreated, http.MethodPost, api+"/pricing/campaigns", s.tokens["revenue"], map[string]any{
		"code": upper("D" + s.run), "name": "Doğrulama", "discount_bp": 500, "valid_from": "2026-01-01", "valid_to": "2030-01-01",
	}, &created)
	if err != nil {
		return err
	}
	return s.api.expect(http.StatusOK, http.MethodPut, fmt.Sprintf("%s/pricing/campaigns/%d/active", api, created.ID), s.tokens["revenue"], map[string]bool{"active": false}, nil)
}

func (s *suite) checkFrontDesk() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	id, err := s.book(today, today.AddDate(0, 0, 2))
	if err != nil {
		return err
	}
	early, err := s.book(today.AddDate(0, 0, 5), today.AddDate(0, 0, 6))
	if err != nil {
		return err
	}
	if err := s.api.expect(http.StatusConflict, http.MethodPost, fmt.Sprintf("%s/reservations/%d/check-in", api, early), s.tokens["reception"], nil, nil); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusOK, http.MethodPost, fmt.Sprintf("%s/reservations/%d/check-in", api, id), s.tokens["reception"], nil, nil); err != nil {
		return err
	}
	var detail struct {
		Status      string `json:"status"`
		Assignments []struct {
			RoomID int64 `json:"room_id"`
		} `json:"assignments"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("%s/reservations/%d", api, id), s.tokens["reception"], nil, &detail); err != nil {
		return err
	}
	if detail.Status != "checked_in" || len(detail.Assignments) != 1 {
		return fmt.Errorf("giriş sonrası durum: %+v", detail)
	}
	s.resID = id

	second, err := s.book(today, today.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	if err := s.api.expect(http.StatusOK, http.MethodPost, fmt.Sprintf("%s/reservations/%d/check-in", api, second), s.tokens["reception"], nil, nil); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusConflict, http.MethodPost, fmt.Sprintf("%s/reservations/%d/check-out", api, second), s.tokens["reception"], nil, nil); err != nil {
		return fmt.Errorf("aynı gün çıkış reddedilmedi: %w", err)
	}
	return nil
}

func (s *suite) checkFolio() error {
	token := s.tokens["reception"]
	var list struct {
		Items []struct {
			Folio struct {
				ID int64 `json:"id"`
			} `json:"folio"`
		} `json:"items"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("%s/reservations/%d/folios", api, s.resID), token, nil, &list); err != nil {
		return err
	}
	s.folioID = list.Items[0].Folio.ID
	base := fmt.Sprintf("%s/folios/%d", api, s.folioID)
	if err := s.api.expect(http.StatusCreated, http.MethodPost, base+"/charges", token, map[string]any{"amount_kurus": 150000, "description": "Spa"}, nil); err != nil {
		return err
	}
	var payment struct {
		ID int64 `json:"id"`
	}
	if err := s.api.expect(http.StatusCreated, http.MethodPost, base+"/payments", token, map[string]any{"amount_kurus": 50000, "method": "kart"}, &payment); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusConflict, http.MethodPost, base+"/refunds", token, map[string]any{"payment_line_id": payment.ID, "amount_kurus": 60000}, nil); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusCreated, http.MethodPost, base+"/refunds", token, map[string]any{"payment_line_id": payment.ID, "amount_kurus": 10000}, nil); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusCreated, http.MethodPost, base+"/split", token, map[string]any{"payer_type": "company", "payer_name": "Doğrulama A.Ş.", "percent": 50}, nil); err != nil {
		return err
	}
	var integrity struct {
		Balanced bool `json:"balanced"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, base+"/integrity", token, nil, &integrity); err != nil {
		return err
	}
	if !integrity.Balanced {
		return fmt.Errorf("folyo toplamı kalemlerle eşleşmedi")
	}
	return nil
}

func (s *suite) checkHousekeeping() error {
	token := s.tokens["housekeeping"]
	room := s.roomIDs[9]
	if err := s.api.expect(http.StatusOK, http.MethodPut, fmt.Sprintf("%s/rooms/%d/status", api, room), token, map[string]string{"status": "dirty"}, nil); err != nil {
		return err
	}
	var task struct {
		ID int64 `json:"id"`
	}
	if err := s.api.expect(http.StatusCreated, http.MethodPost, api+"/housekeeping/tasks", token, map[string]any{"room_id": room, "task_type": "cikis_temizligi"}, &task); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusOK, http.MethodPut, fmt.Sprintf("%s/housekeeping/tasks/%d", api, task.ID), token, map[string]string{"status": "tamamlandi"}, nil); err != nil {
		return err
	}
	var rooms struct {
		Items []struct {
			ID       int64  `json:"id"`
			HkStatus string `json:"hk_status"`
		} `json:"items"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/rooms", token, nil, &rooms); err != nil {
		return err
	}
	for _, item := range rooms.Items {
		if item.ID == room {
			if item.HkStatus != "clean" {
				return fmt.Errorf("oda temizlenmedi: %s", item.HkStatus)
			}
			return nil
		}
	}
	return fmt.Errorf("oda bulunamadı")
}

func (s *suite) checkReports() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	var report struct {
		Points  []json.RawMessage `json:"points"`
		Summary struct {
			OccupancyPct float64 `json:"occupancy_pct"`
			ADRKurus     int64   `json:"adr_kurus"`
			RevPARKurus  int64   `json:"revpar_kurus"`
			RoomsSold    int64   `json:"rooms_sold"`
		} `json:"summary"`
	}
	path := fmt.Sprintf("%s/reports/kpi?from=%s&to=%s&bucket=month", api, day(today.AddDate(-3, 0, 0)), day(today))
	if err := s.api.expect(http.StatusOK, http.MethodGet, path, s.tokens["revenue"], nil, &report); err != nil {
		return err
	}
	if len(report.Points) < 35 || report.Summary.OccupancyPct <= 0 || report.Summary.ADRKurus <= 0 || report.Summary.RevPARKurus <= 0 {
		return fmt.Errorf("rapor eksik: %d nokta, %+v", len(report.Points), report.Summary)
	}
	if report.Summary.RevPARKurus >= report.Summary.ADRKurus {
		return fmt.Errorf("RevPAR ADR'yi geçemez: %+v", report.Summary)
	}
	return nil
}

func (s *suite) checkConcurrency() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	from := today.AddDate(0, 0, 60)
	to := from.AddDate(0, 0, 3)
	const attempts = 300
	var created, soldOut atomic.Int64
	var failure atomic.Value
	var ids sync.Map
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			result, err := s.api.post(api+"/reservations", s.tokens["reservations"], s.booking(from, to, fmt.Sprintf("Yük %d", i)))
			switch {
			case err != nil:
				failure.Store(err.Error())
			case result.Status == http.StatusCreated:
				var out struct {
					ID int64 `json:"id"`
				}
				_ = result.into(&out)
				ids.Store(out.ID, true)
				created.Add(1)
			case result.Status == http.StatusConflict && result.code() == "SOLD_OUT":
				soldOut.Add(1)
			default:
				failure.Store(fmt.Sprintf("%d: %s", result.Status, truncate(result.Body)))
			}
		}(i)
	}
	close(gate)
	wg.Wait()
	if value := failure.Load(); value != nil {
		return fmt.Errorf("beklenmeyen yanıt: %v", value)
	}
	if created.Load() != 11 || soldOut.Load() != attempts-11 {
		return fmt.Errorf("oluşan=%d dolu=%d, beklenen 11 (10 oda + %%10 overbooking)", created.Load(), soldOut.Load())
	}
	sold, limit, err := s.stock(from)
	if err != nil {
		return err
	}
	if sold != 11 || limit != 1 {
		return fmt.Errorf("stok=%d limit=%d", sold, limit)
	}
	var overview struct {
		Nights     []json.RawMessage `json:"nights"`
		Candidates []struct {
			ID int64 `json:"id"`
		} `json:"candidates"`
		Partners []struct {
			ID int64 `json:"id"`
		} `json:"partners"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/overbooking", s.tokens["reception"], nil, &overview); err != nil {
		return err
	}
	if len(overview.Nights) == 0 || len(overview.Partners) == 0 {
		return fmt.Errorf("fazla satış listesi boş")
	}
	var victim int64
	ids.Range(func(key, _ any) bool {
		victim = key.(int64)
		return false
	})
	err = s.api.expect(http.StatusCreated, http.MethodPost, fmt.Sprintf("%s/reservations/%d/walk", api, victim), s.tokens["reception"],
		map[string]any{"partner_hotel_id": overview.Partners[0].ID, "reason": "Otel dolu"}, nil)
	if err != nil {
		return err
	}
	sold, _, err = s.stock(from)
	if err != nil {
		return err
	}
	if sold != 10 {
		return fmt.Errorf("walk sonrası stok %d", sold)
	}
	return nil
}

func (s *suite) stock(date time.Time) (int32, int32, error) {
	var body struct {
		Items []struct {
			TypeID int64 `json:"room_type_id"`
			Sold   int32 `json:"sold"`
			Limit  int32 `json:"overbook_limit"`
		} `json:"items"`
	}
	path := fmt.Sprintf("%s/availability?from=%s&to=%s", api, day(date), day(date.AddDate(0, 0, 1)))
	if err := s.api.expect(http.StatusOK, http.MethodGet, path, s.tokens["manager"], nil, &body); err != nil {
		return 0, 0, err
	}
	for _, item := range body.Items {
		if item.TypeID == s.typeID {
			return item.Sold, item.Limit, nil
		}
	}
	return 0, 0, fmt.Errorf("stok satırı yok")
}

type quote struct {
	Hash       string `json:"hash"`
	NetKurus   int64  `json:"net_kurus"`
	EarlyBP    int64  `json:"early_discount_bp"`
	TotalKurus int64  `json:"total_kurus"`
	Nights     []struct {
		Weekend  bool  `json:"weekend"`
		SeasonBP int64 `json:"season_bp"`
	} `json:"nights"`
}

func (s *suite) quote(from, to time.Time, codes []string, ages []int) (response, error) {
	return s.api.post(api+"/quotes", s.tokens["reservations"], map[string]any{
		"room_type_id": s.typeID, "rate_plan_id": s.planID, "check_in": day(from), "check_out": day(to),
		"adults": 2, "codes": codes, "children_ages": ages,
	})
}

func (s *suite) checkPriceEngine() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	year := today.Year() + 1
	summer := time.Date(year, time.July, 13, 0, 0, 0, 0, time.UTC)
	result, err := s.quote(summer, summer.AddDate(0, 0, 1), nil, nil)
	if err != nil {
		return err
	}
	if result.code() != "MIN_STAY" {
		return fmt.Errorf("minimum konaklama uygulanmadı: %s", truncate(result.Body))
	}
	stay := summer.AddDate(0, 0, 4)
	var first, second, third quote
	for _, target := range []*quote{&first, &second, &third} {
		result, err := s.quote(summer, stay, nil, nil)
		if err != nil {
			return err
		}
		if err := result.into(target); err != nil {
			return err
		}
	}
	if first.Hash == "" || first.Hash != second.Hash || second.Hash != third.Hash {
		return fmt.Errorf("aynı girdi farklı hash üretti")
	}
	if first.EarlyBP != 1500 {
		return fmt.Errorf("erken rezervasyon indirimi: %d", first.EarlyBP)
	}
	weekend, season := false, false
	for _, night := range first.Nights {
		weekend = weekend || night.Weekend
		season = season || night.SeasonBP == 15000
	}
	if !weekend || !season || len(first.Nights) != 4 {
		return fmt.Errorf("gün gün döküm hatalı: %+v", first.Nights)
	}
	result, err = s.quote(summer, stay, []string{"HOSGELDIN10", "FLASH25"}, nil)
	if err != nil {
		return err
	}
	if result.code() != "CAMPAIGN_INCOMPATIBLE" {
		return fmt.Errorf("uyumsuz kampanya kabul edildi: %s", truncate(result.Body))
	}
	var combined quote
	result, err = s.quote(summer, stay, []string{"HOSGELDIN10", "AILE5"}, nil)
	if err != nil {
		return err
	}
	if err := result.into(&combined); err != nil {
		return err
	}
	if combined.NetKurus >= first.NetKurus {
		return fmt.Errorf("uyumlu kampanyalar indirim yapmadı")
	}
	var child quote
	result, err = s.quote(summer, stay, nil, []int{9})
	if err != nil {
		return err
	}
	if err := result.into(&child); err != nil {
		return err
	}
	if child.NetKurus <= first.NetKurus {
		return fmt.Errorf("çocuk ücreti eklenmedi")
	}
	id, err := s.book(summer, stay)
	if err != nil {
		return err
	}
	if err := s.api.expect(http.StatusOK, http.MethodPut, api+"/pricing/rates", s.tokens["revenue"], map[string]any{
		"room_type_id": s.typeID, "rate_plan_id": s.planID, "base_kurus": 900000, "extra_adult_kurus": 80000, "child_kurus": 40000,
	}, nil); err != nil {
		return err
	}
	var check struct {
		Matches bool `json:"matches"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("%s/reservations/%d/price", api, id), s.tokens["reservations"], nil, &check); err != nil {
		return err
	}
	if !check.Matches {
		return fmt.Errorf("fiyat garantisi bozuldu")
	}
	var changed quote
	result, err = s.quote(summer, stay, nil, nil)
	if err != nil {
		return err
	}
	if err := result.into(&changed); err != nil {
		return err
	}
	if changed.NetKurus == first.NetKurus {
		return fmt.Errorf("kural değişikliği yeni teklife yansımadı")
	}
	return s.api.expect(http.StatusOK, http.MethodPut, api+"/pricing/rates", s.tokens["revenue"], map[string]any{
		"room_type_id": s.typeID, "rate_plan_id": s.planID, "base_kurus": 300000, "extra_adult_kurus": 80000, "child_kurus": 40000,
	}, nil)
}

func (s *suite) checkSolver() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	var plan struct {
		Rooms  int `json:"rooms"`
		Stays  int `json:"stays"`
		Result struct {
			ElapsedMS int64 `json:"elapsed_ms"`
			Moves     int   `json:"moves"`
			PrefsMet  int   `json:"prefs_met"`
			Segments  []struct {
				StayID int64 `json:"stay_id"`
				RoomID int64 `json:"room_id"`
				From   int   `json:"from"`
				To     int   `json:"to"`
			} `json:"segments"`
		} `json:"result"`
		Unassigned []int64 `json:"unassigned"`
		Baseline   int     `json:"baseline_moves"`
	}
	body := map[string]any{"from": day(today), "to": day(today.AddDate(0, 0, 90)), "apply": true}
	begin := time.Now()
	if err := s.api.expect(http.StatusOK, http.MethodPost, api+"/assignments/solve", s.tokens["reservations"], body, &plan); err != nil {
		return err
	}
	elapsed := time.Since(begin)
	if plan.Rooms < 120 || plan.Stays < 400 {
		return fmt.Errorf("senaryo küçük: oda=%d konaklama=%d", plan.Rooms, plan.Stays)
	}
	if elapsed > 10*time.Second {
		return fmt.Errorf("çözüm süresi %v", elapsed)
	}
	used := map[[2]int64]int64{}
	for _, seg := range plan.Result.Segments {
		for d := seg.From; d < seg.To; d++ {
			key := [2]int64{seg.RoomID, int64(d)}
			if other, ok := used[key]; ok {
				return fmt.Errorf("oda %d iki rezervasyona verildi: %d, %d", seg.RoomID, other, seg.StayID)
			}
			used[key] = seg.StayID
		}
	}
	fmt.Printf("    tahsis: %d oda, %d konaklama, %v, taşıma=%d (ilk-uygun=%d), atanamayan=%d\n",
		plan.Rooms, plan.Stays, elapsed.Round(time.Millisecond), plan.Result.Moves, plan.Baseline, len(plan.Unassigned))
	if plan.Result.Moves > plan.Baseline {
		return fmt.Errorf("çözücü taban çizgisinden kötü: %d > %d", plan.Result.Moves, plan.Baseline)
	}
	return nil
}

func (s *suite) signed(code string, event channel.Event) (response, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return response{}, err
	}
	var payload json.RawMessage = raw
	return s.api.do(http.MethodPost, api+"/channels/"+code+"/events", "", payload, map[string]string{"X-Channel-Signature": channel.Sign(s.secret, raw)})
}

func (s *suite) checkChannel() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	from := today.AddDate(0, 0, 75)
	ref := "VERIFY-" + s.run
	newEvent := func(id string, seq int64, kind string, nights int) channel.Event {
		return channel.Event{
			EventID: ref + "-" + id, ExternalRef: ref, Seq: seq, Type: kind, RoomType: "STD",
			CheckIn: day(from), CheckOut: day(from.AddDate(0, 0, nights)), Adults: 2, GuestName: "Kanal Doğrulama", NetKurus: int64(nights) * 300000,
		}
	}
	ordered := []channel.Event{newEvent("3", 3, "modify", 4), newEvent("1", 1, "create", 2), newEvent("2", 2, "modify", 3), newEvent("3", 3, "modify", 4), newEvent("1", 1, "create", 2)}
	outcomes := make([]string, 0, len(ordered))
	for _, event := range ordered {
		result, err := s.signed("booking", event)
		if err != nil {
			return err
		}
		var body struct {
			Outcome string `json:"outcome"`
		}
		if err := result.into(&body); err != nil {
			return err
		}
		outcomes = append(outcomes, body.Outcome)
	}
	want := []string{"applied", "stale", "stale", "duplicate", "duplicate"}
	for i := range want {
		if outcomes[i] != want[i] {
			return fmt.Errorf("sonuçlar %v, beklenen %v", outcomes, want)
		}
	}
	var list struct {
		Items []struct {
			ID       int64  `json:"id"`
			CheckOut string `json:"check_out"`
			Code     string `json:"code"`
		} `json:"items"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/reservations?limit=20&q=Kanal+Do%C4%9Frulama", s.tokens["manager"], nil, &list); err != nil {
		return err
	}
	if len(list.Items) == 0 || list.Items[0].CheckOut[:10] != day(from.AddDate(0, 0, 4)) {
		return fmt.Errorf("en güncel durum uygulanmadı: %+v", list.Items)
	}
	bad, err := s.api.do(http.MethodPost, api+"/channels/booking/events", "", map[string]string{"event_id": "x"}, map[string]string{"X-Channel-Signature": "yanlis"})
	if err != nil {
		return err
	}
	if bad.Status != http.StatusUnauthorized {
		return fmt.Errorf("sahte imza kabul edildi: %d", bad.Status)
	}
	return s.checkDeadLetter(from)
}

func (s *suite) checkDeadLetter(from time.Time) error {
	if err := s.simulator.expect(http.StatusNoContent, http.MethodPost, "/control", "", map[string]int{"fail_rate": 100}, nil); err != nil {
		return err
	}
	defer s.simulator.post("/control", "", map[string]int{"fail_rate": 0})
	id, err := s.book(from.AddDate(0, 0, 6), from.AddDate(0, 0, 7))
	if err != nil {
		return err
	}
	var dead struct {
		Items []struct {
			ID       int64  `json:"id"`
			TypeCode string `json:"type_code"`
			Attempts int    `json:"attempts"`
		} `json:"items"`
	}
	deadline := time.Now().Add(40 * time.Second)
	found := int64(0)
	code := upper("V" + s.run)
	for found == 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("mesaj ölü kuyruğa düşmedi")
		}
		time.Sleep(500 * time.Millisecond)
		if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/channels/outbox?status=dead&limit=100", s.tokens["manager"], nil, &dead); err != nil {
			return err
		}
		for _, item := range dead.Items {
			if item.TypeCode == code {
				found = item.ID
				if item.Attempts < 2 {
					return fmt.Errorf("yeniden deneme yapılmadı: %d", item.Attempts)
				}
				break
			}
		}
	}
	if err := s.simulator.expect(http.StatusNoContent, http.MethodPost, "/control", "", map[string]int{"fail_rate": 0}, nil); err != nil {
		return err
	}
	if err := s.api.expect(http.StatusOK, http.MethodPost, fmt.Sprintf("%s/channels/outbox/%d/retry", api, found), s.tokens["manager"], nil, nil); err != nil {
		return err
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		var sent struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		}
		if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/channels/outbox?status=sent&limit=200", s.tokens["manager"], nil, &sent); err != nil {
			return err
		}
		for _, item := range sent.Items {
			if item.ID == found {
				return s.api.expect(http.StatusOK, http.MethodPost, fmt.Sprintf("%s/reservations/%d/cancel", api, id), s.tokens["reservations"], nil, nil)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ölü mesaj yeniden gönderilemedi")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *suite) checkIdempotency() error {
	base := fmt.Sprintf("%s/folios/%d/payments", api, s.folioID)
	key := map[string]string{"Idempotency-Key": "verify-" + s.run}
	body := map[string]any{"amount_kurus": 1000, "method": "nakit"}
	first, err := s.api.do(http.MethodPost, base, s.tokens["reception"], body, key)
	if err != nil {
		return err
	}
	second, err := s.api.do(http.MethodPost, base, s.tokens["reception"], body, key)
	if err != nil {
		return err
	}
	if first.Status != http.StatusCreated || second.Status != http.StatusCreated || string(first.Body) != string(second.Body) {
		return fmt.Errorf("yanıtlar farklı: %d / %d", first.Status, second.Status)
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		return fmt.Errorf("ikinci istek yeniden işlendi")
	}
	other, err := s.api.do(http.MethodPost, base, s.tokens["reception"], map[string]any{"amount_kurus": 2000, "method": "nakit"}, key)
	if err != nil {
		return err
	}
	if other.Status != http.StatusUnprocessableEntity {
		return fmt.Errorf("farklı gövde kabul edildi: %d", other.Status)
	}
	return nil
}

func (s *suite) checkAPIContract() error {
	result, err := s.api.get(api+"/reservations/999999999", s.tokens["manager"])
	if err != nil {
		return err
	}
	if result.Status != http.StatusNotFound || result.code() != "NOT_FOUND" {
		return fmt.Errorf("hata biçimi: %d %s", result.Status, truncate(result.Body))
	}
	var first, second struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/reservations?limit=25", s.tokens["manager"], nil, &first); err != nil {
		return err
	}
	if first.NextCursor == nil {
		return fmt.Errorf("sonraki imleç yok")
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("%s/reservations?limit=25&cursor=%d", api, *first.NextCursor), s.tokens["manager"], nil, &second); err != nil {
		return err
	}
	if len(second.Items) == 0 || second.Items[0].ID >= first.Items[len(first.Items)-1].ID {
		return fmt.Errorf("keyset sayfalama hatalı")
	}
	ids := make([]int64, 0, 50)
	for _, item := range append(first.Items, second.Items...) {
		ids = append(ids, item.ID)
	}
	if !sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] > ids[j] }) {
		return fmt.Errorf("sıra bozuk")
	}
	var audit struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/audit-log?limit=50", s.tokens["manager"], nil, &audit); err != nil {
		return err
	}
	if len(audit.Items) == 0 {
		return fmt.Errorf("denetim kaydı boş")
	}
	metrics, err := s.api.get("/metrics", "")
	if err != nil {
		return err
	}
	if metrics.Status != http.StatusOK || !contains(metrics.Body, "konakpro_http_requests_total") {
		return fmt.Errorf("metrik ucu eksik")
	}
	openapi, err := s.api.get("/openapi.yaml", "")
	if err != nil {
		return err
	}
	if openapi.Status != http.StatusOK || !contains(openapi.Body, "openapi:") {
		return fmt.Errorf("OpenAPI belgesi yok")
	}
	return nil
}

func contains(body []byte, value string) bool {
	return bytes.Contains(body, []byte(value))
}

type auditState struct {
	BusinessDate string `json:"business_date"`
	Violations   int    `json:"integrity_violations"`
	Duplicates   int    `json:"duplicate_charges"`
}

var auditStart time.Time

func (s *suite) checkNightAudit() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	auditStart = today
	token := s.tokens["revenue"]
	crashed, err := s.api.post(api+"/night-audit/run", token, map[string]any{"crash_after": 3})
	if err != nil {
		return err
	}
	if crashed.Status != http.StatusInternalServerError {
		return fmt.Errorf("hata enjeksiyonu çalışmadı (FAULT_INJECTION=true olmalı): %d %s", crashed.Status, truncate(crashed.Body))
	}
	after, err := s.businessDate()
	if err != nil {
		return err
	}
	if !after.Equal(today) {
		return fmt.Errorf("yarıda kalan denetim tarihi ilerletti")
	}
	var report struct {
		Charged int  `json:"charged"`
		Resumed bool `json:"resumed"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodPost, api+"/night-audit/run", token, nil, &report); err != nil {
		return err
	}
	if !report.Resumed || report.Charged == 0 {
		return fmt.Errorf("devam edilmedi: %+v", report)
	}
	var state auditState
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/night-audit", token, nil, &state); err != nil {
		return err
	}
	if state.Duplicates != 0 || state.Violations != 0 {
		return fmt.Errorf("çift ücret=%d bütünlük ihlali=%d", state.Duplicates, state.Violations)
	}
	var folio struct {
		Lines []struct {
			Kind string `json:"kind"`
		} `json:"lines"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("%s/folios/%d", api, s.folioID), s.tokens["reception"], nil, &folio); err != nil {
		return err
	}
	kinds := map[string]int{}
	for _, line := range folio.Lines {
		kinds[line.Kind]++
	}
	if kinds["room"] != 1 || kinds["tax_kdv"] != 1 || kinds["tax_accom"] != 1 {
		return fmt.Errorf("gecelik ücret ve vergiler yazılmadı: %v", kinds)
	}
	return nil
}

func (s *suite) checkAuditAdvanced() error {
	today, err := s.businessDate()
	if err != nil {
		return err
	}
	if !today.Equal(auditStart.AddDate(0, 0, 1)) {
		return fmt.Errorf("otel günü %s, beklenen %s", day(today), day(auditStart.AddDate(0, 0, 1)))
	}
	var state struct {
		History []struct {
			Status string `json:"status"`
		} `json:"history"`
	}
	if err := s.api.expect(http.StatusOK, http.MethodGet, api+"/night-audit", s.tokens["manager"], nil, &state); err != nil {
		return err
	}
	if len(state.History) == 0 || state.History[0].Status != "completed" {
		return fmt.Errorf("denetim geçmişi: %+v", state.History)
	}
	return nil
}
