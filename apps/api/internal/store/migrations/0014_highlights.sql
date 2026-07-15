CREATE TABLE highlights (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    source_item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    quote_item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    exact text NOT NULL,
    prefix text NOT NULL DEFAULT '',
    suffix text NOT NULL DEFAULT '',
    offset_hint int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX highlights_source_idx ON highlights (user_id, source_item_id);
