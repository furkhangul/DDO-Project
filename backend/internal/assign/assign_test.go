package assign_test

import (
	"context"
	"testing"

	"konakpro/internal/assign"
	"konakpro/internal/auditlog"
	"konakpro/internal/booking"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/testkit"
)

var ctx = context.Background()

func actor() auditlog.Actor {
	id := int64(1)
	return auditlog.Actor{ID: &id, Label: "test"}
}

func create(t *testing.T, svc *booking.Service, from, to string) db.Reservation {
	t.Helper()
	r, err := svc.Create(ctx, actor(), booking.CreateRequest{
		Guest: &booking.NewGuest{FullName: "Konuk"}, RoomTypeID: 1, RatePlanID: 1,
		CheckIn: testkit.Date(t, from), CheckOut: testkit.Date(t, to), Adults: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyPersistsPlanAndIsRepeatable(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	create(t, svc, "2027-03-02", "2027-03-05")
	create(t, svc, "2027-03-03", "2027-03-06")
	create(t, svc, "2027-03-05", "2027-03-08")
	solver := &assign.Service{Store: st, Seed: 1}
	from, to := testkit.Date(t, "2027-03-01"), testkit.Date(t, "2027-03-15")

	preview, err := solver.Solve(ctx, actor(), from, to, false)
	if err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM room_assignments").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if preview.Applied || stored != 0 {
		t.Fatal("önizleme kayıt yazmamalı")
	}
	for i := 0; i < 2; i++ {
		plan, err := solver.Solve(ctx, actor(), from, to, true)
		if err != nil {
			t.Fatal(err)
		}
		if !plan.Applied || plan.Result.Moves != 0 || len(plan.Unassigned) != 0 {
			t.Fatalf("plan: %+v", plan)
		}
	}
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM room_assignments").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 3 {
		t.Fatalf("atama %d", stored)
	}
}

func TestSolverRespectsLockedManualMove(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	a := create(t, svc, "2027-03-02", "2027-03-05")
	b := create(t, svc, "2027-03-03", "2027-03-06")
	solver := &assign.Service{Store: st, Seed: 1}
	if err := solver.Move(ctx, actor(), a.ID, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := solver.Solve(ctx, actor(), testkit.Date(t, "2027-03-01"), testkit.Date(t, "2027-03-15"), true); err != nil {
		t.Fatal(err)
	}
	segments, err := st.ListAssignmentsForReservation(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].RoomID != 5 {
		t.Fatalf("elle taşınan oda değişti: %+v", segments)
	}
	other, err := st.ListAssignmentsForReservation(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].RoomID == 5 {
		t.Fatalf("çakışan atama: %+v", other)
	}
}

func TestMoveRejectsTakenRoomAndWrongType(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	a := create(t, svc, "2027-03-02", "2027-03-05")
	b := create(t, svc, "2027-03-03", "2027-03-06")
	solver := &assign.Service{Store: st, Seed: 1}
	if err := solver.Move(ctx, actor(), a.ID, 1); err != nil {
		t.Fatal(err)
	}
	err := solver.Move(ctx, actor(), b.ID, 1)
	if app, ok := apperr.As(err); !ok || app.Code != "ROOM_TAKEN" {
		t.Fatalf("hata: %v", err)
	}
	err = solver.Move(ctx, actor(), b.ID, 6)
	if app, ok := apperr.As(err); !ok || app.Code != "ROOM_TYPE_MISMATCH" {
		t.Fatalf("hata: %v", err)
	}
}

func TestInHouseMoveKeepsOccupiedNights(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &booking.Service{Store: st}
	r := create(t, svc, testkit.Day0, "2027-03-05")
	if err := svc.CheckIn(ctx, actor(), r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "UPDATE hotel_state SET business_date = '2027-03-03'"); err != nil {
		t.Fatal(err)
	}
	if err := (&assign.Service{Store: st}).Move(ctx, actor(), r.ID, 3); err != nil {
		t.Fatal(err)
	}
	segments, err := st.ListAssignmentsForReservation(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 || segments[1].RoomID != 3 || segments[1].DateFrom.Format("2006-01-02") != "2027-03-03" {
		t.Fatalf("segmentler: %+v", segments)
	}
}

func TestHorizonValidated(t *testing.T) {
	st := testkit.Fresh(t)
	solver := &assign.Service{Store: st}
	if _, err := solver.Solve(ctx, actor(), testkit.Date(t, "2027-03-05"), testkit.Date(t, "2027-03-01"), false); err == nil {
		t.Fatal("ters aralık kabul edildi")
	}
	if _, err := solver.Solve(ctx, actor(), testkit.Date(t, "2027-03-05"), testkit.Date(t, "2028-03-01"), false); err == nil {
		t.Fatal("aşırı ufuk kabul edildi")
	}
}
