package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"konakpro/internal/api"
	"konakpro/internal/assign"
	"konakpro/internal/auth"
	"konakpro/internal/booking"
	"konakpro/internal/channel"
	"konakpro/internal/folio"
	"konakpro/internal/housekeeping"
	"konakpro/internal/nightaudit"
	"konakpro/internal/platform/store"
	"konakpro/internal/reports"
	"konakpro/internal/testkit"
)

const password = "Test!12345"

var ctx = context.Background()

type harness struct {
	t      *testing.T
	server *httptest.Server
	store  *store.Store
	tokens map[string]string
}

func setup(t *testing.T) *harness {
	t.Helper()
	st := testkit.Fresh(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := prometheus.NewRegistry()
	server := &api.Server{
		Log: log, Store: st, Auth: auth.New(st, strings.Repeat("k", 32), time.Minute, time.Hour),
		Booking: &booking.Service{Store: st}, Folio: &folio.Service{Store: st},
		NightAudit: &nightaudit.Service{Store: st, Log: log, AllowFaults: true}, Assign: &assign.Service{Store: st, Seed: 1},
		Housekeeping: &housekeeping.Service{Store: st}, Reports: &reports.Service{Store: st}, Channel: &channel.Service{Store: st},
		Timeout: 10 * time.Second, Registry: registry, Metrics: api.NewMetrics(registry),
	}
	h := &harness{t: t, server: httptest.NewServer(server.Router()), store: st, tokens: map[string]string{}}
	t.Cleanup(h.server.Close)
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE users SET password_hash = $1", hash); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users (email, password_hash, full_name, role) VALUES
		('r@t.co', $1, 'Resepsiyon', 'reception'), ('b@t.co', $1, 'Rezervasyon', 'reservations'),
		('h@t.co', $1, 'Kat', 'housekeeping'), ('a@t.co', $1, 'Gelir', 'revenue'), ('x@t.co', $1, 'Yabancı', 'guest')`, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "INSERT INTO guests (full_name, user_id) SELECT 'Yabancı', id FROM users WHERE email = 'x@t.co'"); err != nil {
		t.Fatal(err)
	}
	for role, email := range map[string]string{"manager": "m@t.co", "guest": "g@t.co", "reception": "r@t.co", "reservations": "b@t.co", "housekeeping": "h@t.co", "revenue": "a@t.co", "stranger": "x@t.co"} {
		var body struct {
			Tokens struct {
				AccessToken string `json:"access_token"`
			} `json:"tokens"`
		}
		h.expect(http.StatusOK, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": email, "password": password}, &body)
		h.tokens[role] = body.Tokens.AccessToken
	}
	return h
}

func (h *harness) raw(method, path, token string, payload any, headers map[string]string) (int, []byte, http.Header) {
	h.t.Helper()
	var reader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			h.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := h.server.Client().Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, body, response.Header
}

func (h *harness) expect(want int, method, path, token string, payload, target any) {
	h.t.Helper()
	status, body, _ := h.raw(method, path, token, payload, nil)
	if status != want {
		h.t.Fatalf("%s %s: beklenen %d, gelen %d: %s", method, path, want, status, body)
	}
	if target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) book(token, from, to string) int64 {
	h.t.Helper()
	var out struct {
		ID int64 `json:"id"`
	}
	h.expect(http.StatusCreated, http.MethodPost, "/api/v1/reservations", token, map[string]any{
		"room_type_id": 1, "rate_plan_id": 1, "check_in": from, "check_out": to, "adults": 2,
		"guest": map[string]string{"full_name": "Deneme"},
	}, &out)
	return out.ID
}

var routeLine = regexp.MustCompile(`\{method: http\.Method(\w+), path: v1 \+ "([^"]+)"(.*)`)

func routeTable(t *testing.T) []struct{ method, path, flags string } {
	t.Helper()
	source, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	var routes []struct{ method, path, flags string }
	for _, match := range routeLine.FindAllStringSubmatch(string(source), -1) {
		routes = append(routes, struct{ method, path, flags string }{strings.ToUpper(match[1]), "/api/v1" + match[2], match[3]})
	}
	if len(routes) < 50 {
		t.Fatalf("rota tablosu okunamadı: %d", len(routes))
	}
	return routes
}

func concrete(path string) string {
	return regexp.MustCompile(`\{\w+\}`).ReplaceAllString(path, "1")
}

func TestEveryProtectedRouteRejectsAnonymous(t *testing.T) {
	h := setup(t)
	for _, route := range routeTable(t) {
		if strings.Contains(route.flags, "public: true") {
			continue
		}
		status, body, _ := h.raw(route.method, concrete(route.path), "", nil, nil)
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s oturumsuz %d döndü: %s", route.method, route.path, status, body)
		}
	}
}

