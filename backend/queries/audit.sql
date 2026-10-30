-- name: GetAuditRun :one
SELECT * FROM night_audit_runs WHERE business_date = $1;

-- name: ListAuditRuns :many
SELECT * FROM night_audit_runs ORDER BY business_date DESC LIMIT 30;

-- name: StartAuditRun :one
INSERT INTO night_audit_runs (business_date, status) VALUES ($1, 'running')
ON CONFLICT (business_date) DO UPDATE
SET attempts = night_audit_runs.attempts + 1, last_error = NULL
WHERE night_audit_runs.status = 'running'
RETURNING *;

-- name: SeedAuditItems :execrows
INSERT INTO night_audit_items (business_date, reservation_id)
SELECT sqlc.arg(business_date)::date, r.id
FROM reservations r
WHERE r.status = 'checked_in' AND r.check_in <= sqlc.arg(business_date)::date AND r.check_out > sqlc.arg(business_date)::date
ON CONFLICT DO NOTHING;

-- name: SetAuditTotals :exec
UPDATE night_audit_runs
SET total_items = (SELECT count(*) FROM night_audit_items i WHERE i.business_date = $1),
    done_items = (SELECT count(*) FROM night_audit_items i WHERE i.business_date = $1 AND i.done_at IS NOT NULL)
WHERE night_audit_runs.business_date = $1;

-- name: PendingAuditItems :many
SELECT reservation_id FROM night_audit_items
WHERE business_date = $1 AND done_at IS NULL
ORDER BY reservation_id;

-- name: CompleteAuditItem :exec
UPDATE night_audit_items SET done_at = now() WHERE business_date = $1 AND reservation_id = $2;

-- name: BumpAuditProgress :exec
UPDATE night_audit_runs SET done_items = done_items + 1 WHERE business_date = $1;

-- name: FailAuditRun :exec
UPDATE night_audit_runs SET last_error = $2 WHERE business_date = $1;

-- name: FinishAuditRun :exec
UPDATE night_audit_runs SET status = 'completed', finished_at = now(), last_error = NULL WHERE business_date = $1;

-- name: ListNoShows :many
SELECT id, room_type_id, check_in, check_out FROM reservations
WHERE status = 'confirmed' AND check_in <= $1 ORDER BY id;

-- name: UpsertDailyStats :exec
INSERT INTO daily_stats (stay_date, rooms_available, rooms_sold, room_revenue_kurus)
SELECT sqlc.arg(stay_date)::date,
       (SELECT count(*)::int FROM rooms WHERE active AND hk_status <> 'out_of_order'),
       (SELECT count(*)::int FROM folio_lines l WHERE l.business_date = sqlc.arg(stay_date)::date AND l.kind = 'room'),
       (SELECT COALESCE(SUM(l.amount_kurus), 0)::bigint FROM folio_lines l WHERE l.business_date = sqlc.arg(stay_date)::date AND l.kind = 'room')
ON CONFLICT (stay_date) DO UPDATE
SET rooms_available = EXCLUDED.rooms_available, rooms_sold = EXCLUDED.rooms_sold, room_revenue_kurus = EXCLUDED.room_revenue_kurus;

-- name: ListInHouseRooms :many
SELECT DISTINCT a.room_id FROM room_assignments a
JOIN reservations r ON r.id = a.reservation_id
WHERE r.status = 'checked_in' AND a.stay @> sqlc.arg(day)::date;

-- name: KPISeries :many
SELECT stay_date, rooms_available, rooms_sold, room_revenue_kurus
FROM daily_stats
WHERE stay_date >= $1 AND stay_date < $2
ORDER BY stay_date;

-- name: OnTheBooks :many
SELECT n.stay_date, count(*)::int AS rooms_sold, COALESCE(SUM(n.net_kurus), 0)::bigint AS room_revenue_kurus
FROM reservation_nights n
JOIN reservations r ON r.id = n.reservation_id
WHERE n.stay_date >= $1 AND n.stay_date < $2 AND r.status IN ('confirmed', 'checked_in')
GROUP BY n.stay_date
ORDER BY n.stay_date;

-- name: RevenueByRoomType :many
SELECT t.code, t.name, count(*)::int AS rooms_sold, COALESCE(SUM(n.net_kurus), 0)::bigint AS room_revenue_kurus
FROM reservation_nights n
JOIN reservations r ON r.id = n.reservation_id
JOIN room_types t ON t.id = r.room_type_id
WHERE n.stay_date >= $1 AND n.stay_date < $2 AND r.status IN ('confirmed', 'checked_in', 'checked_out')
GROUP BY t.id
ORDER BY room_revenue_kurus DESC;

-- name: DashboardCounts :one
SELECT
    (SELECT count(*)::int FROM reservations r WHERE r.check_in = sqlc.arg(day)::date AND r.status = 'confirmed') AS arrivals,
    (SELECT count(*)::int FROM reservations r WHERE r.check_out = sqlc.arg(day)::date AND r.status = 'checked_in') AS departures,
    (SELECT count(*)::int FROM reservations r WHERE r.status = 'checked_in') AS in_house,
    (SELECT count(*)::int FROM rooms WHERE active) AS rooms_total,
    (SELECT count(*)::int FROM rooms WHERE active AND hk_status = 'dirty') AS rooms_dirty,
    (SELECT count(*)::int FROM rooms WHERE active AND hk_status = 'out_of_order') AS rooms_out_of_order,
    (SELECT COALESCE(SUM(sold), 0)::int FROM inventory i WHERE i.stay_date = sqlc.arg(day)::date) AS sold_today,
    (SELECT count(*)::int FROM inventory i WHERE i.sold > i.capacity AND i.stay_date >= sqlc.arg(day)::date) AS overbooked_nights,
    (SELECT count(*)::int FROM channel_outbox o WHERE o.status IN ('pending', 'sending')) AS outbox_pending,
    (SELECT count(*)::int FROM channel_outbox o WHERE o.status = 'dead') AS outbox_dead,
    (SELECT count(*)::int FROM channel_inbox c WHERE c.received_at > now() - interval '1 hour') AS inbox_last_hour;

-- name: ListArrivals :many
SELECT r.id, r.code, r.check_out, r.adults, r.status, g.full_name AS guest_name, t.code AS type_code,
       (SELECT rm.number FROM room_assignments a JOIN rooms rm ON rm.id = a.room_id
        WHERE a.reservation_id = r.id ORDER BY a.stay LIMIT 1) AS room_number
FROM reservations r
JOIN guests g ON g.id = r.guest_id
JOIN room_types t ON t.id = r.room_type_id
WHERE r.check_in = $1 AND r.status = 'confirmed'
ORDER BY r.id
LIMIT 50;

-- name: ListDepartures :many
SELECT r.id, r.code, r.check_in, r.status, g.full_name AS guest_name, t.code AS type_code,
       (SELECT rm.number FROM room_assignments a JOIN rooms rm ON rm.id = a.room_id
        WHERE a.reservation_id = r.id ORDER BY a.stay DESC LIMIT 1) AS room_number
FROM reservations r
JOIN guests g ON g.id = r.guest_id
JOIN room_types t ON t.id = r.room_type_id
WHERE r.check_out = $1 AND r.status = 'checked_in'
ORDER BY r.id
LIMIT 50;
