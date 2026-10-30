package booking_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"konakpro/internal/auditlog"
	"konakpro/internal/booking"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
	"konakpro/internal/testkit"
)

var ctx = context.Background()

func actor() auditlog.Actor {
	id := int64(1)
	return auditlog.Actor{ID: &id, Label: "test"}
}

func request(t *testing.T, typeID int64, from, to string) booking.CreateRequest {
	return booking.CreateRequest{
		Guest: &booking.NewGuest{FullName: "Ada Lovelace"}, RoomTypeID: typeID, RatePlanID: 1,
		CheckIn: testkit.Date(t, from), CheckOut: testkit.Date(t, to), Adults: 2,
	}
}

func code(err error) string {
	if app, ok := apperr.As(err); ok {
		return app.Code
	}
	return ""
}

func sold(t *testing.T, st *store.Store, typeID int64, day string) int {
	t.Helper()
	var value int
	if err := st.Pool.QueryRow(ctx, "SELECT sold FROM inventory WHERE room_type_id=$1 AND stay_date=$2", typeID, day).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestConcurrentBookingNeverExceedsStock(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	const attempts = 300
	var ok, full atomic.Int64
	var other atomic.Value
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-12"))
			switch {
			case err == nil:
				ok.Add(1)
			case code(err) == "SOLD_OUT":
				full.Add(1)
			default:
				other.Store(err)
			}
		}()
	}
	wg.Wait()
	if value := other.Load(); value != nil {
		t.Fatalf("beklenmeyen hata: %v", value)
	}
	if ok.Load() != 6 || full.Load() != attempts-6 {
		t.Fatalf("başarılı=%d dolu=%d, beklenen 6 (5 oda + %%20 overbooking)", ok.Load(), full.Load())
	}
	if value := sold(t, st, 1, "2027-03-11"); value != 6 {
		t.Fatalf("stok sayacı %d", value)
	}
}

func TestDatabaseRejectsOversellEvenWithoutService(t *testing.T) {
	st := testkit.Fresh(t)
	if _, err := (&booking.Service{Store: st}).Create(ctx, actor(), request(t, 2, "2027-03-10", "2027-03-11")); err != nil {
		t.Fatal(err)
	}
	_, err := st.Pool.Exec(ctx, "UPDATE inventory SET sold = sold + 1 WHERE room_type_id = 2 AND stay_date = '2027-03-10'")
	if !store.IsCheck(err) || store.ConstraintName(err) != "inventory_not_oversold" {
		t.Fatalf("CHECK kısıtı devreye girmedi: %v", err)
	}
}

func TestOverlappingRoomAssignmentRejectedByExclude(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	a, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-13"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-12", "2027-03-14"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertAssignment(ctx, db.InsertAssignmentParams{ReservationID: a.ID, RoomID: 1, DateFrom: a.CheckIn, DateTo: a.CheckOut}); err != nil {
		t.Fatal(err)
	}
	err = st.InsertAssignment(ctx, db.InsertAssignmentParams{ReservationID: b.ID, RoomID: 1, DateFrom: b.CheckIn, DateTo: b.CheckOut})
	if !store.IsExclusion(err) {
		t.Fatalf("EXCLUDE devreye girmedi: %v", err)
	}
}

func TestCheckoutDayDoesNotBlockNextArrival(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	first, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-10", "2027-03-12"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-12", "2027-03-14"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []db.Reservation{first, second} {
		if err := st.InsertAssignment(ctx, db.InsertAssignmentParams{ReservationID: r.ID, RoomID: 6, DateFrom: r.CheckIn, DateTo: r.CheckOut}); err != nil {
			t.Fatalf("yarı açık aralık çalışmıyor: %v", err)
		}
	}
}

