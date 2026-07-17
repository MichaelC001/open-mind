-- name: CreateFeed :one
INSERT INTO feeds (user_id, url, title, site_url) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListFeeds :many
SELECT * FROM feeds WHERE user_id = $1 ORDER BY created_at DESC;

-- name: GetFeed :one
SELECT * FROM feeds WHERE user_id = $1 AND id = $2;

-- name: DeleteFeed :execrows
DELETE FROM feeds WHERE user_id = $1 AND id = $2;

-- name: SetFeedPolled :exec
UPDATE feeds SET last_polled_at = $3, last_status = $4, etag = $5, last_modified = $6,
  next_poll_at = $7, poll_interval_minutes = $8
WHERE user_id = $1 AND id = $2;

-- name: KeepFeedItems :execrows
-- Unsubscribing keeps the feed's items in the library: stamp everything
-- still unkept so the Mind predicate keeps showing them once feed_id nulls.
UPDATE items SET kept_at = now(), updated_at = now()
WHERE user_id = $1 AND feed_id = $2 AND kept_at IS NULL;

-- name: ListFeedsDueForPoll :many
-- Cross-user by design (system-wide poller). Only feeds whose adaptive
-- schedule has come due; items saved remain scoped to each feed's user_id.
SELECT * FROM feeds WHERE next_poll_at <= now() ORDER BY next_poll_at ASC;
