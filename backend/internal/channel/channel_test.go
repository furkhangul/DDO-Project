package channel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"konakpro/internal/channel"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
	"konakpro/internal/testkit"
)

var ctx = context.Background()

func event(id, ref string, seq int64, kind, from, to string) channel.Event {
	return channel.Event{
		EventID: id, ExternalRef: ref, Seq: seq, Type: kind, RoomType: "STD", CheckIn: from, CheckOut: to,
		Adults: 2, GuestName: "OTA Misafir", NetKurus: 200001,
	}
}

func send(t *testing.T, svc *channel.Service, e channel.Event) channel.Result {
	t.Helper()
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.Ingest(ctx, "booking", channel.Sign("s3cret", body), body)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func one(t *testing.T, st *store.Store, sql string, args ...any) int {
	t.Helper()
	var value int
	if err := st.Pool.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestDuplicateEventProcessedOnce(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &channel.Service{Store: st}
	e := event("e1", "R1", 1, "create", "2027-03-10", "2027-03-12")
	if got := send(t, svc, e); got.Outcome != channel.OutcomeApplied {
		t.Fatalf("sonuç %+v", got)
	}
	if got := send(t, svc, e); got.Outcome != channel.OutcomeDupe {
		t.Fatalf("sonuç %+v", got)
	}
	if n := one(t, st, "SELECT count(*) FROM reservations"); n != 1 {
		t.Fatalf("rezervasyon %d", n)
	}
	if n := one(t, st, "SELECT sold FROM inventory WHERE stay_date='2027-03-10' AND room_type_id=1"); n != 1 {
		t.Fatalf("stok %d", n)
	}
}

func TestConcurrentDuplicatesProcessOnce(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &channel.Service{Store: st}
	e := event("e1", "R1", 1, "create", "2027-03-10", "2027-03-12")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(e)
			if _, err := svc.Ingest(ctx, "booking", channel.Sign("s3cret", body), body); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := one(t, st, "SELECT count(*) FROM reservations"); n != 1 {
		t.Fatalf("rezervasyon %d", n)
	}
}

func TestOutOfOrderModifyBeforeCreate(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &channel.Service{Store: st}
	send(t, svc, event("e2", "R1", 2, "modify", "2027-03-10", "2027-03-14"))
	if got := send(t, svc, event("e1", "R1", 1, "create", "2027-03-10", "2027-03-12")); got.Outcome != channel.OutcomeStale {
		t.Fatalf("sonuç %+v", got)
	}
	if n := one(t, st, "SELECT (check_out - check_in) FROM reservations WHERE external_ref='R1'"); n != 4 {
		t.Fatalf("en güncel durum uygulanmadı: %d gece", n)
	}
}

func TestCancelBeforeCreateLeavesNoReservation(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &channel.Service{Store: st}
	send(t, svc, event("e3", "R1", 3, "cancel", "", ""))
	send(t, svc, event("e1", "R1", 1, "create", "2027-03-10", "2027-03-12"))
	send(t, svc, event("e2", "R1", 2, "modify", "2027-03-10", "2027-03-14"))
	if n := one(t, st, "SELECT count(*) FROM reservations"); n != 0 {
		t.Fatalf("iptal edilmiş rezervasyon oluştu: %d", n)
	}
}

func TestAnyArrivalOrderConvergesToSameState(t *testing.T) {
	events := []channel.Event{
		event("e1", "R1", 1, "create", "2027-03-10", "2027-03-12"),
		event("e2", "R1", 2, "modify", "2027-03-11", "2027-03-13"),
		event("e3", "R1", 3, "modify", "2027-03-11", "2027-03-16"),
	}
	for seed := int64(0); seed < 6; seed++ {
		st := testkit.Fresh(t)
		svc := &channel.Service{Store: st}
		shuffled := append([]channel.Event{}, events...)
		rand.New(rand.NewSource(seed)).Shuffle(3, func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for _, e := range append(shuffled, shuffled...) {
			send(t, svc, e)
		}
		if n := one(t, st, "SELECT (check_out - check_in) FROM reservations WHERE status='confirmed'"); n != 5 {
			t.Fatalf("tohum %d: %d gece", seed, n)
		}
		if n := one(t, st, "SELECT COALESCE(sum(sold),0)::int FROM inventory"); n != 5 {
			t.Fatalf("tohum %d: stok %d", seed, n)
		}
	}
}

func TestBadSignatureRejected(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &channel.Service{Store: st}
	_, err := svc.Ingest(ctx, "booking", "yanlis", []byte(`{}`))
	if app, ok := apperr.As(err); !ok || app.Status != http.StatusUnauthorized {
		t.Fatalf("hata: %v", err)
	}
}

func TestSoldOutBecomesConflict(t *testing.T) {
	st := testkit.Fresh(t)
	svc := &channel.Service{Store: st}
	first := event("e1", "R1", 1, "create", "2027-03-10", "2027-03-11")
	first.RoomType = "SUI"
	send(t, svc, first)
	second := event("e2", "R2", 1, "create", "2027-03-10", "2027-03-11")
	second.RoomType = "SUI"
	if got := send(t, svc, second); got.Outcome != channel.OutcomeConflict {
		t.Fatalf("sonuç %+v", got)
	}
	if n := one(t, st, "SELECT count(*) FROM channel_bookings WHERE state='conflict'"); n != 1 {
		t.Fatalf("çakışma kaydı %d", n)
	}
}

func worker(st *store.Store, url string) *channel.Worker {
	return &channel.Worker{
		Store: st, Client: &http.Client{Timeout: time.Second}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		BaseURL: url, Interval: time.Millisecond, Batch: 50, MaxAttempts: 3, Backoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
	}
}

func queue(t *testing.T, st *store.Store) {
	t.Helper()
	send(t, &channel.Service{Store: st}, event("e1", "R1", 1, "create", "2027-03-10", "2027-03-11"))
}

func TestWorkerDeliversCurrentAvailability(t *testing.T) {
	st := testkit.Fresh(t)
	var received atomic.Int64
	var lastAvailable atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ari channel.ARI
		_ = json.NewDecoder(r.Body).Decode(&ari)
		lastAvailable.Store(ari.Available)
		received.Add(1)
	}))
	defer server.Close()
	queue(t, st)
	if _, err := worker(st, server.URL).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 2 || lastAvailable.Load() != 5 {
		t.Fatalf("iletilen=%d müsait=%d", received.Load(), lastAvailable.Load())
	}
	if n := one(t, st, "SELECT count(*) FROM channel_outbox WHERE status='sent'"); n != 2 {
		t.Fatalf("gönderilen %d", n)
	}
}