func TestEveryProtectedRouteDeclaresRoles(t *testing.T) {
	for _, route := range routeTable(t) {
		if !strings.Contains(route.flags, "public: true") && !strings.Contains(route.flags, "roles:") {
			t.Errorf("%s %s rol tanımlamıyor", route.method, route.path)
		}
	}
}

func TestPublicRoutesAreExactlyTheKnownSet(t *testing.T) {
	want := map[string]bool{"/api/v1/auth/login": true, "/api/v1/auth/refresh": true, "/api/v1/auth/logout": true, "/api/v1/channels/{code}/events": true}
	for _, route := range routeTable(t) {
		if strings.Contains(route.flags, "public: true") && !want[route.path] {
			t.Errorf("beklenmeyen açık uç: %s", route.path)
		}
	}
}

func TestOpenAPICoversEveryRoute(t *testing.T) {
	document, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range routeTable(t) {
		path := strings.TrimPrefix(route.path, "/api/v1")
		if !bytes.Contains(document, []byte("  "+path+":\n")) {
			t.Errorf("OpenAPI eksik: %s %s", route.method, path)
		}
	}
}

func TestGuestRoleMatrix(t *testing.T) {
	h := setup(t)
	allowed := []string{"/me", "/room-types", "/rate-plans", "/reservations"}
	for _, path := range allowed {
		h.expect(http.StatusOK, http.MethodGet, "/api/v1"+path, h.tokens["guest"], nil, nil)
	}
	denied := []string{"/dashboard", "/rooms", "/calendar?from=2027-03-01&to=2027-03-05", "/overbooking", "/pricing", "/reports/kpi?from=2027-01-01&to=2027-03-01", "/audit-log", "/housekeeping/tasks", "/channels", "/night-audit", "/users", "/live"}
	for _, path := range denied {
		h.expect(http.StatusForbidden, http.MethodGet, "/api/v1"+path, h.tokens["guest"], nil, nil)
	}
}

func TestHousekeepingRoleMatrix(t *testing.T) {
	h := setup(t)
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/housekeeping/tasks", h.tokens["housekeeping"], nil, nil)
	for _, path := range []string{"/reservations", "/pricing", "/night-audit", "/reports/kpi?from=2027-01-01&to=2027-02-01", "/audit-log"} {
		h.expect(http.StatusForbidden, http.MethodGet, "/api/v1"+path, h.tokens["housekeeping"], nil, nil)
	}
}

func TestOnlyFinanceRolesRunAudit(t *testing.T) {
	h := setup(t)
	for _, role := range []string{"reception", "reservations", "housekeeping", "guest"} {
		h.expect(http.StatusForbidden, http.MethodPost, "/api/v1/night-audit/run", h.tokens[role], nil, nil)
	}
	h.expect(http.StatusOK, http.MethodPost, "/api/v1/night-audit/run", h.tokens["revenue"], nil, nil)
}

func TestGuestSeesOnlyOwnReservation(t *testing.T) {
	h := setup(t)
	own := h.book(h.tokens["guest"], "2027-03-10", "2027-03-12")
	h.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("/api/v1/reservations/%d", own), h.tokens["guest"], nil, nil)
	h.expect(http.StatusForbidden, http.MethodGet, fmt.Sprintf("/api/v1/reservations/%d", own), h.tokens["stranger"], nil, nil)
	h.expect(http.StatusForbidden, http.MethodGet, fmt.Sprintf("/api/v1/reservations/%d/folios", own), h.tokens["stranger"], nil, nil)
	h.expect(http.StatusForbidden, http.MethodPost, fmt.Sprintf("/api/v1/reservations/%d/cancel", own), h.tokens["stranger"], nil, nil)
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/reservations", h.tokens["stranger"], nil, &list)
	if len(list.Items) != 0 {
		t.Fatalf("yabancı misafir kayıt gördü: %+v", list.Items)
	}
}

