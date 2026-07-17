-- Per-feed adaptive polling. next_poll_at gates when the periodic poller may
-- refresh a feed; poll_interval_minutes is the current interval that backs off
-- (doubles) while a feed is quiet and resets to the floor on new items.
-- Defaults make every existing feed eligible immediately at the 30-min floor,
-- so behaviour on first run after deploy is unchanged.
ALTER TABLE feeds ADD COLUMN next_poll_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE feeds ADD COLUMN poll_interval_minutes int NOT NULL DEFAULT 30;
CREATE INDEX feeds_next_poll_idx ON feeds (next_poll_at);
