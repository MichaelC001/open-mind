-- One-off: adopt pre-provenance feed items (backfilled before feed_id existed).
-- Run the SELECT first; apply the UPDATE only after reviewing the count/rows.
-- Matches items to their feed by: created around/after the subscription,
-- unkept, provenance-less, and URL host equal to the feed's site host.
--
-- Host comparison is anchored to avoid substring false-positives (e.g. a
-- naive `position(... in ...)` match would let "blog.co" match
-- "myblog.com"). We extract the item URL's host with a regex capture group
-- and compare it for equality against the feed's host (www-stripped), so
-- "blog.co" only matches "blog.co", not "myblog.com" or "blog.co.uk".
-- Residual risk: this is still a straight equality match, so a feed whose
-- site_url host differs from the actual publishing host (e.g. a feed
-- aggregator, or a custom CDN/short-link domain) will not be adopted here
-- and needs manual review. Always run the preview SELECT and eyeball the
-- rows before uncommenting/running the UPDATE.

-- Preview:
SELECT i.id, i.url, f.title AS feed_title
FROM items i
JOIN feeds f ON f.user_id = i.user_id
WHERE i.feed_id IS NULL AND i.kept_at IS NULL
  AND i.created_at >= f.created_at - interval '5 minutes'
  AND f.site_url <> ''
  AND substring(i.url from '^https?://(?:www\.)?([^/]+)')
      = regexp_replace(f.site_url, '^https?://(?:www\.)?([^/]+).*$', '\1');

-- Apply (after review):
-- UPDATE items i SET feed_id = f.id
-- FROM feeds f
-- WHERE f.user_id = i.user_id
--   AND i.feed_id IS NULL AND i.kept_at IS NULL
--   AND i.created_at >= f.created_at - interval '5 minutes'
--   AND f.site_url <> ''
--   AND substring(i.url from '^https?://(?:www\.)?([^/]+)')
--       = regexp_replace(f.site_url, '^https?://(?:www\.)?([^/]+).*$', '\1');