func TestGuestCannotBookForAnotherGuest(t *testing.T) {
	h := setup(t)
	var out struct {
		ID int64 `json:"id"`
	}
	h.expect(http.StatusCreated, http.MethodPost, "/api/v1/reservations", h.tokens["stranger"], map[string]any{
		"room_type_id": 1, "rate_plan_id": 1, "check_in": "2027-03-10", "check_out": "2027-03-11", "adults": 1, "guest_id": 1,
	}, &out)
	h.expect(http.StatusForbidden, http.MethodGet, fmt.Sprintf("/api/v1/reservations/%d", out.ID), h.tokens["guest"], nil, nil)
}

func TestLoginFailuresUseSameMessage(t *testing.T) {
	h := setup(t)
	_, unknown, _ := h.raw(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "yok@t.co", "password": "x"}, nil)
	_, wrong, _ := h.raw(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "m@t.co", "password": "x"}, nil)
	if !bytes.Equal(unknown, wrong) {
		t.Fatalf("hesap varlığı sızıyor: %s / %s", unknown, wrong)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	h := setup(t)
	h.expect(http.StatusUnauthorized, http.MethodGet, "/api/v1/me", h.tokens["manager"]+"x", nil, nil)
	h.expect(http.StatusUnauthorized, http.MethodGet, "/api/v1/me", "eyJhbGciOiJub25lIn0.eyJyb2xlIjoibWFuYWdlciJ9.", nil, nil)
}

func TestRefreshRotationDetectsReuse(t *testing.T) {
	h := setup(t)
	type tokens struct {
		Tokens struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	var first, second tokens
	h.expect(http.StatusOK, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "m@t.co", "password": password}, &first)
	h.expect(http.StatusOK, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first.Tokens.RefreshToken}, &second)
	h.expect(http.StatusUnauthorized, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first.Tokens.RefreshToken}, nil)
	h.expect(http.StatusUnauthorized, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": second.Tokens.RefreshToken}, nil)
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	h := setup(t)
	var login struct {
		Tokens struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	h.expect(http.StatusOK, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "m@t.co", "password": password}, &login)
	h.expect(http.StatusNoContent, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": login.Tokens.RefreshToken}, nil)
	h.expect(http.StatusUnauthorized, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": login.Tokens.RefreshToken}, nil)
}

func TestErrorShapeIsConsistent(t *testing.T) {
	h := setup(t)
	cases := []struct {
		status  int
		method  string
		path    string
		payload any
	}{
		{404, http.MethodGet, "/api/v1/reservations/999", nil},
		{404, http.MethodGet, "/api/v1/yok", nil},
		{405, http.MethodDelete, "/api/v1/reservations", nil},
		{422, http.MethodGet, "/api/v1/reservations/abc", nil},
		{422, http.MethodPost, "/api/v1/quotes", map[string]string{"bilinmeyen": "alan"}},
		{422, http.MethodGet, "/api/v1/reservations?limit=999", nil},
	}
	for _, c := range cases {
		status, body, _ := h.raw(c.method, c.path, h.tokens["manager"], c.payload, nil)
		var parsed struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error.Code == "" || parsed.Error.Message == "" || status != c.status {
			t.Errorf("%s %s: %d %s", c.method, c.path, status, body)
		}
	}
}

func TestIdempotentReplayAndKeyReuse(t *testing.T) {
	h := setup(t)
	id := h.book(h.tokens["reception"], "2027-03-10", "2027-03-11")
	var folios struct {
		Items []struct {
			Folio struct {
				ID int64 `json:"id"`
			} `json:"folio"`
		} `json:"items"`
	}
	h.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("/api/v1/reservations/%d/folios", id), h.tokens["reception"], nil, &folios)
	path := fmt.Sprintf("/api/v1/folios/%d/payments", folios.Items[0].Folio.ID)
	key := map[string]string{"Idempotency-Key": "k1"}
	body := map[string]any{"amount_kurus": 1000, "method": "kart"}
	first, a, _ := h.raw(http.MethodPost, path, h.tokens["reception"], body, key)
	second, b, header := h.raw(http.MethodPost, path, h.tokens["reception"], body, key)
	if first != 201 || second != 201 || !bytes.Equal(a, b) || header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("tekrar: %d %d %s", first, second, header)
	}
	var balance int64
	if err := h.store.Pool.QueryRow(ctx, "SELECT balance_kurus FROM folios WHERE id = $1", folios.Items[0].Folio.ID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != -1000 {
		t.Fatalf("ödeme iki kez yazıldı: %d", balance)
	}
	if status, _, _ := h.raw(http.MethodPost, path, h.tokens["reception"], map[string]any{"amount_kurus": 2000, "method": "kart"}, key); status != 422 {
		t.Fatalf("farklı gövde: %d", status)
	}
}

func TestConcurrentSameKeyCreatesOneReservation(t *testing.T) {
	h := setup(t)
	var wg sync.WaitGroup
	var created, inProgress atomic.Int64
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _, _ := h.raw(http.MethodPost, "/api/v1/reservations", h.tokens["reception"], map[string]any{
				"room_type_id": 1, "rate_plan_id": 1, "check_in": "2027-03-10", "check_out": "2027-03-11", "adults": 2, "guest": map[string]string{"full_name": "Aynı"},
			}, map[string]string{"Idempotency-Key": "ayni"})
			switch status {
			case 201:
				created.Add(1)
			case 409:
				inProgress.Add(1)
			}
		}()
	}
	wg.Wait()
	var rows int
	if err := h.store.Pool.QueryRow(ctx, "SELECT count(*) FROM reservations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || created.Load()+inProgress.Load() != 12 {
		t.Fatalf("satır=%d 201=%d 409=%d", rows, created.Load(), inProgress.Load())
	}
}

