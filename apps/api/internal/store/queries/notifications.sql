-- name: EnqueueNotification :exec
-- ON CONFLICT DO NOTHING is the idempotency guard: the partial unique index
-- covers pending rows only, so a producer re-run collapses into the existing
-- row rather than duplicating, while a fresh window still gets its own row.
--
-- deliver_after is deliberately omitted so the column DEFAULT now() applies.
-- Listing it as a parameter would make sqlc generate a required field, and a
-- caller leaving it zero would send an explicit NULL — which a DEFAULT does
-- not rescue, because DEFAULT only fires for columns absent from the INSERT.
-- Deferral (quiet hours, cap) happens later via DeferNotifications.
INSERT INTO notifications (user_id, category, dedupe_key, title, body, data)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, dedupe_key) WHERE sent_at IS NULL DO NOTHING;

-- name: ListUsersWithDueNotifications :many
-- Deliberately unscoped: the flush job's whole purpose is to enumerate every
-- user with due work, so a user_id predicate would defeat it.
SELECT DISTINCT user_id FROM notifications
WHERE sent_at IS NULL AND attempts < 3 AND deliver_after <= now();

-- name: ListDueNotifications :many
SELECT id, category, dedupe_key, title, body, data
FROM notifications
WHERE user_id = $1 AND sent_at IS NULL AND attempts < 3 AND deliver_after <= now()
ORDER BY created_at;

-- name: ClaimNotifications :exec
UPDATE notifications SET attempts = attempts + 1
WHERE user_id = $1 AND id = ANY(@ids::uuid[]);

-- name: MarkNotificationsSent :exec
UPDATE notifications SET sent_at = now(), last_error = ''
WHERE user_id = $1 AND id = ANY(@ids::uuid[]);

-- name: MarkNotificationsFailed :exec
UPDATE notifications SET last_error = $2
WHERE user_id = $1 AND id = ANY(@ids::uuid[]);

-- name: DeferNotifications :exec
UPDATE notifications SET deliver_after = $2
WHERE user_id = $1 AND id = ANY(@ids::uuid[]);

-- name: CountDeliveriesSince :one
SELECT count(*) FROM notification_deliveries
WHERE user_id = $1 AND ok AND sent_at >= $2;

-- name: RecordDelivery :exec
INSERT INTO notification_deliveries (user_id, notification_id, channel, token, ticket_id, ok, error)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetUserEmail :one
-- The flush job needs the account e-mail to resolve the e-mail channel's
-- target. Scoped by id, which is the caller's own user_id.
SELECT email FROM users WHERE id = $1;

-- name: UpsertPushDevice :exec
INSERT INTO push_devices (user_id, api_key_id, token, platform)
VALUES ($1, $2, $3, $4)
ON CONFLICT (token) DO UPDATE
SET user_id = EXCLUDED.user_id,
    api_key_id = EXCLUDED.api_key_id,
    platform = EXCLUDED.platform,
    last_seen_at = now(),
    failed_at = NULL;

-- name: DeletePushDevice :execrows
DELETE FROM push_devices WHERE user_id = $1 AND token = $2;

-- name: ListPushDevices :many
SELECT token, platform FROM push_devices
WHERE user_id = $1 AND failed_at IS NULL;

-- name: MarkPushDeviceFailed :exec
-- Deliberately unscoped: token is UNIQUE, so it alone identifies exactly one
-- row and a user_id predicate would be redundant.
UPDATE push_devices SET failed_at = now() WHERE token = $1;

-- name: ListRecentTickets :many
-- The receipt job needs the token, not just the ticket, so it can retire a
-- device Expo reports as unregistered.
SELECT ticket_id, token FROM notification_deliveries
WHERE channel = 'expo' AND ok AND ticket_id <> '' AND token <> ''
  AND sent_at > now() - interval '1 hour';

-- name: PruneNotifications :execrows
-- Two clauses: delivered rows age out after 30 days, and abandoned rows
-- (retries exhausted, never sent) after 7 — without the second, failed rows
-- would sit in the pending partial index forever.
DELETE FROM notifications
WHERE (sent_at IS NOT NULL AND sent_at < now() - interval '30 days')
   OR (sent_at IS NULL AND attempts >= 3 AND created_at < now() - interval '7 days');
