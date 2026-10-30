-- +goose Up
CREATE TYPE folio_line_kind AS ENUM ('room', 'tax_kdv', 'tax_accom', 'extra', 'payment', 'refund', 'transfer');

CREATE TABLE folios (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    reservation_id bigint NOT NULL REFERENCES reservations (id) ON DELETE CASCADE,
    payer_type     text NOT NULL CHECK (payer_type IN ('guest', 'company')),
    payer_name     text NOT NULL CHECK (payer_name <> ''),
    tax_number     text,
    is_primary     boolean NOT NULL DEFAULT false,
    balance_kurus  bigint NOT NULL DEFAULT 0,
    closed_at      timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX folios_one_primary_idx ON folios (reservation_id) WHERE is_primary;
CREATE INDEX folios_reservation_idx ON folios (reservation_id);

CREATE TABLE folio_lines (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    folio_id          bigint NOT NULL REFERENCES folios (id),
    reservation_id    bigint NOT NULL REFERENCES reservations (id),
    kind              folio_line_kind NOT NULL,
    amount_kurus      bigint NOT NULL CHECK (amount_kurus <> 0),
    description       text NOT NULL,
    business_date     date NOT NULL,
    method            text CHECK (method IN ('nakit', 'kart', 'havale')),
    transfer_group    uuid,
    refund_of_line_id bigint REFERENCES folio_lines (id),
    posted_by         bigint REFERENCES users (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT folio_lines_sign CHECK (
        (kind = 'payment' AND amount_kurus < 0)
        OR (kind = 'refund' AND amount_kurus > 0)
        OR (kind IN ('room', 'tax_kdv', 'tax_accom') AND amount_kurus > 0)
        OR kind IN ('extra', 'transfer')
    )
);
CREATE INDEX folio_lines_folio_idx ON folio_lines (folio_id, id);
CREATE UNIQUE INDEX folio_lines_single_night_charge_idx ON folio_lines (reservation_id, business_date, kind)
    WHERE kind IN ('room', 'tax_kdv', 'tax_accom');

CREATE TRIGGER folio_lines_append_only BEFORE UPDATE OR DELETE ON folio_lines
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- +goose StatementBegin
CREATE FUNCTION folio_apply_line() RETURNS trigger AS $$
BEGIN
    UPDATE folios SET balance_kurus = balance_kurus + NEW.amount_kurus
    WHERE id = NEW.folio_id AND closed_at IS NULL;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'folio % is closed', NEW.folio_id USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER folio_lines_apply AFTER INSERT ON folio_lines
    FOR EACH ROW EXECUTE FUNCTION folio_apply_line();

-- +goose StatementBegin
CREATE FUNCTION folio_guard_balance() RETURNS trigger AS $$
BEGIN
    IF NEW.balance_kurus IS DISTINCT FROM OLD.balance_kurus AND pg_trigger_depth() < 2 THEN
        RAISE EXCEPTION 'folio balance changes only through folio_lines' USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER folios_guard_balance BEFORE UPDATE ON folios
    FOR EACH ROW EXECUTE FUNCTION folio_guard_balance();

CREATE VIEW folio_integrity AS
    SELECT f.id AS folio_id,
           f.balance_kurus,
           COALESCE(SUM(l.amount_kurus), 0)::bigint AS lines_total_kurus
    FROM folios f
    LEFT JOIN folio_lines l ON l.folio_id = f.id
    GROUP BY f.id;

CREATE TABLE night_audit_runs (
    business_date date PRIMARY KEY,
    status        text NOT NULL CHECK (status IN ('running', 'completed')),
    total_items   integer NOT NULL DEFAULT 0,
    done_items    integer NOT NULL DEFAULT 0,
    attempts      integer NOT NULL DEFAULT 1,
    last_error    text,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);

CREATE TABLE night_audit_items (
    business_date  date NOT NULL REFERENCES night_audit_runs (business_date),
    reservation_id bigint NOT NULL REFERENCES reservations (id),
    done_at        timestamptz,
    PRIMARY KEY (business_date, reservation_id)
);
CREATE INDEX night_audit_items_pending_idx ON night_audit_items (business_date, reservation_id) WHERE done_at IS NULL;

CREATE TABLE daily_stats (
    stay_date          date PRIMARY KEY,
    rooms_available    integer NOT NULL CHECK (rooms_available >= 0),
    rooms_sold         integer NOT NULL CHECK (rooms_sold >= 0),
    room_revenue_kurus bigint NOT NULL CHECK (room_revenue_kurus >= 0)
);

-- +goose Down
DROP TABLE daily_stats;
DROP TABLE night_audit_items;
DROP TABLE night_audit_runs;
DROP VIEW folio_integrity;
DROP TABLE folio_lines;
DROP TABLE folios;
DROP FUNCTION folio_guard_balance();
DROP FUNCTION folio_apply_line();
DROP TYPE folio_line_kind;