func TestKeysetPaginationIsStableAndComplete(t *testing.T) {
	h := setup(t)
	for i := 0; i < 5; i++ {
		h.book(h.tokens["reception"], "2027-04-01", "2027-04-02")
	}
	seen := map[int64]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		var result struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
			NextCursor *int64 `json:"next_cursor"`
		}
		h.expect(http.StatusOK, http.MethodGet, "/api/v1/reservations?limit=2"+cursor, h.tokens["manager"], nil, &result)
		for _, item := range result.Items {
			if seen[item.ID] {
				t.Fatalf("tekrar eden kayıt: %d", item.ID)
			}
			seen[item.ID] = true
		}
		if result.NextCursor == nil {
			break
		}
		cursor = fmt.Sprintf("&cursor=%d", *result.NextCursor)
	}
	if len(seen) != 5 {
		t.Fatalf("eksik kayıt: %d", len(seen))
	}
}

func TestHealthReportsDatabase(t *testing.T) {
	h := setup(t)
	var body map[string]string
	h.expect(http.StatusOK, http.MethodGet, "/health", "", nil, &body)
	if body["database"] != "ok" || body["business_date"] != testkit.Day0 {
		t.Fatalf("sağlık: %v", body)
	}
	h.store.Pool.Close()
	h.expect(http.StatusServiceUnavailable, http.MethodGet, "/health", "", nil, nil)
}

func TestMetricsExposeRequestCounters(t *testing.T) {
	h := setup(t)
	h.expect(http.StatusOK, http.MethodGet, "/health", "", nil, nil)
	_, body, _ := h.raw(http.MethodGet, "/metrics", "", nil, nil)
	if !bytes.Contains(body, []byte("konakpro_http_requests_total")) {
		t.Fatal("metrik yok")
	}
}

