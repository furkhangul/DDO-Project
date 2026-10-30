package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var tables = []string{
	"audit_log", "idempotency_keys", "refresh_tokens", "hotel_state", "users", "guests", "room_types", "rooms", "room_adjacency",
	"inventory", "channels", "partner_hotels", "pricing_settings", "rate_plans", "room_rates", "seasons", "early_booking_tiers",
	"campaigns", "campaign_compat", "reservations", "reservation_nights", "room_assignments", "reservation_campaigns", "walks",
	"folios", "folio_lines", "night_audit_runs", "night_audit_items", "daily_stats", "hk_tasks", "channel_inbox", "channel_bookings",
	"channel_outbox", "seed_meta",
}

func Reset(ctx context.Context, pool *pgxpool.Pool) error {
	sql := "TRUNCATE "
	for i, table := range tables {
		if i > 0 {
			sql += ", "
		}
		sql += table
	}
	_, err := pool.Exec(ctx, sql+" RESTART IDENTITY CASCADE")
	return err
}

func Seeded(ctx context.Context, pool *pgxpool.Pool) (bool, string, error) {
	var digest string
	err := pool.QueryRow(ctx, "SELECT digest FROM seed_meta WHERE id = 1").Scan(&digest)
	if err == pgx.ErrNoRows {
		return false, "", nil
	}
	return err == nil, digest, err
}

func Load(ctx context.Context, pool *pgxpool.Pool, d *Dataset, channelSecret string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := loadReference(ctx, tx, d, channelSecret); err != nil {
			return err
		}
		if err := loadStays(ctx, tx, d); err != nil {
			return err
		}
		if err := loadFolios(ctx, tx, d); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO seed_meta (seed, anchor, digest) VALUES ($1, $2, $3)", d.Seed, d.Anchor, d.Digest); err != nil {
			return err
		}
		for _, table := range []string{"users", "guests", "rooms", "room_types", "reservations", "folios", "channels", "rate_plans"} {
			if _, err := tx.Exec(ctx, fmt.Sprintf("SELECT setval(pg_get_serial_sequence('%s', 'id'), (SELECT max(id) FROM %s))", table, table)); err != nil {
				return err
			}
		}
		return nil
	})
}

func copyRows(ctx context.Context, tx pgx.Tx, table string, columns []string, count int, row func(i int) []any) error {
	_, err := tx.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromSlice(count, func(i int) ([]any, error) {
		return row(i), nil
	}))
	if err != nil {
		return fmt.Errorf("%s: %w", table, err)
	}
	return nil
}

