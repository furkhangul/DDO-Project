-- +goose Up
CREATE TYPE reservation_status AS ENUM ('confirmed', 'checked_in', 'checked_out', 'cancelled', 'no_show', 'walked');

CREATE TABLE reservations (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code            text NOT NULL UNIQUE,
    guest_id        bigint NOT NULL REFERENCES guests (id),
    room_type_id    bigint NOT NULL REFERENCES room_types (id),
    rate_plan_id    bigint NOT NULL REFERENCES rate_plans (id),
    check_in        date NOT NULL,
    check_out       date NOT NULL,
    adults          integer NOT NULL CHECK (adults BETWEEN 1 AND 10),
    children_ages   integer[] NOT NULL DEFAULT '{}',
    status          reservation_status NOT NULL DEFAULT 'confirmed',
    channel_id      bigint REFERENCES channels (id),
    external_ref    text,
    pref_floor      integer,
    pref_view       text CHECK (pref_view IN ('deniz', 'bahce', 'sehir')),
    adjacent_group  text,
    booked_on       date NOT NULL,
    total_net_kurus bigint NOT NULL CHECK (total_net_kurus >= 0),
    total_tax_kurus bigint NOT NULL CHECK (total_tax_kurus >= 0),
    price_input     jsonb,
    price_rules     jsonb,
    price_hash      text NOT NULL,
    created_by      bigint REFERENCES users (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reservations_dates CHECK (check_out > check_in),
    CONSTRAINT reservations_external UNIQUE (channel_id, external_ref)
);
CREATE INDEX reservations_arrivals_idx ON reservations (check_in, status);
CREATE INDEX reservations_departures_idx ON reservations (check_out, status);
CREATE INDEX reservations_guest_idx ON reservations (guest_id, id DESC);
CREATE INDEX reservations_active_type_idx ON reservations (room_type_id, check_in, check_out)
    WHERE status IN ('confirmed', 'checked_in');

CREATE TABLE reservation_nights (
    reservation_id bigint NOT NULL REFERENCES reservations (id) ON DELETE CASCADE,
    stay_date      date NOT NULL,
    net_kurus      bigint NOT NULL CHECK (net_kurus >= 0),
    kdv_kurus      bigint NOT NULL CHECK (kdv_kurus >= 0),
    accom_kurus    bigint NOT NULL CHECK (accom_kurus >= 0),
    PRIMARY KEY (reservation_id, stay_date)
);
CREATE INDEX reservation_nights_date_idx ON reservation_nights (stay_date) INCLUDE (net_kurus, reservation_id);

CREATE TABLE room_assignments (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    reservation_id bigint NOT NULL REFERENCES reservations (id) ON DELETE CASCADE,
    room_id        bigint NOT NULL REFERENCES rooms (id),
    stay           daterange NOT NULL CHECK (NOT isempty(stay)),
    locked         boolean NOT NULL DEFAULT false,
    CONSTRAINT room_assignments_no_double_booking EXCLUDE USING gist (room_id WITH =, stay WITH &&),
    CONSTRAINT room_assignments_no_overlap_per_reservation EXCLUDE USING gist (reservation_id WITH =, stay WITH &&)
);
CREATE INDEX room_assignments_reservation_idx ON room_assignments (reservation_id);

CREATE TABLE reservation_campaigns (
    reservation_id bigint NOT NULL REFERENCES reservations (id) ON DELETE CASCADE,
    campaign_id    bigint NOT NULL REFERENCES campaigns (id),
    PRIMARY KEY (reservation_id, campaign_id)
);

CREATE TABLE walks (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    reservation_id    bigint NOT NULL UNIQUE REFERENCES reservations (id),
    partner_hotel_id  bigint NOT NULL REFERENCES partner_hotels (id),
    nights            integer NOT NULL CHECK (nights > 0),
    cost_kurus        bigint NOT NULL CHECK (cost_kurus >= 0),
    reason            text NOT NULL,
    walked_by         bigint NOT NULL REFERENCES users (id),
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE walks;
DROP TABLE reservation_campaigns;
DROP TABLE room_assignments;
DROP TABLE reservation_nights;
DROP TABLE reservations;
DROP TYPE reservation_status;
