-- name: CreateFolio :one
INSERT INTO folios (reservation_id, payer_type, payer_name, tax_number, is_primary)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetFolio :one
SELECT * FROM folios WHERE id = $1;

-- name: LockFolio :one
SELECT * FROM folios WHERE id = $1 FOR UPDATE;

-- name: LockFoliosOrdered :many
SELECT * FROM folios WHERE id = ANY(sqlc.arg(ids)::bigint[]) ORDER BY id FOR UPDATE;

-- name: ListFolios :many
SELECT * FROM folios WHERE reservation_id = $1 ORDER BY is_primary DESC, id;

-- name: PrimaryFolio :one
SELECT * FROM folios WHERE reservation_id = $1 AND is_primary;

-- name: ListFolioLines :many
SELECT l.*, u.full_name AS posted_by_name
FROM folio_lines l LEFT JOIN users u ON u.id = l.posted_by
WHERE l.folio_id = $1 ORDER BY l.id;

-- name: GetFolioLine :one
SELECT * FROM folio_lines WHERE id = $1;

-- name: InsertFolioLine :one
INSERT INTO folio_lines (folio_id, reservation_id, kind, amount_kurus, description, business_date, method, transfer_group, refund_of_line_id, moved_line_id, posted_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING *;

-- name: InsertNightCharge :execrows
INSERT INTO folio_lines (folio_id, reservation_id, kind, amount_kurus, description, business_date)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (reservation_id, business_date, kind) WHERE kind IN ('room', 'tax_kdv', 'tax_accom') DO NOTHING;

-- name: RefundedAmount :one
SELECT COALESCE(SUM(amount_kurus), 0)::bigint FROM folio_lines WHERE refund_of_line_id = $1;

-- name: CloseFolios :exec
UPDATE folios SET closed_at = now() WHERE reservation_id = $1 AND closed_at IS NULL;

-- name: OpenFolioBalance :one
SELECT COALESCE(SUM(ABS(balance_kurus)), 0)::bigint FROM folios WHERE reservation_id = $1;

-- name: FolioIntegrityViolations :one
SELECT count(*)::int FROM folio_integrity WHERE balance_kurus <> lines_total_kurus;

-- name: CheckFolioIntegrity :one
SELECT * FROM folio_integrity WHERE folio_id = $1;

-- name: DuplicateNightCharges :one
SELECT count(*)::int FROM (
    SELECT 1 FROM folio_lines WHERE kind IN ('room', 'tax_kdv', 'tax_accom')
    GROUP BY reservation_id, business_date, kind HAVING count(*) > 1
) d;