func drain(t *testing.T, st *store.Store, w *channel.Worker) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for one(t, st, "SELECT count(*) FROM channel_outbox WHERE status IN ('pending','sending')") > 0 {
		if time.Now().After(deadline) {
			t.Fatal("kuyruk boşalmadı")
		}
		if _, err := w.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRetriesThenSucceeds(t *testing.T) {
	st := testkit.Fresh(t)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	queue(t, st)
	drain(t, st, worker(st, server.URL))
	if n := one(t, st, "SELECT count(*) FROM channel_outbox WHERE status='sent'"); n != 2 {
		t.Fatalf("gönderilen %d", n)
	}
	if n := one(t, st, "SELECT count(*) FROM channel_outbox WHERE status='dead'"); n != 0 {
		t.Fatalf("ölü %d", n)
	}
}

func TestExhaustedMessagesGoToDeadLetterAndCanRetry(t *testing.T) {
	st := testkit.Fresh(t)
	var healthy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()
	queue(t, st)
	w := worker(st, server.URL)
	drain(t, st, w)
	if n := one(t, st, "SELECT count(*) FROM channel_outbox WHERE status='dead' AND attempts=3"); n != 2 {
		t.Fatalf("ölü %d", n)
	}
	healthy.Store(true)
	id := int64(one(t, st, "SELECT min(id)::int FROM channel_outbox"))
	changed, err := st.RetryDeadOutbox(ctx, id)
	if err != nil || changed != 1 {
		t.Fatalf("yeniden deneme: %d %v", changed, err)
	}
	drain(t, st, w)
	if n := one(t, st, "SELECT count(*) FROM channel_outbox WHERE status='sent'"); n != 1 {
		t.Fatalf("gönderilen %d", n)
	}
}

func TestClaimSkipsLockedRowsForParallelWorkers(t *testing.T) {
	st := testkit.Fresh(t)
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received.Add(1) }))
	defer server.Close()
	svc := &channel.Service{Store: st}
	for i := 0; i < 10; i++ {
		send(t, svc, event(fmt.Sprintf("e%d", i), fmt.Sprintf("R%d", i), 1, "create", "2027-04-01", "2027-04-05"))
	}
	total := one(t, st, "SELECT count(*) FROM channel_outbox WHERE status='pending'")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := worker(st, server.URL).Tick(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if received.Load() != int64(total) {
		t.Fatalf("iletilen %d, beklenen %d", received.Load(), total)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	base, limit := 100*time.Millisecond, time.Second
	first := channel.Delay(base, limit, 1, 7)
	third := channel.Delay(base, limit, 3, 7)
	last := channel.Delay(base, limit, 30, 7)
	if first < base || first > base*5/4 {
		t.Fatalf("ilk gecikme %v", first)
	}
	if third < 4*base {
		t.Fatalf("üçüncü gecikme %v", third)
	}
	if last < limit || last > limit*5/4 {
		t.Fatalf("üst sınır %v", last)
	}
}
