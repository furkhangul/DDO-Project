-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND active;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND active;

-- name: ListStaff :many
SELECT id, email, full_name, role FROM users WHERE active AND role <> 'guest' ORDER BY full_name;

-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4);

-- name: LockRefreshToken :one
SELECT * FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE;

-- name: MarkRefreshTokenUsed :exec
UPDATE refresh_tokens SET used_at = now() WHERE id = $1;

-- name: RevokeRefreshFamily :exec
UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL;

-- name: GetHotelState :one
SELECT * FROM hotel_state WHERE id = 1;

-- name: LockHotelState :one
SELECT * FROM hotel_state WHERE id = 1 FOR UPDATE;

-- name: AdvanceBusinessDate :one
UPDATE hotel_state SET business_date = business_date + 1 WHERE business_date = $1 RETURNING business_date;

-- name: InsertAudit :exec
INSERT INTO audit_log (actor_id, actor_label, action, entity, entity_id, before_data, after_data)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAudit :many
SELECT * FROM audit_log
WHERE (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
  AND (sqlc.narg(entity)::text IS NULL OR entity = sqlc.narg(entity))
ORDER BY id DESC
LIMIT $1;

-- name: ClaimIdempotencyKey :one
INSERT INTO idempotency_keys (user_id, idem_key, method, path, request_hash, locked_until)
VALUES ($1, $2, $3, $4, $5, now() + interval '60 seconds')
ON CONFLICT (user_id, idem_key) DO UPDATE
SET locked_until = now() + interval '60 seconds'
WHERE idempotency_keys.status_code IS NULL
  AND idempotency_keys.locked_until < now()
  AND idempotency_keys.request_hash = EXCLUDED.request_hash
RETURNING status_code;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE user_id = $1 AND idem_key = $2;

-- name: FinishIdempotencyKey :exec
UPDATE idempotency_keys SET status_code = $3, response_body = $4 WHERE user_id = $1 AND idem_key = $2;

-- name: ReleaseIdempotencyKey :exec
DELETE FROM idempotency_keys WHERE user_id = $1 AND idem_key = $2 AND status_code IS NULL;

-- name: PurgeIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE created_at < now() - interval '48 hours';

-- name: RecentActivity :many
SELECT id, actor_label, action, entity, entity_id, created_at FROM audit_log ORDER BY id DESC LIMIT 12;