func TestFullStayLifecycleOverHTTP(t *testing.T) {
	h := setup(t)
	token := h.tokens["reception"]
	id := h.book(token, testkit.Day0, "2027-03-03")
	base := fmt.Sprintf("/api/v1/reservations/%d", id)
	h.expect(http.StatusOK, http.MethodPost, base+"/check-in", token, nil, nil)
	h.expect(http.StatusConflict, http.MethodPost, base+"/check-out", token, nil, nil)
	for i := 0; i < 2; i++ {
		h.expect(http.StatusOK, http.MethodPost, "/api/v1/night-audit/run", h.tokens["manager"], nil, nil)
	}
	var folios struct {
		Items []struct {
			Folio struct {
				ID           int64 `json:"id"`
				BalanceKurus int64 `json:"balance_kurus"`
			} `json:"folio"`
		} `json:"items"`
	}
	h.expect(http.StatusOK, http.MethodGet, base+"/folios", token, nil, &folios)
	folioID, balance := folios.Items[0].Folio.ID, folios.Items[0].Folio.BalanceKurus
	if balance != 224000 {
		t.Fatalf("bakiye %d", balance)
	}
	h.expect(http.StatusConflict, http.MethodPost, base+"/check-out", token, nil, nil)
	h.expect(http.StatusCreated, http.MethodPost, fmt.Sprintf("/api/v1/folios/%d/payments", folioID), token, map[string]any{"amount_kurus": balance, "method": "kart"}, nil)
	h.expect(http.StatusOK, http.MethodPost, base+"/check-out", token, nil, nil)
	h.expect(http.StatusConflict, http.MethodPost, fmt.Sprintf("/api/v1/folios/%d/charges", folioID), token, map[string]any{"amount_kurus": 100, "description": "geç"}, nil)

	var tasks struct {
		Items []struct {
			ID       int64  `json:"id"`
			TaskType string `json:"task_type"`
			RoomID   int64  `json:"room_id"`
		} `json:"items"`
	}
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/housekeeping/tasks", h.tokens["housekeeping"], nil, &tasks)
	if len(tasks.Items) == 0 {
		t.Fatal("çıkış temizliği görevi oluşmadı")
	}
	task := tasks.Items[len(tasks.Items)-1]
	h.expect(http.StatusOK, http.MethodPut, fmt.Sprintf("/api/v1/housekeeping/tasks/%d", task.ID), h.tokens["housekeeping"], map[string]any{"status": "tamamlandi"}, nil)
	var status string
	if err := h.store.Pool.QueryRow(ctx, "SELECT hk_status FROM rooms WHERE id = $1", task.RoomID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "clean" {
		t.Fatalf("oda %s", status)
	}
}

func TestPricingCrudIsAudited(t *testing.T) {
	h := setup(t)
	token := h.tokens["revenue"]
	var season struct {
		ID int64 `json:"id"`
	}
	h.expect(http.StatusCreated, http.MethodPost, "/api/v1/pricing/seasons", token, map[string]any{"name": "Yaz", "from": "2027-06-01", "to": "2027-09-01", "multiplier_bp": 15000, "min_stay": 3}, &season)
	h.expect(http.StatusConflict, http.MethodPost, "/api/v1/pricing/seasons", token, map[string]any{"name": "Çakışan", "from": "2027-07-01", "to": "2027-08-01", "multiplier_bp": 12000, "min_stay": 1}, nil)
	var tier struct {
		ID int64 `json:"id"`
	}
	h.expect(http.StatusOK, http.MethodPost, "/api/v1/pricing/early-tiers", token, map[string]any{"min_lead_days": 30, "discount_bp": 1000}, &tier)
	var a, b struct {
		ID int64 `json:"id"`
	}
	campaign := func(code string) map[string]any {
		return map[string]any{"code": code, "name": code, "discount_bp": 500, "valid_from": "2027-01-01", "valid_to": "2028-01-01"}
	}
	h.expect(http.StatusCreated, http.MethodPost, "/api/v1/pricing/campaigns", token, campaign("AA"), &a)
	h.expect(http.StatusCreated, http.MethodPost, "/api/v1/pricing/campaigns", token, campaign("BB"), &b)
	h.expect(http.StatusCreated, http.MethodPost, "/api/v1/pricing/campaign-compat", token, map[string]any{"campaign_a": b.ID, "campaign_b": a.ID}, nil)
	h.expect(http.StatusOK, http.MethodPost, "/api/v1/quotes", token, map[string]any{"room_type_id": 1, "rate_plan_id": 1, "check_in": "2027-03-10", "check_out": "2027-03-11", "adults": 2, "codes": []string{"AA", "BB"}}, nil)
	h.expect(http.StatusNoContent, http.MethodDelete, "/api/v1/pricing/campaign-compat", token, map[string]any{"campaign_a": a.ID, "campaign_b": b.ID}, nil)
	h.expect(http.StatusUnprocessableEntity, http.MethodPost, "/api/v1/quotes", token, map[string]any{"room_type_id": 1, "rate_plan_id": 1, "check_in": "2027-03-10", "check_out": "2027-03-11", "adults": 2, "codes": []string{"AA", "BB"}}, nil)
	h.expect(http.StatusOK, http.MethodPut, fmt.Sprintf("/api/v1/pricing/campaigns/%d/active", a.ID), token, map[string]bool{"active": false}, nil)
	h.expect(http.StatusNoContent, http.MethodDelete, fmt.Sprintf("/api/v1/pricing/seasons/%d", season.ID), token, nil, nil)
	h.expect(http.StatusNoContent, http.MethodDelete, fmt.Sprintf("/api/v1/pricing/early-tiers/%d", tier.ID), token, nil, nil)
	h.expect(http.StatusOK, http.MethodPut, "/api/v1/pricing/settings", token, map[string]any{"weekend_bp": 12500, "child_free_max_age": 6, "child_max_age": 12, "kdv_bp": 1000, "accommodation_bp": 200}, nil)
	var count int
	if err := h.store.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE action LIKE 'pricing.%'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 10 {
		t.Fatalf("denetim kaydı eksik: %d", count)
	}
}

func TestReportsAggregateCorrectly(t *testing.T) {
	h := setup(t)
	if _, err := h.store.Pool.Exec(ctx, `INSERT INTO daily_stats VALUES ('2027-01-01', 100, 80, 8000000), ('2027-01-02', 100, 60, 6000000), ('2027-02-01', 100, 50, 7500000)`); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Points []struct {
			Date string `json:"date"`
		} `json:"points"`
		Summary struct {
			OccupancyPct float64 `json:"occupancy_pct"`
			ADRKurus     int64   `json:"adr_kurus"`
			RevPARKurus  int64   `json:"revpar_kurus"`
		} `json:"summary"`
	}
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/reports/kpi?from=2027-01-01&to=2027-03-01&bucket=month", h.tokens["revenue"], nil, &report)
	if len(report.Points) != 2 || report.Summary.ADRKurus != 113157 || report.Summary.RevPARKurus != 71666 || report.Summary.OccupancyPct != 63.3 {
		t.Fatalf("rapor: %+v", report)
	}
}

