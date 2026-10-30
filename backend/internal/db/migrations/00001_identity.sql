-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TYPE user_role AS ENUM ('reception', 'reservations', 'housekeeping', 'revenue', 'manager', 'guest');

CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         text NOT NULL UNIQUE CHECK (email = lower(email)),
    password_hash text NOT NULL,
    full_name     text NOT NULL CHECK (full_name <> ''),
    role          user_role NOT NULL,
    active        boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE refresh_tokens (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id  uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);

CREATE TABLE hotel_state (
    id            smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    name          text NOT NULL,
    business_date date NOT NULL
);

CREATE TABLE audit_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id    bigint REFERENCES users (id),
    actor_label text NOT NULL,
    action      text NOT NULL,
    entity      text NOT NULL,
    entity_id   text NOT NULL,
    before_data jsonb,
    after_data  jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_entity_idx ON audit_log (entity, entity_id, id DESC);

-- +goose StatementBegin
CREATE FUNCTION forbid_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- +goose StatementBegin
CREATE FUNCTION notify_live() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('live_events', json_build_object(
        'id', NEW.id,
        'actor', NEW.actor_label,
        'action', NEW.action,
        'entity', NEW.entity,
        'entity_id', NEW.entity_id,
        'at', NEW.created_at
    )::text);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER audit_log_notify AFTER INSERT ON audit_log
    FOR EACH ROW EXECUTE FUNCTION notify_live();

CREATE TABLE idempotency_keys (
    user_id       bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    idem_key      text NOT NULL CHECK (length(idem_key) BETWEEN 1 AND 128),
    method        text NOT NULL,
    path          text NOT NULL,
    request_hash  bytea NOT NULL,
    status_code   integer,
    response_body bytea,
    locked_until  timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, idem_key)
);

-- +goose Down
DROP TABLE idempotency_keys;
DROP TABLE audit_log;
DROP FUNCTION notify_live();
DROP FUNCTION forbid_mutation();
DROP TABLE hotel_state;
DROP TABLE refresh_tokens;
DROP TABLE users;
DROP TYPE user_role;
