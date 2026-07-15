CREATE TABLE user_settings (
    user_id uuid NOT NULL REFERENCES users(id),
    key text NOT NULL,
    value text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
ALTER TABLE lenses ADD COLUMN digest_schedule text NOT NULL DEFAULT '';
ALTER TABLE lenses ADD COLUMN last_digest_at timestamptz;