func loadReference(ctx context.Context, tx pgx.Tx, d *Dataset, secret string) error {
	steps := []func() error{
		func() error {
			_, err := tx.Exec(ctx, "INSERT INTO hotel_state (name, business_date) VALUES ('Konak Palas Otel', $1)", d.Anchor)
			return err
		},
		func() error {
			return copyRows(ctx, tx, "users", []string{"id", "email", "password_hash", "full_name", "role"}, len(d.Users), func(i int) []any {
				u := d.Users[i]
				return []any{u.ID, u.Email, u.Hash, u.Name, u.Role}
			})
		},
		func() error {
			return copyRows(ctx, tx, "room_types", []string{"id", "code", "name", "description", "base_occupancy", "max_occupancy", "overbook_pct"}, len(d.Types), func(i int) []any {
				t := d.Types[i]
				return []any{t.ID, t.Code, t.Name, t.Description, t.BaseOccupancy, t.MaxOccupancy, t.OverbookPct}
			})
		},
		func() error {
			return copyRows(ctx, tx, "rooms", []string{"id", "number", "room_type_id", "floor", "view", "hk_status"}, len(d.Rooms), func(i int) []any {
				r := d.Rooms[i]
				return []any{r.ID, r.Number, r.TypeID, r.Floor, r.View, r.Status}
			})
		},
		func() error {
			return copyRows(ctx, tx, "room_adjacency", []string{"room_a", "room_b"}, len(d.Adjacent), func(i int) []any {
				return []any{d.Adjacent[i][0], d.Adjacent[i][1]}
			})
		},
		func() error { return loadPricing(ctx, tx, d) },
		func() error {
			for _, c := range [][3]any{{"booking", "Booking.com", 1500}, {"expedia", "Expedia", 1800}, {"tatilbudur", "Tatilbudur", 1200}} {
				if _, err := tx.Exec(ctx, "INSERT INTO channels (code, name, commission_bp, secret) VALUES ($1, $2, $3, $4)", c[0], c[1], c[2], secret); err != nil {
					return err
				}
			}
			for _, p := range [][3]any{{"Liman Otel", 0.8, 280000}, {"Bahçe Suites", 1.6, 340000}, {"Kordon Park", 2.4, 420000}} {
				if _, err := tx.Exec(ctx, "INSERT INTO partner_hotels (name, distance_km, nightly_cost_kurus) VALUES ($1, $2, $3)", p[0], p[1], p[2]); err != nil {
					return err
				}
			}
			return nil
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func loadPricing(ctx context.Context, tx pgx.Tx, d *Dataset) error {
	if _, err := tx.Exec(ctx, `INSERT INTO pricing_settings (weekend_bp, child_free_max_age, child_max_age, kdv_bp, accommodation_bp) VALUES (12000, 6, 12, 1000, 200)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rate_plans (code, name, multiplier_bp, refundable) VALUES
		('BAR', 'Esnek Tarife', 10000, true), ('NRF', 'İadesiz Tarife', 9000, false), ('BB', 'Kahvaltı Dahil', 11500, true)`); err != nil {
		return err
	}
	for _, t := range d.Types {
		for plan := int64(1); plan <= 3; plan++ {
			if _, err := tx.Exec(ctx, "INSERT INTO room_rates (room_type_id, rate_plan_id, base_kurus, extra_adult_kurus, child_kurus) VALUES ($1, $2, $3, $4, $5)",
				t.ID, plan, t.BaseKurus, t.ExtraKurus, t.ChildKurus); err != nil {
				return err
			}
		}
	}
	first := d.Anchor.Year() - historyYears
	last := d.Anchor.Year() + 2
	for year := first; year <= last; year++ {
		for _, s := range []struct {
			name           string
			fromM, fromD   int
			toM, toD, mult int
			minStay        int
		}{
			{"Ara Sezon İlkbahar", 4, 1, 6, 1, 11500, 1},
			{"Yaz", 6, 1, 9, 1, 15000, 2},
			{"Ara Sezon Sonbahar", 9, 1, 10, 1, 11500, 1},
			{"Yılbaşı", 12, 1, 12, 31, 10500, 1},
		} {
			from := time.Date(year, time.Month(s.fromM), s.fromD, 0, 0, 0, 0, time.UTC)
			to := time.Date(year, time.Month(s.toM), s.toD, 0, 0, 0, 0, time.UTC)
			if _, err := tx.Exec(ctx, "INSERT INTO seasons (name, during, multiplier_bp, min_stay) VALUES ($1, daterange($2::date, $3::date), $4, $5)",
				fmt.Sprintf("%s %d", s.name, year), from, to, s.mult, s.minStay); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO early_booking_tiers (min_lead_days, discount_bp) VALUES (30, 1000), (90, 1500)"); err != nil {
		return err
	}
	validFrom, validTo := d.Anchor.AddDate(-1, 0, 0), d.Anchor.AddDate(2, 0, 0)
	for _, c := range []struct {
		code, name string
		bp         int
		fixed      int64
		minNights  int
		withEarly  bool
		maxUses    *int
	}{
		{code: "HOSGELDIN10", name: "Hoş Geldin İndirimi", bp: 1000, minNights: 1, withEarly: true},
		{code: "AILE5", name: "Aile Kampanyası", bp: 500, minNights: 2, withEarly: true},
		{code: "UZUN500", name: "Uzun Konaklama", fixed: 50000, minNights: 5},
		{code: "FLASH25", name: "Flaş Fırsat", bp: 2500, minNights: 1},
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO campaigns (code, name, discount_bp, discount_fixed_kurus, valid_stay, min_nights, with_early_booking, max_uses)
			VALUES ($1, $2, $3, $4, daterange($5::date, $6::date), $7, $8, $9)`,
			c.code, c.name, c.bp, c.fixed, validFrom, validTo, c.minNights, c.withEarly, c.maxUses); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, "INSERT INTO campaign_compat (campaign_a, campaign_b) VALUES (1, 2)")
	return err
}

func date(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func loadStays(ctx context.Context, tx pgx.Tx, d *Dataset) error {
	if err := copyRows(ctx, tx, "guests", []string{"id", "full_name", "email", "user_id"}, len(d.Guests), func(i int) []any {
		g := d.Guests[i]
		var email *string
		if g.Email != "" {
			email = &g.Email
		}
		return []any{g.ID, g.Name, email, g.UserID}
	}); err != nil {
		return err
	}
	if err := copyRows(ctx, tx, "reservations", []string{
		"id", "code", "guest_id", "room_type_id", "rate_plan_id", "check_in", "check_out", "adults", "status", "channel_id", "external_ref",
		"pref_floor", "pref_view", "adjacent_group", "booked_on", "total_net_kurus", "total_tax_kurus", "price_hash",
	}, len(d.Reservations), func(i int) []any {
		r := d.Reservations[i]
		var floor *int32
		if r.PrefFloor != nil {
			value := int32(*r.PrefFloor)
			floor = &value
		}
		return []any{r.ID, r.Code, r.GuestID, r.TypeID, r.PlanID, date(r.CheckIn), date(r.CheckOut), int32(r.Adults), r.Status, r.ChannelID, r.ExternalRef,
			floor, r.PrefView, r.AdjacentGroup, date(r.BookedOn), r.NetKurus, r.TaxKurus, "seed"}
	}); err != nil {
		return err
	}
	if err := copyRows(ctx, tx, "reservation_nights", []string{"reservation_id", "stay_date", "net_kurus", "kdv_kurus", "accom_kurus"}, len(d.Nights), func(i int) []any {
		n := d.Nights[i]
		return []any{n.ReservationID, date(n.Date), n.Net, n.KDV, n.Accom}
	}); err != nil {
		return err
	}
	for _, a := range d.Assignments {
		if _, err := tx.Exec(ctx, "INSERT INTO room_assignments (reservation_id, room_id, stay, locked) VALUES ($1, $2, daterange($3::date, $4::date), $5)",
			a.ReservationID, a.RoomID, a.From, a.To, a.Locked); err != nil {
			return fmt.Errorf("oda ataması %d: %w", a.ReservationID, err)
		}
	}
	if err := copyRows(ctx, tx, "inventory", []string{"room_type_id", "stay_date", "capacity", "overbook_limit", "sold"}, len(d.Inventory), func(i int) []any {
		row := d.Inventory[i]
		return []any{row.TypeID, date(row.Date), int32(row.Capacity), int32(row.Limit), int32(row.Sold)}
	}); err != nil {
		return err
	}
	if err := copyRows(ctx, tx, "daily_stats", []string{"stay_date", "rooms_available", "rooms_sold", "room_revenue_kurus"}, len(d.Daily), func(i int) []any {
		row := d.Daily[i]
		return []any{date(row.Date), int32(row.Rooms), int32(row.Sold), row.Revenue}
	}); err != nil {
		return err
	}
	return copyRows(ctx, tx, "hk_tasks", []string{"room_id", "task_type", "business_date"}, len(d.Tasks), func(i int) []any {
		t := d.Tasks[i]
		return []any{t.RoomID, t.Type, date(t.Date)}
	})
}

func loadFolios(ctx context.Context, tx pgx.Tx, d *Dataset) error {
	if err := copyRows(ctx, tx, "folios", []string{"id", "reservation_id", "payer_type", "payer_name", "is_primary"}, len(d.Folios), func(i int) []any {
		f := d.Folios[i]
		return []any{f.ID, f.ReservationID, "guest", f.Payer, true}
	}); err != nil {
		return err
	}
	return copyRows(ctx, tx, "folio_lines", []string{"folio_id", "reservation_id", "kind", "amount_kurus", "description", "business_date", "method"}, len(d.Lines), func(i int) []any {
		l := d.Lines[i]
		return []any{l.FolioID, l.ReservationID, l.Kind, l.Amount, l.Description, date(l.Date), l.Method}
	})
}
