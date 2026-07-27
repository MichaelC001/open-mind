CREATE TABLE push_devices (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id   uuid REFERENCES api_keys(id) ON DELETE CASCADE,
    token        text NOT NULL UNIQUE,
    platform     text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    failed_at    timestamptz
);
CREATE INDEX push_devices_user_idx ON push_devices (user_id) WHERE failed_at IS NULL;

CREATE TABLE notifications (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category      text NOT NULL,
    dedupe_key    text NOT NULL,
    title         text NOT NULL,
    body          text NOT NULL,
    data          jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    deliver_after timestamptz NOT NULL DEFAULT now(),
    sent_at       timestamptz,
    attempts      int NOT NULL DEFAULT 0,
    last_error    text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX notifications_pending_dedupe_idx
    ON notifications (user_id, dedupe_key) WHERE sent_at IS NULL;
CREATE INDEX notifications_due_idx
    ON notifications (deliver_after) WHERE sent_at IS NULL;

CREATE TABLE notification_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    channel         text NOT NULL,
    -- token is the push destination this row is about. The receipt job needs
    -- it to retire a dead device, and the ledger is the only place the
    -- ticket-to-token mapping survives. Empty for e-mail.
    token           text NOT NULL DEFAULT '',
    ticket_id       text NOT NULL DEFAULT '',
    sent_at         timestamptz NOT NULL DEFAULT now(),
    ok              bool NOT NULL,
    error           text NOT NULL DEFAULT ''
);
CREATE INDEX notification_deliveries_cap_idx
    ON notification_deliveries (user_id, sent_at DESC);
