-- +goose Up
CREATE TABLE hk_tasks (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    room_id       bigint NOT NULL REFERENCES rooms (id),
    task_type     text NOT NULL CHECK (task_type IN ('cikis_temizligi', 'konaklama_temizligi', 'kontrol', 'bakim')),
    status        text NOT NULL DEFAULT 'bekliyor' CHECK (status IN ('bekliyor', 'devam', 'tamamlandi')),
    assigned_to   bigint REFERENCES users (id),
    business_date date NOT NULL,
    note          text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (room_id, business_date, task_type)
);
CREATE INDEX hk_tasks_date_idx ON hk_tasks (business_date, status);

CREATE TABLE channel_inbox (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel_id   bigint NOT NULL REFERENCES channels (id),
    event_id     text NOT NULL,
    external_ref text NOT NULL,
    seq          bigint NOT NULL CHECK (seq > 0),
    event_type   text NOT NULL CHECK (event_type IN ('create', 'modify', 'cancel')),
    payload      jsonb NOT NULL,
    outcome      text NOT NULL CHECK (outcome IN ('applied', 'stale', 'conflict', 'rejected')),
    detail       text NOT NULL DEFAULT '',
    received_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (channel_id, event_id)
);
CREATE INDEX channel_inbox_recent_idx ON channel_inbox (id DESC);

CREATE TABLE channel_bookings (
    channel_id     bigint NOT NULL REFERENCES channels (id),
    external_ref   text NOT NULL,
    last_seq       bigint NOT NULL,
    state          text NOT NULL CHECK (state IN ('active', 'cancelled', 'conflict')),
    reservation_id bigint REFERENCES reservations (id),
    PRIMARY KEY (channel_id, external_ref)
);

CREATE TABLE channel_outbox (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel_id      bigint NOT NULL REFERENCES channels (id),
    room_type_id    bigint NOT NULL REFERENCES room_types (id),
    stay_date       date NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'dead', 'superseded')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until    timestamptz,
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz
);
CREATE UNIQUE INDEX channel_outbox_one_pending_idx ON channel_outbox (channel_id, room_type_id, stay_date)
    WHERE status = 'pending';
CREATE INDEX channel_outbox_due_idx ON channel_outbox (next_attempt_at) WHERE status IN ('pending', 'sending');
CREATE INDEX channel_outbox_dead_idx ON channel_outbox (id DESC) WHERE status = 'dead';

-- +goose StatementBegin
CREATE FUNCTION inventory_enqueue_outbox() RETURNS trigger AS $$
BEGIN
    IF (NEW.sold, NEW.capacity, NEW.overbook_limit) IS DISTINCT FROM (OLD.sold, OLD.capacity, OLD.overbook_limit) THEN
        INSERT INTO channel_outbox (channel_id, room_type_id, stay_date)
        SELECT c.id, NEW.room_type_id, NEW.stay_date FROM channels c WHERE c.active
        ON CONFLICT (channel_id, room_type_id, stay_date) WHERE status = 'pending' DO NOTHING;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER inventory_outbox AFTER UPDATE ON inventory
    FOR EACH ROW EXECUTE FUNCTION inventory_enqueue_outbox();

CREATE TABLE seed_meta (
    id         smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    seed       bigint NOT NULL,
    anchor     date NOT NULL,
    digest     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE seed_meta;
DROP TRIGGER inventory_outbox ON inventory;
DROP FUNCTION inventory_enqueue_outbox();
DROP TABLE channel_outbox;
DROP TABLE channel_bookings;
DROP TABLE channel_inbox;
DROP TABLE hk_tasks;
