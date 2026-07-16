ALTER TABLE items ADD COLUMN feed_id uuid REFERENCES feeds(id) ON DELETE SET NULL;
ALTER TABLE items ADD COLUMN kept_at timestamptz;
CREATE INDEX items_feed_idx ON items (user_id, feed_id) WHERE feed_id IS NOT NULL;
