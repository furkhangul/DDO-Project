-- name: GetPricingSettings :one
SELECT * FROM pricing_settings WHERE id = 1;

-- name: UpdatePricingSettings :one
UPDATE pricing_settings
SET weekend_bp = $1, child_free_max_age = $2, child_max_age = $3, kdv_bp = $4, accommodation_bp = $5
WHERE id = 1 RETURNING *;

-- name: ListRatePlans :many
SELECT * FROM rate_plans WHERE active ORDER BY id;

-- name: GetRatePlan :one
SELECT * FROM rate_plans WHERE id = $1;

-- name: GetRatePlanByCode :one
SELECT * FROM rate_plans WHERE code = $1;

-- name: GetRoomRate :one
SELECT * FROM room_rates WHERE room_type_id = $1 AND rate_plan_id = $2;

-- name: ListRoomRates :many
SELECT rr.*, t.code AS type_code, t.name AS type_name, p.code AS plan_code, p.name AS plan_name
FROM room_rates rr
JOIN room_types t ON t.id = rr.room_type_id
JOIN rate_plans p ON p.id = rr.rate_plan_id
ORDER BY t.id, p.id;

-- name: UpsertRoomRate :one
INSERT INTO room_rates (room_type_id, rate_plan_id, base_kurus, extra_adult_kurus, child_kurus)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (room_type_id, rate_plan_id) DO UPDATE
SET base_kurus = EXCLUDED.base_kurus, extra_adult_kurus = EXCLUDED.extra_adult_kurus, child_kurus = EXCLUDED.child_kurus
RETURNING *;

-- name: ListSeasons :many
SELECT id, name, lower(during)::date AS date_from, upper(during)::date AS date_to, multiplier_bp, min_stay
FROM seasons ORDER BY during;

-- name: CreateSeason :one
INSERT INTO seasons (name, during, multiplier_bp, min_stay)
VALUES ($1, daterange(sqlc.arg(date_from)::date, sqlc.arg(date_to)::date), $2, $3)
RETURNING id;

-- name: DeleteSeason :execrows
DELETE FROM seasons WHERE id = $1;

-- name: ListEarlyTiers :many
SELECT * FROM early_booking_tiers ORDER BY min_lead_days;

-- name: UpsertEarlyTier :one
INSERT INTO early_booking_tiers (min_lead_days, discount_bp) VALUES ($1, $2)
ON CONFLICT (min_lead_days) DO UPDATE SET discount_bp = EXCLUDED.discount_bp
RETURNING *;

-- name: DeleteEarlyTier :execrows
DELETE FROM early_booking_tiers WHERE id = $1;

-- name: ListCampaigns :many
SELECT id, code, name, discount_bp, discount_fixed_kurus, lower(valid_stay)::date AS valid_from, upper(valid_stay)::date AS valid_to,
       min_nights, with_early_booking, max_uses, used_count, active
FROM campaigns ORDER BY code;

-- name: CreateCampaign :one
INSERT INTO campaigns (code, name, discount_bp, discount_fixed_kurus, valid_stay, min_nights, with_early_booking, max_uses)
VALUES ($1, $2, $3, $4, daterange(sqlc.arg(valid_from)::date, sqlc.arg(valid_to)::date), $5, $6, $7)
RETURNING id;

-- name: SetCampaignActive :execrows
UPDATE campaigns SET active = $2 WHERE id = $1;

-- name: ListCampaignCompat :many
SELECT a.code AS code_a, b.code AS code_b, c.campaign_a, c.campaign_b
FROM campaign_compat c
JOIN campaigns a ON a.id = c.campaign_a
JOIN campaigns b ON b.id = c.campaign_b
ORDER BY a.code, b.code;

-- name: AddCampaignCompat :exec
INSERT INTO campaign_compat (campaign_a, campaign_b) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: RemoveCampaignCompat :execrows
DELETE FROM campaign_compat WHERE campaign_a = $1 AND campaign_b = $2;

-- name: ConsumeCampaign :execrows
UPDATE campaigns SET used_count = used_count + 1
WHERE code = $1 AND active AND (max_uses IS NULL OR used_count < max_uses);

-- name: ReleaseCampaigns :exec
UPDATE campaigns SET used_count = used_count - 1
WHERE id IN (SELECT campaign_id FROM reservation_campaigns WHERE reservation_id = $1) AND used_count > 0;

-- name: LinkReservationCampaign :exec
INSERT INTO reservation_campaigns (reservation_id, campaign_id)
SELECT $1, id FROM campaigns WHERE code = $2;

-- name: ClearReservationCampaigns :exec
DELETE FROM reservation_campaigns WHERE reservation_id = $1;
