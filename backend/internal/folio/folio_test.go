package folio_test

import (
	"context"
	"sync"
	"testing"

	"konakpro/internal/auditlog"
	"konakpro/internal/booking"
	"konakpro/internal/db"
	"konakpro/internal/folio"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
	"konakpro/internal/testkit"
)

var ctx = context.Background()

func actor() auditlog.Actor {
	id := int64(1)
	return auditlog.Actor{ID: &id, Label: "test"}
}

func code(err error) string {
	if app, ok := apperr.As(err); ok {
		return app.Code
	}
	return ""
}

func setup(t *testing.T) (*store.Store, *folio.Service, db.Folio) {
	st := testkit.Fresh(t)
	r, err := (&booking.Service{Store: st}).Create(ctx, actor(), booking.CreateRequest{
		Guest: &booking.NewGuest{FullName: "Ada"}, RoomTypeID: 1, RatePlanID: 1,
		CheckIn: testkit.Date(t, testkit.Day0), CheckOut: testkit.Date(t, "2027-03-04"), Adults: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.PrimaryFolio(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return st, &folio.Service{Store: st}, f
}

func assertIntegrity(t *testing.T, st *store.Store) {
	t.Helper()
	bad, err := st.FolioIntegrityViolations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("%d folyoda bakiye kalemlerle uyuşmuyor", bad)
	}
}

func balance(t *testing.T, st *store.Store, id int64) int64 {
	t.Helper()
	f, err := st.GetFolio(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return f.BalanceKurus
}

func TestChargeAndPartialPayment(t *testing.T) {
	st, svc, f := setup(t)
	if _, err := svc.Charge(ctx, actor(), f.ID, 100000, "Minibar"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pay(ctx, actor(), f.ID, 30000, "kart"); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, st, f.ID); got != 70000 {
		t.Fatalf("bakiye %d", got)
	}
	assertIntegrity(t, st)
}

func TestConcurrentPostingsKeepBalanceExact(t *testing.T) {
	st, svc, f := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = svc.Charge(ctx, actor(), f.ID, 1500, "Servis")
			} else {
				_, err = svc.Pay(ctx, actor(), f.ID, 1000, "nakit")
			}
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if got := balance(t, st, f.ID); got != 40*1500-40*1000 {
		t.Fatalf("bakiye %d", got)
	}
	assertIntegrity(t, st)
}

func TestRefundCannotExceedPayment(t *testing.T) {
	st, svc, f := setup(t)
	payment, err := svc.Pay(ctx, actor(), f.ID, 50000, "kart")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refund(ctx, actor(), f.ID, payment.ID, 30000); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refund(ctx, actor(), f.ID, payment.ID, 20001); code(err) != "REFUND_EXCEEDS_PAYMENT" {
		t.Fatalf("hata: %v", err)
	}
	if _, err := svc.Refund(ctx, actor(), f.ID, payment.ID, 20000); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, st, f.ID); got != 0 {
		t.Fatalf("bakiye %d", got)
	}
	assertIntegrity(t, st)
}

func TestConcurrentRefundsNeverOverRefund(t *testing.T) {
	st, svc, f := setup(t)
	payment, err := svc.Pay(ctx, actor(), f.ID, 10000, "kart")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.Refund(ctx, actor(), f.ID, payment.ID, 4000)
		}()
	}
	wg.Wait()
	refunded, err := st.RefundedAmount(ctx, &payment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refunded != 8000 {
		t.Fatalf("iade toplamı %d", refunded)
	}
}

func TestSplitByPercentToCompanyConservesTotal(t *testing.T) {
	st, svc, f := setup(t)
	if _, err := svc.Charge(ctx, actor(), f.ID, 100001, "Toplantı"); err != nil {
		t.Fatal(err)
	}
	view, err := svc.Split(ctx, actor(), f.ID, folio.SplitRequest{PayerType: "company", PayerName: "ACME A.Ş.", Percent: 60})
	if err != nil {
		t.Fatal(err)
	}
	if view.Folio.BalanceKurus != 60001 || balance(t, st, f.ID) != 40000 {
		t.Fatalf("bölme: %d / %d", view.Folio.BalanceKurus, balance(t, st, f.ID))
	}
	assertIntegrity(t, st)
}

func TestMovedLineCannotMoveTwice(t *testing.T) {
	st, svc, f := setup(t)
	line, err := svc.Charge(ctx, actor(), f.ID, 5000, "Kahvaltı")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.Split(ctx, actor(), f.ID, folio.SplitRequest{PayerType: "company", PayerName: "ACME", LineIDs: []int64{line.ID}})
	if err != nil {
		t.Fatal(err)
	}
	target := view.Folio.ID
	if _, err := svc.Split(ctx, actor(), f.ID, folio.SplitRequest{TargetFolioID: &target, LineIDs: []int64{line.ID}}); code(err) != "LINE_ALREADY_MOVED" {
		t.Fatalf("hata: %v", err)
	}
	assertIntegrity(t, st)
}

func TestSplitValidations(t *testing.T) {
	_, svc, f := setup(t)
	if _, err := svc.Charge(ctx, actor(), f.ID, 1000, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Split(ctx, actor(), f.ID, folio.SplitRequest{PayerType: "company", PayerName: "A", AmountKurus: 2000}); code(err) != "SPLIT_EXCEEDS_BALANCE" {
		t.Fatalf("hata: %v", err)
	}
	if _, err := svc.Split(ctx, actor(), f.ID, folio.SplitRequest{PayerType: "x", PayerName: "A", AmountKurus: 1}); code(err) != "VALIDATION_ERROR" {
		t.Fatalf("hata: %v", err)
	}
	id := f.ID
	if _, err := svc.Split(ctx, actor(), f.ID, folio.SplitRequest{TargetFolioID: &id, AmountKurus: 1}); code(err) != "VALIDATION_ERROR" {
		t.Fatalf("hata: %v", err)
	}
}

func TestFolioLinesAreImmutableAndBalanceGuarded(t *testing.T) {
	st, svc, f := setup(t)
	if _, err := svc.Charge(ctx, actor(), f.ID, 1000, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE folio_lines SET amount_kurus = 1"); err == nil {
		t.Fatal("kalem değiştirilebildi")
	}
	if _, err := st.Pool.Exec(ctx, "DELETE FROM folio_lines"); err == nil {
		t.Fatal("kalem silinebildi")
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE folios SET balance_kurus = 0"); err == nil {
		t.Fatal("bakiye elle değiştirilebildi")
	}
}

func TestClosedFolioRejectsPostings(t *testing.T) {
	st, svc, f := setup(t)
	if err := st.CloseFolios(ctx, f.ReservationID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Charge(ctx, actor(), f.ID, 1000, "x"); code(err) != "FOLIO_CLOSED" {
		t.Fatalf("hata: %v", err)
	}
}

func TestInvalidAmounts(t *testing.T) {
	_, svc, f := setup(t)
	if _, err := svc.Pay(ctx, actor(), f.ID, 0, "kart"); code(err) != "VALIDATION_ERROR" {
		t.Fatalf("hata: %v", err)
	}
	if _, err := svc.Pay(ctx, actor(), f.ID, 10, "bitcoin"); code(err) != "VALIDATION_ERROR" {
		t.Fatalf("hata: %v", err)
	}
	if _, err := svc.Charge(ctx, actor(), f.ID, 0, "x"); code(err) != "VALIDATION_ERROR" {
		t.Fatalf("hata: %v", err)
	}
}