func TestCancelReleasesStock(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	r, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-10", "2027-03-11"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-10", "2027-03-11")); code(err) != "SOLD_OUT" {
		t.Fatalf("dolu olmalıydı: %v", err)
	}
	if err := svc.Cancel(ctx, actor(), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-10", "2027-03-11")); err != nil {
		t.Fatalf("iptal sonrası satılamadı: %v", err)
	}
	if err := svc.Cancel(ctx, actor(), r.ID); code(err) != "INVALID_STATE" {
		t.Fatalf("ikinci iptal: %v", err)
	}
}

func TestWalkOnlyWhenOverbookedAndReleasesStock(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	var last db.Reservation
	var err error
	for i := 0; i < 5; i++ {
		last, err = svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-12"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Walk(ctx, actor(), last.ID, booking.WalkRequest{PartnerHotelID: 1, Reason: "x"}); code(err) != "NOT_OVERBOOKED" {
		t.Fatalf("fazla satış yokken walk: %v", err)
	}
	overbooked, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-12"))
	if err != nil {
		t.Fatal(err)
	}
	walk, err := svc.Walk(ctx, actor(), overbooked.ID, booking.WalkRequest{PartnerHotelID: 1, Reason: "Otel dolu"})
	if err != nil {
		t.Fatal(err)
	}
	if walk.CostKurus != 400000 || walk.Nights != 2 {
		t.Fatalf("walk maliyeti: %+v", walk)
	}
	if value := sold(t, st, 1, "2027-03-10"); value != 5 {
		t.Fatalf("stok: %d", value)
	}
}

func TestStoredPriceSurvivesRuleChange(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	r, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-12"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE room_rates SET base_kurus = base_kurus * 3; UPDATE pricing_settings SET kdv_bp = 2000"); err != nil {
		t.Fatal(err)
	}
	check, err := svc.VerifyPrice(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Matches || check.Replayed.NetKurus != r.TotalNetKurus {
		t.Fatalf("garanti fiyat bozuldu: %+v", check)
	}
	fresh, err := svc.Quote(ctx, booking.QuoteRequest{RoomTypeID: 1, RatePlanID: 1, CheckIn: r.CheckIn, CheckOut: r.CheckOut, Adults: 2})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.NetKurus == r.TotalNetKurus {
		t.Fatal("yeni teklif eski fiyatla aynı olmamalı")
	}
}

func TestStaleQuoteRejected(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	req := request(t, 1, "2027-03-10", "2027-03-12")
	req.QuoteHash = "eski"
	if _, err := svc.Create(ctx, actor(), req); code(err) != "PRICE_CHANGED" {
		t.Fatalf("hata: %v", err)
	}
}

