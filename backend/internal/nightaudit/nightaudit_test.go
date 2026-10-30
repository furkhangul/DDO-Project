package nightaudit_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"konakpro/internal/auditlog"
	"konakpro/internal/booking"
	"konakpro/internal/nightaudit"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
	"konakpro/internal/testkit"
)

var ctx = context.Background()

func actor() auditlog.Actor {
	id := int64(1)
	return auditlog.Actor{ID: &id, Label: "test"}
}

func service(st *store.Store) *nightaudit.Service {
	return &nightaudit.Service{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), AllowFaults: true}
}

func inHouse(t *testing.T, st *store.Store, count int) []int64 {
	t.Helper()
	svc := &booking.Service{Store: st}
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		r, err := svc.Create(ctx, actor(), booking.CreateRequest{
			Guest: &booking.NewGuest{FullName: "Konuk"}, RoomTypeID: 1, RatePlanID: 1,
			CheckIn: testkit.Date(t, testkit.Day0), CheckOut: testkit.Date(t, "2027-03-04"), Adults: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.CheckIn(ctx, actor(), r.ID, nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	return ids
}

func count(t *testing.T, st *store.Store, sql string, args ...any) int {
	t.Helper()
	var value int
	if err := st.Pool.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestChargesEveryInHouseGuestAndAdvancesDate(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 3)
	report, err := service(st).Run(ctx, actor(), nightaudit.Fault{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Charged != 3 || report.NextDate != "2027-03-02" {
		t.Fatalf("rapor: %+v", report)
	}
	if lines := count(t, st, "SELECT count(*) FROM folio_lines WHERE kind IN ('room','tax_kdv','tax_accom')"); lines != 9 {
		t.Fatalf("satır sayısı %d", lines)
	}
	var net, kdv, accom int64
	if err := st.Pool.QueryRow(ctx, `SELECT sum(amount_kurus) FILTER (WHERE kind='room'), sum(amount_kurus) FILTER (WHERE kind='tax_kdv'), sum(amount_kurus) FILTER (WHERE kind='tax_accom') FROM folio_lines`).Scan(&net, &kdv, &accom); err != nil {
		t.Fatal(err)
	}
	if net != 300000 || kdv != 30000 || accom != 6000 {
		t.Fatalf("tutarlar: %d %d %d", net, kdv, accom)
	}
}

func TestCrashMidRunResumesWithoutDoubleCharge(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 5)
	svc := service(st)
	if _, err := svc.Run(ctx, actor(), nightaudit.Fault{CrashAfter: 2}); !errors.Is(err, nightaudit.ErrInjected) {
		t.Fatalf("hata: %v", err)
	}
	if lines := count(t, st, "SELECT count(*) FROM folio_lines WHERE kind='room'"); lines != 2 {
		t.Fatalf("yarıda kalan satır: %d", lines)
	}
	state, err := st.GetHotelState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.BusinessDate.Format("2006-01-02") != testkit.Day0 {
		t.Fatal("yarıda kalan denetim tarihi ilerletmemeli")
	}
	report, err := svc.Run(ctx, actor(), nightaudit.Fault{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Resumed || report.Charged != 3 {
		t.Fatalf("devam raporu: %+v", report)
	}
	if lines := count(t, st, "SELECT count(*) FROM folio_lines WHERE kind='room'"); lines != 5 {
		t.Fatalf("toplam satır: %d", lines)
	}
	duplicates, err := st.DuplicateNightCharges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if duplicates != 0 {
		t.Fatalf("çift ücret: %d", duplicates)
	}
	bad, err := st.FolioIntegrityViolations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("bütünlük: %d", bad)
	}
}

func TestDatabaseRejectsSecondNightCharge(t *testing.T) {
	st := testkit.Fresh(t)
	ids := inHouse(t, st, 1)
	if _, err := service(st).Run(ctx, actor(), nightaudit.Fault{}); err != nil {
		t.Fatal(err)
	}
	_, err := st.Pool.Exec(ctx, `INSERT INTO folio_lines (folio_id, reservation_id, kind, amount_kurus, description, business_date)
		SELECT f.id, f.reservation_id, 'room', 1, 'tekrar', $2 FROM folios f WHERE f.reservation_id = $1`, ids[0], testkit.Day0)
	if !store.IsUnique(err) {
		t.Fatalf("benzersiz kısıt devreye girmedi: %v", err)
	}
}

func TestConcurrentRunsAreRejected(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 4)
	svc := service(st)
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, busy := 0, 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Run(ctx, actor(), nightaudit.Fault{ItemDelay: 30 * 1e6})
			mu.Lock()
			defer mu.Unlock()
			if app, ok := apperr.As(err); ok && app.Code == "AUDIT_RUNNING" {
				busy++
			} else if err == nil {
				succeeded++
			} else {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if succeeded != 1 || busy != 1 {
		t.Fatalf("başarılı=%d meşgul=%d", succeeded, busy)
	}
	if lines := count(t, st, "SELECT count(*) FROM folio_lines WHERE kind='room'"); lines != 4 {
		t.Fatalf("satır %d", lines)
	}
}

func TestNoShowReleasesInventory(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	if _, err := svc.Create(ctx, actor(), booking.CreateRequest{
		Guest: &booking.NewGuest{FullName: "Gelmedi"}, RoomTypeID: 2, RatePlanID: 1,
		CheckIn: testkit.Date(t, testkit.Day0), CheckOut: testkit.Date(t, "2027-03-03"), Adults: 2,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := service(st).Run(ctx, actor(), nightaudit.Fault{})
	if err != nil {
		t.Fatal(err)
	}
	if report.NoShows != 1 {
		t.Fatalf("rapor: %+v", report)
	}
	if sold := count(t, st, "SELECT sold FROM inventory WHERE room_type_id = 2 AND stay_date = '2027-03-02'"); sold != 0 {
		t.Fatalf("stok bırakılmadı: %d", sold)
	}
}

func TestCheckoutDuringAuditIsSkipped(t *testing.T) {
	st := testkit.Fresh(t)
	ids := inHouse(t, st, 2)
	if _, err := st.Pool.Exec(ctx, "UPDATE reservations SET status = 'checked_out' WHERE id = $1", ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := service(st).Run(ctx, actor(), nightaudit.Fault{}); err != nil {
		t.Fatal(err)
	}
	if lines := count(t, st, "SELECT count(*) FROM folio_lines WHERE kind='room'"); lines != 1 {
		t.Fatalf("çıkış yapmış misafire ücret yazıldı: %d", lines)
	}
}

func TestFaultsIgnoredWhenDisabled(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 3)
	svc := service(st)
	svc.AllowFaults = false
	if _, err := svc.Run(ctx, actor(), nightaudit.Fault{CrashAfter: 1}); err != nil {
		t.Fatalf("üretimde hata enjeksiyonu çalışmamalı: %v", err)
	}
}

func TestDailyStatsWritten(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 2)
	if _, err := service(st).Run(ctx, actor(), nightaudit.Fault{}); err != nil {
		t.Fatal(err)
	}
	if sold := count(t, st, "SELECT rooms_sold FROM daily_stats WHERE stay_date = $1", testkit.Day0); sold != 2 {
		t.Fatalf("istatistik: %d", sold)
	}
}

func TestStatusReflectsRunningAndHistory(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 3)
	svc := service(st)
	_, current, history, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != nil || len(history) != 0 {
		t.Fatalf("başlangıç: %+v %+v", current, history)
	}
	if _, err := svc.Run(ctx, actor(), nightaudit.Fault{CrashAfter: 1}); !errors.Is(err, nightaudit.ErrInjected) {
		t.Fatalf("hata: %v", err)
	}
	_, current, _, err = svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != "running" || current.DoneItems != 1 || current.TotalItems != 3 || current.LastError == nil {
		t.Fatalf("yarıda kalan denetim: %+v", current)
	}
	if _, err := svc.Run(ctx, actor(), nightaudit.Fault{}); err != nil {
		t.Fatal(err)
	}
	state, current, history, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != nil || len(history) != 1 || history[0].Status != "completed" || history[0].Attempts != 2 {
		t.Fatalf("tamamlanan: %+v %+v", current, history)
	}
	if state.BusinessDate.Format("2006-01-02") != "2027-03-02" {
		t.Fatalf("gün: %v", state.BusinessDate)
	}
}

func TestStayoverTaskCreatedForNextDay(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 2)
	if _, err := service(st).Run(ctx, actor(), nightaudit.Fault{}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, "SELECT count(*) FROM hk_tasks WHERE task_type='konaklama_temizligi' AND business_date='2027-03-02'"); n != 2 {
		t.Fatalf("görev: %d", n)
	}
}

func TestChargeFailureRecordsErrorAndStaysResumable(t *testing.T) {
	st := testkit.Fresh(t)
	ids := inHouse(t, st, 2)
	if _, err := st.Pool.Exec(ctx, "DELETE FROM reservation_nights WHERE reservation_id = $1", ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "ALTER TABLE folio_lines ADD CONSTRAINT boom CHECK (description <> 'Oda ücreti 01.03.2027')"); err != nil {
		t.Fatal(err)
	}
	svc := service(st)
	if _, err := svc.Run(ctx, actor(), nightaudit.Fault{}); err == nil {
		t.Fatal("hata bekleniyordu")
	}
	run, err := st.GetAuditRun(ctx, testkit.Date(t, testkit.Day0))
	if err != nil {
		t.Fatal(err)
	}
	if run.LastError == nil || run.Status != "running" {
		t.Fatalf("hata kaydedilmedi: %+v", run)
	}
	if _, err := st.Pool.Exec(ctx, "ALTER TABLE folio_lines DROP CONSTRAINT boom"); err != nil {
		t.Fatal(err)
	}
	report, err := svc.Run(ctx, actor(), nightaudit.Fault{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Charged != 1 || !report.Resumed {
		t.Fatalf("devam: %+v", report)
	}
}

func TestOnItemChargedHookCalled(t *testing.T) {
	st := testkit.Fresh(t)
	inHouse(t, st, 2)
	svc := service(st)
	seen := 0
	svc.OnItemCharged = func() { seen++ }
	if _, err := svc.Run(ctx, actor(), nightaudit.Fault{ItemDelay: 1}); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("kanca %d", seen)
	}
}
