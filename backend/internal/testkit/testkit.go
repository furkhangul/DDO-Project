package testkit

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"konakpro/internal/db/migrations"
	"konakpro/internal/platform/store"
)

var (
	templateOnce sync.Once
	templateErr  error
	counter      atomic.Int64
)

const template = "konak_tpl"

func withDatabase(raw, name string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func admin(ctx context.Context, raw, statement string) error {
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, statement)
	return err
}

func prepareTemplate(ctx context.Context, raw string) error {
	lock, err := pgx.Connect(ctx, raw)
	if err != nil {
		return err
	}
	defer lock.Close(ctx)
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(991)"); err != nil {
		return err
	}
	defer func() {
		_, _ = lock.Exec(ctx, "SELECT pg_advisory_unlock(991)")
	}()
	var exists bool
	if err := lock.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", template).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := lock.Exec(ctx, "CREATE DATABASE "+template); err != nil {
			return err
		}
	}
	return migrations.Up(withDatabase(raw, template))
}

func Fresh(t *testing.T) *store.Store {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL tanımlı değil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	templateOnce.Do(func() { templateErr = prepareTemplate(ctx, raw) })
	if templateErr != nil {
		t.Fatalf("şablon veritabanı: %v", templateErr)
	}
	name := fmt.Sprintf("t_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), counter.Add(1))
	if err := admin(ctx, raw, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, template)); err != nil {
		t.Fatalf("test veritabanı: %v", err)
	}
	pool, err := store.Connect(ctx, withDatabase(raw, name), 40)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	t.Cleanup(func() {
		pool.Close()
		clean, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = admin(clean, raw, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
	})
	if err := Fixture(ctx, st); err != nil {
		t.Fatalf("fikstür: %v", err)
	}
	return st
}

const Day0 = "2027-03-01"

func Fixture(ctx context.Context, st *store.Store) error {
	statements := []string{
		`INSERT INTO hotel_state (name, business_date) VALUES ('Test', '` + Day0 + `')`,
		`INSERT INTO users (email, password_hash, full_name, role) VALUES ('m@t.co', 'x', 'Müdür', 'manager'), ('g@t.co', 'x', 'Misafir', 'guest')`,
		`INSERT INTO pricing_settings (weekend_bp, child_free_max_age, child_max_age, kdv_bp, accommodation_bp) VALUES (12000, 6, 12, 1000, 200)`,
		`INSERT INTO rate_plans (code, name) VALUES ('BAR', 'Esnek')`,
		`INSERT INTO room_types (code, name, base_occupancy, max_occupancy, overbook_pct) VALUES ('STD', 'Standart', 2, 3, 20), ('SUI', 'Süit', 2, 4, 0)`,
		`INSERT INTO room_rates (room_type_id, rate_plan_id, base_kurus, extra_adult_kurus, child_kurus) VALUES (1, 1, 100000, 20000, 10000), (2, 1, 300000, 50000, 20000)`,
		`INSERT INTO rooms (number, room_type_id, floor, view) SELECT (100 + n)::text, 1, 1, 'deniz' FROM generate_series(1, 5) n`,
		`INSERT INTO rooms (number, room_type_id, floor, view) VALUES ('601', 2, 6, 'deniz')`,
		`INSERT INTO channels (code, name, secret) VALUES ('booking', 'Booking', 's3cret'), ('expedia', 'Expedia', 's3cret')`,
		`INSERT INTO partner_hotels (name, distance_km, nightly_cost_kurus) VALUES ('Liman', 1.0, 200000)`,
		`INSERT INTO guests (full_name, user_id) VALUES ('Test Misafir', 2)`,
	}
	for _, statement := range statements {
		if _, err := st.Pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("%s: %w", strings.Fields(statement)[2], err)
		}
	}
	return nil
}

func Date(t *testing.T, value string) time.Time {
	t.Helper()
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		t.Fatal(err)
	}
	return day
}
