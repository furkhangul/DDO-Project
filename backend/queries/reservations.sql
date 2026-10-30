-- name: CreateGuest :one
INSERT INTO guests (full_name, email, phone) VALUES ($1, $2, $3) RETURNING *;

-- name: GetGuest :one
SELECT * FROM guests WHERE id = $1;

-- name: GetGuestByUser :one
SELECT * FROM guests WHERE user_id = $1;

-- name: InsertReservation :one
INSERT INTO reservations (
    code, guest_id, room_type_id, rate_plan_id, check_in, check_out, adults, children_ages,
    channel_id, external_ref, pref_floor, pref_view, adjacent_group, booked_on,
    total_net_kurus, total_tax_kurus, price_input, price_rules, price_hash, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
RETURNING *;

-- name: InsertReservationNight :exec
INSERT INTO reservation_nights (reservation_id, stay_date, net_kurus, kdv_kurus, accom_kurus)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteReservationNights :exec
DELETE FROM reservation_nights WHERE reservation_id = $1;

-- name: DeleteReservationNightsFrom :exec
DELETE FROM reservation_nights WHERE reservation_id = $1 AND stay_date >= $2;

-- name: ListReservationNights :many
SELECT * FROM reservation_nights WHERE reservation_id = $1 ORDER BY stay_date;

-- name: GetReservation :one
SELECT r.*, g.full_name AS guest_name, g.user_id AS guest_user_id, t.code AS type_code, t.name AS type_name,
       p.code AS plan_code, p.name AS plan_name, c.code AS channel_code
FROM reservations r
JOIN guests g ON g.id = r.guest_id
JOIN room_types t ON t.id = r.room_type_id
JOIN rate_plans p ON p.id = r.rate_plan_id
LEFT JOIN channels c ON c.id = r.channel_id
WHERE r.id = $1;

-- name: LockReservation :one
SELECT * FROM reservations WHERE id = $1 FOR UPDATE;

-- name: ListReservations :many
SELECT r.id, r.code, r.check_in, r.check_out, r.adults, r.status, r.total_net_kurus, r.total_tax_kurus, r.created_at,
       g.full_name AS guest_name, t.code AS type_code, c.code AS channel_code
FROM reservations r
JOIN guests g ON g.id = r.guest_id
JOIN room_types t ON t.id = r.room_type_id
LEFT JOIN channels c ON c.id = r.channel_id
WHERE (sqlc.narg(before_id)::bigint IS NULL OR r.id < sqlc.narg(before_id))
  AND (sqlc.narg(status)::reservation_status IS NULL OR r.status = sqlc.narg(status))
  AND (sqlc.narg(guest_id)::bigint IS NULL OR r.guest_id = sqlc.narg(guest_id))
  AND (sqlc.narg(arrival)::date IS NULL OR r.check_in = sqlc.narg(arrival))
  AND (sqlc.narg(search)::text IS NULL OR r.code ILIKE '%' || sqlc.narg(search) || '%' OR g.full_name ILIKE '%' || sqlc.narg(search) || '%')
ORDER BY r.id DESC
LIMIT $1;

-- name: UpdateReservationStatus :exec
UPDATE reservations SET status = $2, updated_at = now() WHERE id = $1;

-- name: UpdateReservationStay :one
UPDATE reservations
SET room_type_id = $2, check_in = $3, check_out = $4, adults = $5, children_ages = $6,
    total_net_kurus = $7, total_tax_kurus = $8, price_input = $9, price_rules = $10, price_hash = $11, updated_at = now()
WHERE id = $1 RETURNING *;

-- name: ShortenReservation :exec
UPDATE reservations
SET check_out = $2,
    total_net_kurus = (SELECT COALESCE(SUM(n.net_kurus), 0)::bigint FROM reservation_nights n WHERE n.reservation_id = reservations.id),
    total_tax_kurus = (SELECT COALESCE(SUM(n.kdv_kurus + n.accom_kurus), 0)::bigint FROM reservation_nights n WHERE n.reservation_id = reservations.id),
    updated_at = now()
WHERE id = $1;

-- name: ListStaysForSolver :many
SELECT r.id, r.room_type_id, r.check_in, r.check_out, r.pref_floor, r.pref_view, r.adjacent_group, r.status
FROM reservations r
WHERE r.status IN ('confirmed', 'checked_in') AND r.check_in < sqlc.arg(date_to) AND r.check_out > sqlc.arg(date_from)
ORDER BY r.id;

-- name: ListAssignmentsBetween :many
SELECT a.id, a.reservation_id, a.room_id, lower(a.stay)::date AS date_from, upper(a.stay)::date AS date_to, a.locked
FROM room_assignments a
WHERE a.stay && daterange(sqlc.arg(date_from)::date, sqlc.arg(date_to)::date)
ORDER BY a.id;

-- name: ListAssignmentsForReservation :many
SELECT a.id, a.reservation_id, a.room_id, r.number AS room_number, lower(a.stay)::date AS date_from, upper(a.stay)::date AS date_to, a.locked
FROM room_assignments a JOIN rooms r ON r.id = a.room_id
WHERE a.reservation_id = $1
ORDER BY a.stay;

-- name: InsertAssignment :exec
INSERT INTO room_assignments (reservation_id, room_id, stay, locked)
VALUES ($1, $2, daterange(sqlc.arg(date_from)::date, sqlc.arg(date_to)::date), $3);

-- name: DeleteAssignments :exec
DELETE FROM room_assignments WHERE reservation_id = $1;

-- name: DeleteUnlockedAssignments :execrows
DELETE FROM room_assignments a
USING reservations r
WHERE a.reservation_id = r.id AND NOT a.locked AND r.status = 'confirmed'
  AND r.check_in < sqlc.arg(date_to) AND r.check_out > sqlc.arg(date_from);

-- name: LockReservationAssignments :exec
UPDATE room_assignments SET locked = true WHERE reservation_id = $1;

-- name: TruncateAssignmentAt :exec
UPDATE room_assignments
SET stay = daterange(lower(stay), sqlc.arg(cut)::date)
WHERE reservation_id = sqlc.arg(reservation_id) AND stay @> sqlc.arg(cut)::date AND lower(stay) < sqlc.arg(cut)::date;

-- name: DeleteAssignmentsFrom :exec
DELETE FROM room_assignments WHERE reservation_id = sqlc.arg(reservation_id) AND lower(stay) >= sqlc.arg(cut)::date;

-- name: CalendarSegments :many
SELECT a.id AS assignment_id, a.room_id, lower(a.stay)::date AS date_from, upper(a.stay)::date AS date_to, a.locked,
       r.id AS reservation_id, r.code, r.status, r.adults, g.full_name AS guest_name, c.code AS channel_code
FROM room_assignments a
JOIN reservations r ON r.id = a.reservation_id
JOIN guests g ON g.id = r.guest_id
LEFT JOIN channels c ON c.id = r.channel_id
WHERE a.stay && daterange(sqlc.arg(date_from)::date, sqlc.arg(date_to)::date)
ORDER BY a.room_id, a.stay;

-- name: CalendarUnassigned :many
SELECT r.id, r.code, r.check_in, r.check_out, r.status, g.full_name AS guest_name, t.code AS type_code
FROM reservations r
JOIN guests g ON g.id = r.guest_id
JOIN room_types t ON t.id = r.room_type_id
WHERE r.status = 'confirmed' AND r.check_in < sqlc.arg(date_to) AND r.check_out > sqlc.arg(date_from)
  AND NOT EXISTS (SELECT 1 FROM room_assignments a WHERE a.reservation_id = r.id)
ORDER BY r.check_in, r.id
LIMIT 200;

-- name: InsertWalk :one
INSERT INTO walks (reservation_id, partner_hotel_id, nights, cost_kurus, reason, walked_by)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: ListWalkCandidates :many
SELECT r.id, r.code, r.check_in, r.check_out, g.full_name AS guest_name, t.code AS type_code, t.id AS room_type_id
FROM reservations r
JOIN guests g ON g.id = r.guest_id
JOIN room_types t ON t.id = r.room_type_id
WHERE r.status = 'confirmed'
  AND EXISTS (
      SELECT 1 FROM inventory i
      WHERE i.room_type_id = r.room_type_id AND i.stay_date >= r.check_in AND i.stay_date < r.check_out
        AND i.sold > i.capacity
  )
ORDER BY r.check_in, r.id
LIMIT 100;

-- name: CountOverbookedNightsForStay :one
SELECT count(*)::int FROM inventory
WHERE room_type_id = $1 AND stay_date >= $2 AND stay_date < $3 AND sold > capacity;

-- name: ListWalks :many
SELECT w.*, r.code, g.full_name AS guest_name, p.name AS partner_name
FROM walks w
JOIN reservations r ON r.id = w.reservation_id
JOIN guests g ON g.id = r.guest_id
JOIN partner_hotels p ON p.id = w.partner_hotel_id
ORDER BY w.id DESC LIMIT 50;

-- name: FindReadyRoom :one
SELECT r.id FROM rooms r
WHERE r.room_type_id = $1 AND r.active AND r.hk_status IN ('clean', 'inspected')
  AND NOT EXISTS (
      SELECT 1 FROM room_assignments a
      WHERE a.room_id = r.id AND a.stay && daterange(sqlc.arg(date_from)::date, sqlc.arg(date_to)::date)
  )
ORDER BY r.number LIMIT 1;
