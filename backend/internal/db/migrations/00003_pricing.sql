-- +goose Up
CREATE TABLE pricing_settings (
    id                 smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    weekend_bp         integer NOT NULL CHECK (weekend_bp BETWEEN 5000 AND 30000),
    child_free_max_age integer NOT NULL CHECK (child_free_max_age BETWEEN 0 AND 17),
    child_max_age      integer NOT NULL CHECK (child_max_age BETWEEN 0 AND 17),
    kdv_bp             integer NOT NULL CHECK (kdv_bp BETWEEN 0 AND 5000),
    accommodation_bp   integer NOT NULL CHECK (accommodation_bp BETWEEN 0 AND 5000),
    CHECK (child_max_age >= child_free_max_age)
);

CREATE TABLE rate_plans (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    name          text NOT NULL,
    multiplier_bp integer NOT NULL DEFAULT 10000 CHECK (multiplier_bp BETWEEN 1000 AND 30000),
    refundable    boolean NOT NULL DEFAULT true,
    active        boolean NOT NULL DEFAULT true
);

CREATE TABLE room_rates (
    room_type_id      bigint NOT NULL REFERENCES room_types (id),
    rate_plan_id      bigint NOT NULL REFERENCES rate_plans (id),
    base_kurus        bigint NOT NULL CHECK (base_kurus > 0),
    extra_adult_kurus bigint NOT NULL CHECK (extra_adult_kurus >= 0),
    child_kurus       bigint NOT NULL CHECK (child_kurus >= 0),
    PRIMARY KEY (room_type_id, rate_plan_id)
);

CREATE TABLE seasons (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text NOT NULL,
    during        daterange NOT NULL CHECK (NOT isempty(during)),
    multiplier_bp integer NOT NULL CHECK (multiplier_bp BETWEEN 1000 AND 50000),
    min_stay      integer NOT NULL DEFAULT 1 CHECK (min_stay BETWEEN 1 AND 30),
    CONSTRAINT seasons_no_overlap EXCLUDE USING gist (during WITH &&)
);

CREATE TABLE early_booking_tiers (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    min_lead_days integer NOT NULL UNIQUE CHECK (min_lead_days > 0),
    discount_bp   integer NOT NULL CHECK (discount_bp BETWEEN 1 AND 9000)
);

CREATE TABLE campaigns (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code                 text NOT NULL UNIQUE CHECK (code = upper(code)),
    name                 text NOT NULL,
    discount_bp          integer NOT NULL DEFAULT 0 CHECK (discount_bp BETWEEN 0 AND 9000),
    discount_fixed_kurus bigint NOT NULL DEFAULT 0 CHECK (discount_fixed_kurus >= 0),
    valid_stay           daterange NOT NULL,
    min_nights           integer NOT NULL DEFAULT 1 CHECK (min_nights >= 1),
    with_early_booking   boolean NOT NULL DEFAULT false,
    max_uses             integer CHECK (max_uses IS NULL OR max_uses > 0),
    used_count           integer NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    active               boolean NOT NULL DEFAULT true,
    CONSTRAINT campaigns_single_discount CHECK ((discount_bp > 0) <> (discount_fixed_kurus > 0)),
    CONSTRAINT campaigns_usage_limit CHECK (max_uses IS NULL OR used_count <= max_uses)
);

CREATE TABLE campaign_compat (
    campaign_a bigint NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    campaign_b bigint NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    PRIMARY KEY (campaign_a, campaign_b),
    CHECK (campaign_a < campaign_b)
);

-- +goose Down
DROP TABLE campaign_compat;
DROP TABLE campaigns;
DROP TABLE early_booking_tiers;
DROP TABLE seasons;
DROP TABLE room_rates;
DROP TABLE rate_plans;
DROP TABLE pricing_settings;
