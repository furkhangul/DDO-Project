-- name: ListRoomTypes :many
SELECT t.*, (SELECT count(*) FROM rooms r WHERE r.room_type_id = t.id AND r.active)::int AS room_count
FROM room_types t ORDER BY t.id;

-- name: GetRoomType :one
SELECT * FROM room_types WHERE id = $1;

-- name: CreateRoomType :one
INSERT INTO room_types (code, name, description, base_occupancy, max_occupancy, overbook_pct)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: UpdateRoomType :one
UPDATE room_types
SET name = $2, description = $3, base_occupancy = $4, max_occupancy = $5, overbook_pct = $6, active = $7
WHERE id = $1 RETURNING *;

-- name: ListRooms :many
SELECT r.*, t.code AS type_code, t.name AS type_name
FROM rooms r JOIN room_types t ON t.id = r.room_type_id
WHERE r.active ORDER BY r.number;

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = $1;

-- name: LockRoom :one
SELECT * FROM rooms WHERE id = $1 FOR UPDATE;

-- name: CreateRoom :one
INSERT INTO rooms (number, room_type_id, floor, view) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: UpdateRoom :one
UPDATE rooms SET floor = $2, view = $3, active = $4 WHERE id = $1 RETURNING *;

-- name: SetRoomStatus :one
UPDATE rooms SET hk_status = $2 WHERE id = $1 RETURNING *;

-- name: ListAdjacency :many
SELECT room_a, room_b FROM room_adjacency ORDER BY room_a, room_b;

-- name: AddAdjacency :exec
INSERT INTO room_adjacency (room_a, room_b) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: EnsureInventory :exec
INSERT INTO inventory (room_type_id, stay_date, capacity, overbook_limit)
SELECT t.id, d::date, c.rooms, (c.rooms * t.overbook_pct) / 100
FROM room_types t
CROSS JOIN LATERAL (SELECT count(*)::int AS rooms FROM rooms r WHERE r.room_type_id = t.id AND r.active) c
CROSS JOIN generate_series(sqlc.arg(date_from)::date, sqlc.arg(date_to)::date - 1, interval '1 day') d
WHERE t.id = sqlc.arg(room_type_id)
ON CONFLICT (room_type_id, stay_date) DO NOTHING;

-- name: SyncInventoryLimits :execrows
UPDATE inventory i
SET capacity = c.rooms,
    overbook_limit = GREATEST((c.rooms * t.overbook_pct) / 100, i.sold - c.rooms)
FROM room_types t
CROSS JOIN LATERAL (SELECT count(*)::int AS rooms FROM rooms r WHERE r.room_type_id = t.id AND r.active) c
WHERE i.room_type_id = t.id AND t.id = $1 AND i.stay_date >= $2;

-- name: LockInventory :many
SELECT * FROM inventory
WHERE room_type_id = $1 AND stay_date >= $2 AND stay_date < $3
ORDER BY stay_date FOR UPDATE;

-- name: ConsumeInventory :execrows
UPDATE inventory SET sold = sold + 1
WHERE room_type_id = $1 AND stay_date >= $2 AND stay_date < $3;

-- name: ReleaseInventory :execrows
UPDATE inventory SET sold = sold - 1
WHERE room_type_id = $1 AND stay_date >= $2 AND stay_date < $3;

-- name: ListAvailability :many
SELECT i.*, t.code AS type_code, t.name AS type_name
FROM inventory i JOIN room_types t ON t.id = i.room_type_id
WHERE i.stay_date >= $1 AND i.stay_date < $2 AND t.active
  AND (sqlc.narg(room_type_id)::bigint IS NULL OR i.room_type_id = sqlc.narg(room_type_id))
ORDER BY i.stay_date, i.room_type_id;

-- name: ListOverbookedNights :many
SELECT i.*, t.code AS type_code, t.name AS type_name
FROM inventory i JOIN room_types t ON t.id = i.room_type_id
WHERE i.sold > i.capacity AND i.stay_date >= $1
ORDER BY i.stay_date, i.room_type_id;

-- name: ListPartnerHotels :many
SELECT * FROM partner_hotels WHERE active ORDER BY distance_km;

-- name: GetPartnerHotel :one
SELECT * FROM partner_hotels WHERE id = $1 AND active;