func TestCampaignUsageLimitUnderConcurrency(t *testing.T) {
	st := testkit.Fresh(t)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO campaigns (code, name, discount_bp, valid_stay, max_uses, with_early_booking)
		VALUES ('TEK', 'Tek', 1000, '[2027-01-01,2028-01-01)', 2, true)`); err != nil {
		t.Fatal(err)
	}
	svc := &booking.Service{Store: st}
	var ok atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := request(t, 1, fmt.Sprintf("2027-04-%02d", i+1), fmt.Sprintf("2027-04-%02d", i+2))
			req.Codes = []string{"TEK"}
			if _, err := svc.Create(ctx, actor(), req); err == nil {
				ok.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 2 {
		t.Fatalf("limit aşıldı: %d", ok.Load())
	}
}

func TestModifyMovesStockAtomically(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	r, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-10", "2027-03-12"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-20", "2027-03-21")); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Modify(ctx, actor(), r.ID, booking.ModifyRequest{RoomTypeID: 2, CheckIn: testkit.Date(t, "2027-03-20"), CheckOut: testkit.Date(t, "2027-03-22"), Adults: 2})
	if code(err) != "SOLD_OUT" {
		t.Fatalf("dolu tarihe değişiklik: %v", err)
	}
	if value := sold(t, st, 2, "2027-03-10"); value != 1 {
		t.Fatalf("başarısız değişiklik stoğu bozdu: %d", value)
	}
	updated, err := svc.Modify(ctx, actor(), r.ID, booking.ModifyRequest{RoomTypeID: 2, CheckIn: testkit.Date(t, "2027-03-11"), CheckOut: testkit.Date(t, "2027-03-14"), Adults: 2})
	if err != nil {
		t.Fatal(err)
	}
	if value := sold(t, st, 2, "2027-03-10"); value != 0 {
		t.Fatalf("eski gece bırakılmadı: %d", value)
	}
	if !updated.CheckOut.Equal(testkit.Date(t, "2027-03-14")) {
		t.Fatalf("tarih: %v", updated.CheckOut)
	}
}

func TestCheckInRules(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	future, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-11"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckIn(ctx, actor(), future.ID, nil); code(err) != "NOT_ARRIVAL_DAY" {
		t.Fatalf("hata: %v", err)
	}
	today, err := svc.Create(ctx, actor(), request(t, 1, testkit.Day0, "2027-03-03"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE rooms SET hk_status = 'dirty' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	room := int64(1)
	if err := svc.CheckIn(ctx, actor(), today.ID, &room); code(err) != "ROOM_NOT_READY" {
		t.Fatalf("kirli oda: %v", err)
	}
	if err := svc.CheckIn(ctx, actor(), today.ID, nil); err != nil {
		t.Fatalf("hazır oda seçilemedi: %v", err)
	}
	segments, err := st.ListAssignmentsForReservation(ctx, today.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].RoomID == 1 || !segments[0].Locked {
		t.Fatalf("atama: %+v", segments)
	}
}

func TestEarlyCheckoutReleasesUnusedNights(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	r, err := svc.Create(ctx, actor(), request(t, 2, "2027-03-01", "2027-03-05"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckIn(ctx, actor(), r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckOut(ctx, actor(), r.ID); code(err) != "SAME_DAY_CHECKOUT" {
		t.Fatalf("aynı gün çıkış: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE hotel_state SET business_date = '2027-03-03'"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckOut(ctx, actor(), r.ID); err != nil {
		t.Fatal(err)
	}
	if value := sold(t, st, 2, "2027-03-02"); value != 1 {
		t.Fatalf("kullanılan gece: %d", value)
	}
	if value := sold(t, st, 2, "2027-03-03"); value != 0 {
		t.Fatalf("boşalan gece: %d", value)
	}
	nights, err := st.ListReservationNights(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nights) != 2 {
		t.Fatalf("gece sayısı: %d", len(nights))
	}
	room, err := st.GetRoom(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if room.HkStatus != db.HkStatusDirty {
		t.Fatalf("oda kirli olmalıydı: %s", room.HkStatus)
	}
}

func TestPastArrivalRejected(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	if _, err := svc.Create(ctx, actor(), request(t, 1, "2027-02-01", "2027-02-03")); code(err) != "ARRIVAL_IN_PAST" {
		t.Fatalf("hata: %v", err)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	st := testkit.Fresh(t)
	if _, err := (&booking.Service{Store: st}).Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-11")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE audit_log SET action = 'x'"); err == nil {
		t.Fatal("denetim kaydı değiştirilebildi")
	}
	if _, err := st.Pool.Exec(ctx, "DELETE FROM audit_log"); err == nil {
		t.Fatal("denetim kaydı silinebildi")
	}
}

func TestChannelOutboxQueuedInSameTransaction(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	if _, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-12")); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM channel_outbox WHERE status='pending'").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 4 {
		t.Fatalf("2 gece × 2 kanal bekleniyordu: %d", pending)
	}
	if _, err := svc.Create(ctx, actor(), request(t, 1, "2027-03-10", "2027-03-12")); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM channel_outbox WHERE status='pending'").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 4 {
		t.Fatalf("birleştirme çalışmadı: %d", pending)
	}
}