func TestChannelEndpointVerifiesSignature(t *testing.T) {
	h := setup(t)
	event := channel.Event{EventID: "e1", ExternalRef: "R1", Seq: 1, Type: "create", RoomType: "STD", CheckIn: "2027-03-10", CheckOut: "2027-03-12", Adults: 2, GuestName: "OTA", NetKurus: 100000}
	raw, _ := json.Marshal(event)
	status, _, _ := h.raw(http.MethodPost, "/api/v1/channels/booking/events", "", json.RawMessage(raw), map[string]string{"X-Channel-Signature": "bad"})
	if status != http.StatusUnauthorized {
		t.Fatalf("imzasız: %d", status)
	}
	status, _, _ = h.raw(http.MethodPost, "/api/v1/channels/booking/events", "", json.RawMessage(raw), map[string]string{"X-Channel-Signature": channel.Sign("s3cret", raw)})
	if status != http.StatusAccepted {
		t.Fatalf("imzalı: %d", status)
	}
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/channels", h.tokens["manager"], nil, nil)
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/channels/inbox", h.tokens["manager"], nil, nil)
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/channels/outbox?status=pending", h.tokens["manager"], nil, nil)
}

func TestWalkAndOverbookingOverHTTP(t *testing.T) {
	h := setup(t)
	var last int64
	for i := 0; i < 6; i++ {
		last = h.book(h.tokens["reservations"], "2027-03-20", "2027-03-21")
	}
	var overview struct {
		Nights     []json.RawMessage `json:"nights"`
		Candidates []json.RawMessage `json:"candidates"`
	}
	h.expect(http.StatusOK, http.MethodGet, "/api/v1/overbooking", h.tokens["reception"], nil, &overview)
	if len(overview.Nights) != 1 || len(overview.Candidates) != 6 {
		t.Fatalf("liste: %d %d", len(overview.Nights), len(overview.Candidates))
	}
	h.expect(http.StatusCreated, http.MethodPost, fmt.Sprintf("/api/v1/reservations/%d/walk", last), h.tokens["reception"], map[string]any{"partner_hotel_id": 1, "reason": "dolu"}, nil)
	h.expect(http.StatusUnprocessableEntity, http.MethodPost, "/api/v1/reservations", h.tokens["guest"], map[string]any{
		"room_type_id": 2, "rate_plan_id": 1, "check_in": "2027-02-01", "check_out": "2027-02-02", "adults": 2,
	}, nil)
}
