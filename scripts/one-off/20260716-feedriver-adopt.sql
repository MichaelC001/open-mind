-- One-off: adopt pre-provenance feed items (backfilled before feed_id existed).
-- Run the SELECT first; apply the UPDATE only after reviewing the count/rows.
-- Matches items to their feed by: created around/after the subscription,
-- unkept, provenance-less, and URL host related to the feed's site host.

-- Preview:
SELECT i.id, i.url, f.title AS feed_title
FROM items i
JOIN feeds f ON f.user_id = i.user_id
WHERE i.feed_id IS NULL AND i.kept_at IS NULL
  AND i.created_at >= f.created_at - interval '5 minutes'
  AND f.site_url <> ''
  AND position(regexp_replace(f.site_url, '^https?://(www\.)?', '') in i.url) > 0;

-- Apply (after review):
-- UPDATE items i SET feed_id = f.id
-- FROM feeds f
-- WHERE f.user_id = i.user_id
--   AND i.feed_id IS NULL AND i.kept_at IS NULL
--   AND i.created_at >= f.created_at - interval '5 minutes'
--   AND f.site_url <> ''
--   AND position(regexp_replace(f.site_url, '^https?://(www\.)?', '') in i.url) > 0;
