-- +goose Up
CREATE TYPE hk_status AS ENUM ('dirty', 'clean', 'inspected', 'out_of_order');

CREATE TABLE room_types (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code           text NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9_-]{2,16}$'),
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    base_occupancy integer NOT NULL DEFAULT 2 CHECK (base_occupancy BETWEEN 1 AND 8),
    max_occupancy  integer NOT NULL DEFAULT 3 CHECK (max_occupancy BETWEEN 1 AND 10),
    overbook_pct   integer NOT NULL DEFAULT 0 CHECK (overbook_pct BETWEEN 0 AND 30),
    active         boolean NOT NULL DEFAULT true,
    CHECK (max_occupancy >= base_occupancy)
);

CREATE TABLE rooms (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    number       text NOT NULL UNIQUE,
    room_type_id bigint NOT NULL REFERENCES room_types (id),
    floor        integer NOT NULL CHECK (floor BETWEEN 0 AND 60),
    view         text NOT NULL CHECK (view IN ('deniz', 'bahce', 'sehir')),
    hk_status    hk_status NOT NULL DEFAULT 'clean',
    active       boolean NOT NULL DEFAULT true
);
CREATE INDEX rooms_type_idx ON rooms (room_type_id) WHERE active;

CREATE TABLE room_adjacency (
    room_a bigint NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    room_b bigint NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    PRIMARY KEY (room_a, room_b),
    CHECK (room_a < room_b)
);

CREATE TABLE inventory (
    room_type_id   bigint NOT NULL REFERENCES room_types (id),
    stay_date      date NOT NULL,
    capacity       integer NOT NULL CHECK (capacity >= 0),
    overbook_limit integer NOT NULL DEFAULT 0 CHECK (overbook_limit >= 0),
    sold           integer NOT NULL DEFAULT 0 CHECK (sold >= 0),
    PRIMARY KEY (room_type_id, stay_date),
    CONSTRAINT inventory_not_oversold CHECK (sold <= capacity + overbook_limit)
);

CREATE TABLE guests (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    full_name  text NOT NULL CHECK (full_name <> ''),
    email      text,
    phone      text,
    user_id    bigint UNIQUE REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX guests_email_idx ON guests (lower(email));

CREATE TABLE channels (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    name          text NOT NULL,
    commission_bp integer NOT NULL DEFAULT 0 CHECK (commission_bp BETWEEN 0 AND 5000),
    secret        text NOT NULL,
    active        boolean NOT NULL DEFAULT true
);

CREATE TABLE partner_hotels (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         text NOT NULL UNIQUE,
    distance_km  numeric(5, 1) NOT NULL CHECK (distance_km >= 0),
    nightly_cost_kurus bigint NOT NULL CHECK (nightly_cost_kurus >= 0),
    active       boolean NOT NULL DEFAULT true
);

-- +goose Down
DROP TABLE partner_hotels;
DROP TABLE channels;
DROP TABLE guests;
DROP TABLE inventory;
DROP TABLE room_adjacency;
DROP TABLE rooms;
DROP TABLE room_types;
DROP TYPE hk_status;
