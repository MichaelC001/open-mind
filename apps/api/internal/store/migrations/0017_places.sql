CREATE TABLE item_places (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    name text NOT NULL,
    hint text NOT NULL DEFAULT '',
    address text NOT NULL DEFAULT '',
    lat double precision,
    lng double precision,
    source text NOT NULL DEFAULT 'caption',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (item_id, name)
);
CREATE INDEX item_places_user_item_idx ON item_places (user_id, item_id);
