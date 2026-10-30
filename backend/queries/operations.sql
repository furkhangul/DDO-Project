-- name: UpsertTask :one
INSERT INTO hk_tasks (room_id, task_type, business_date, assigned_to, note)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (room_id, business_date, task_type) DO UPDATE SET note = EXCLUDED.note
RETURNING *;

-- name: GetTask :one
SELECT * FROM hk_tasks WHERE id = $1;

-- name: UpdateTask :one
UPDATE hk_tasks SET status = $2, assigned_to = $3, note = $4, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListTasks :many
SELECT t.*, r.number AS room_number, r.floor, r.hk_status, u.full_name AS assignee
FROM hk_tasks t
JOIN rooms r ON r.id = t.room_id
LEFT JOIN users u ON u.id = t.assigned_to
WHERE t.business_date = $1
ORDER BY t.status, r.number;

-- name: GetChannelByCode :one
SELECT * FROM channels WHERE code = $1 AND active;

-- name: ListChannels :many
SELECT id, code, name, commission_bp, active FROM channels ORDER BY id;

-- name: InsertInbox :one
INSERT INTO channel_inbox (channel_id, event_id, external_ref, seq, event_type, payload, outcome)
VALUES ($1, $2, $3, $4, $5, $6, 'rejected')
ON CONFLICT (channel_id, event_id) DO NOTHING
RETURNING id;

-- name: GetInboxOutcome :one
SELECT outcome, detail FROM channel_inbox WHERE channel_id = $1 AND event_id = $2;

-- name: SetInboxOutcome :exec
UPDATE channel_inbox SET outcome = $2, detail = $3 WHERE id = $1;

-- name: EnsureChannelBooking :exec
INSERT INTO channel_bookings (channel_id, external_ref, last_seq, state) VALUES ($1, $2, 0, 'active')
ON CONFLICT DO NOTHING;

-- name: LockChannelBooking :one
SELECT * FROM channel_bookings WHERE channel_id = $1 AND external_ref = $2 FOR UPDATE;

-- name: UpdateChannelBooking :exec
UPDATE channel_bookings SET last_seq = $3, state = $4, reservation_id = $5 WHERE channel_id = $1 AND external_ref = $2;

-- name: ListInbox :many
SELECT i.id, i.event_id, i.external_ref, i.seq, i.event_type, i.outcome, i.detail, i.received_at, c.code AS channel_code
FROM channel_inbox i JOIN channels c ON c.id = i.channel_id
WHERE (sqlc.narg(before_id)::bigint IS NULL OR i.id < sqlc.narg(before_id))
ORDER BY i.id DESC LIMIT $1;

-- name: InboxStats :many
SELECT outcome, count(*)::int AS total FROM channel_inbox GROUP BY outcome;

-- name: ClaimOutbox :many
UPDATE channel_outbox
SET status = 'sending', locked_until = now() + interval '30 seconds', attempts = attempts + 1
WHERE id IN (
    SELECT o.id FROM channel_outbox o
    WHERE (o.status = 'pending' AND o.next_attempt_at <= now())
       OR (o.status = 'sending' AND o.locked_until < now())
    ORDER BY o.next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: OutboxPayload :one
SELECT c.code AS channel_code, t.code AS type_code, i.capacity, i.overbook_limit, i.sold
FROM channels c
JOIN inventory i ON i.room_type_id = $2 AND i.stay_date = $3
JOIN room_types t ON t.id = i.room_type_id
WHERE c.id = $1;

-- name: MarkOutboxSent :exec
UPDATE channel_outbox SET status = 'sent', sent_at = now(), locked_until = NULL, last_error = NULL WHERE id = $1;

-- name: MarkOutboxDead :exec
UPDATE channel_outbox SET status = 'dead', locked_until = NULL, last_error = $2 WHERE id = $1;

-- name: MarkOutboxRetry :exec
UPDATE channel_outbox o
SET status = CASE WHEN EXISTS (
        SELECT 1 FROM channel_outbox p
        WHERE p.status = 'pending' AND p.channel_id = o.channel_id
          AND p.room_type_id = o.room_type_id AND p.stay_date = o.stay_date AND p.id <> o.id
    ) THEN 'superseded' ELSE 'pending' END,
    next_attempt_at = $2, locked_until = NULL, last_error = $3
WHERE o.id = $1;

-- name: RetryDeadOutbox :execrows
UPDATE channel_outbox o
SET status = 'pending', attempts = 0, next_attempt_at = now(), last_error = NULL
WHERE o.id = $1 AND o.status = 'dead'
  AND NOT EXISTS (
      SELECT 1 FROM channel_outbox p
      WHERE p.status = 'pending' AND p.channel_id = o.channel_id
        AND p.room_type_id = o.room_type_id AND p.stay_date = o.stay_date
  );

-- name: ListOutbox :many
SELECT o.*, c.code AS channel_code, t.code AS type_code
FROM channel_outbox o
JOIN channels c ON c.id = o.channel_id
JOIN room_types t ON t.id = o.room_type_id
WHERE (sqlc.narg(status)::text IS NULL OR o.status = sqlc.narg(status))
  AND (sqlc.narg(before_id)::bigint IS NULL OR o.id < sqlc.narg(before_id))
ORDER BY o.id DESC LIMIT $1;

-- name: OutboxStats :many
SELECT status, count(*)::int AS total FROM channel_outbox GROUP BY status;

-- name: PurgeSentOutbox :exec
DELETE FROM channel_outbox WHERE status IN ('sent', 'superseded') AND created_at < now() - interval '1 day';
