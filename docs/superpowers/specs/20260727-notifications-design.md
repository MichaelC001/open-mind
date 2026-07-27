# Notifications — outbox substrate, mobile push, and three job-driven producers

Date: 2026-07-27. Openmind has no notification support of any kind today. The
async spine that would feed one already exists — River jobs enrich saves, poll
feeds, and scan Lens digests — but the only way any of that reaches a user is
the per-Lens digest e-mail sent through `internal/mailer`. Everything else
happens silently and is discovered only when the user next opens the app.

This spec adds a **delivery substrate** (device registry, pluggable channel
adapters, per-user preferences, coalescing, quiet hours, and a daily cap) plus
the **three job-driven producers** that sit on top of it: Lens digests, feed
river activity, and save-lifecycle exceptions.

## Goal

A user who saves things on mobile finds out — calmly, and on their own terms —
when a digest is ready, when their feeds have accumulated something worth a
look, and when the pipeline gave up on one of their saves.

Concretely:

- Mobile push via Expo, and e-mail via the existing SMTP mailer, as two
  channels over one substrate.
- Per-category, per-channel preferences, with quiet hours and a daily cap, so
  the high-volume producer (feed river) cannot make the feature feel spammy.
- Nothing configured → `noop` → the app stays fully functional, per principle 5.

## Non-goals

- **Per-item reminders** ("remind me about this save"). Requested, and a good
  idea, but it is a distinct feature — new table, new item-detail UI, its own
  scheduling semantics — that merely *consumes* this substrate. Separate spec.
- **Web Push / VAPID.** No service worker, no new web dependency. Web is where
  you configure notifications; mobile is where you receive them.
- **In-app notification inbox.** No bell icon, no notifications list view.
- **Changing the existing digest e-mail.** The digest job keeps sending its
  rich HTML e-mail exactly as it does today (see "Current state"). Push is
  purely additive alongside it.
- **A new required service.** Expo Push is optional and behind config; the
  binary + Postgres deployment story is unchanged.

## Current state (verified)

- `internal/jobs` registers four workers on River's `default` queue
  (`MaxWorkers: 5`): enrich, extract-places, poll-feeds, send-kindle, and
  scan-digests. Two periodic jobs: `poll_feeds` (30 min) and `scan_digests`
  (hourly), both `RunOnStart`.
- `ScanDigestsWorker` is the pattern to copy for fan-out: a periodic scan that
  decides what is due, then enqueues one follow-up job per unit of work.
- `internal/mailer` sends multipart SMTP with attachments, standard library
  only. Used by Send-to-Kindle and digests.
- `internal/ai` is the adapter pattern to mirror: a `Provider` interface, an
  ordered `chain.go`, a `noop.go` that keeps the app whole, and a `fake.go`
  used by tests.
- `user_settings` (migration 0015) is a `(user_id, key, value)` k/v table with
  sqlc queries already generated (`GetSetting`, `UpsertSetting`,
  `DeleteSetting`, `ListSettings`).
- `api_keys` (migration 0011) is the per-device credential — mobile signs in
  and mints an `omk_` key. Revoking it is how a device signs out.
- `openapi.yaml` already exposes `GET /settings` and `PATCH /settings`, where
  PATCH is documented as "only provided fields are changed".
- Latest migration is `0019_item_tagged_location.sql`.
- `apps/mobile` has no `expo-notifications`. `apps/web` has no service worker.

## Data model — migration `0020_notifications.sql`

### `push_devices`

```sql
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
```

Tying the row to `api_key_id` means signing out (which revokes the key)
cascades the push token away — no orphaned tokens pushing at a device that is
no longer signed in. `failed_at` records Expo's `DeviceNotRegistered` verdict
so a dead token stops being retried forever.

### `notifications` — the outbox

```sql
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
```

`category` is one of `digest`, `feed_river`, `lifecycle`. `data` carries the
deep-link payload (`{"item_id": …}`, `{"lens_id": …}`, `{"feed_id": …}`).

The partial unique index is the idempotency guard: a producer re-running — a
River retry, or the digest scan's one-hour grace window overlapping — collapses
into the existing pending row instead of duplicating. The partial due-index
keeps the flush query's working set bounded by *pending* rows rather than total
rows, so the table growing large does not slow delivery.

### `notification_deliveries` — the ledger

```sql
CREATE TABLE notification_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    channel         text NOT NULL,
    ticket_id       text NOT NULL DEFAULT '',
    sent_at         timestamptz NOT NULL DEFAULT now(),
    ok              bool NOT NULL,
    error           text NOT NULL DEFAULT ''
);
CREATE INDEX notification_deliveries_cap_idx
    ON notification_deliveries (user_id, sent_at DESC);
```

Separate from the outbox because one notification fans out to N channels × M
devices, each succeeding or failing independently. `ticket_id` is what the
Expo receipt job later looks up. The `(user_id, sent_at DESC)` index is what
keeps the daily-cap count from degrading into a growing scan.

### Preferences

Reuse `user_settings`; no fourth table, no backfill. An absent key means the
documented default, so existing installs upgrade with sane behaviour.

| Key | Values | Default |
|---|---|---|
| `notify.digest` | `off` / `push` / `email` / `both` | `push` |
| `notify.feed_river` | same | `off` |
| `notify.lifecycle` | same | `push` |
| `notify.quiet_hours` | `"22:00-07:00"`, `""` = none | `""` |
| `notify.timezone` | IANA, e.g. `Europe/London` | `UTC` |
| `notify.daily_cap` | integer | `10` |

`feed_river` defaults to `off` deliberately: it is the one category that can
generate volume, and it should be a thing the user opts into rather than
something that greets them after an upgrade.

## `internal/notify` — the adapter

Mirrors `internal/ai` in shape.

```
notify.go    Notification, Category, Target, Result, Sender
router.go    fan-out across the channels enabled for one user
expo.go      Expo Push provider (batches <=100 tokens per request)
email.go     wraps internal/mailer
noop.go      logs and succeeds
prefs.go     typed view over the user_settings k/v
coalesce.go  pure: []Notification -> []Notification
window.go    pure: quiet-hours and cap arithmetic
```

```go
// Sender delivers notifications over exactly one channel. Senders never touch
// the store: the router resolves targets and writes the ledger, so a sender is
// a pure "given this message and these addresses, deliver" adapter.
type Sender interface {
	Name() string
	Send(ctx context.Context, n Notification, t Target) ([]Result, error)
}
```

`Target` carries the resolved destinations (`[]Device` for push, an address for
e-mail). `Result` is `{Channel, Token, TicketID, OK, Err}` per target, and maps
one-to-one onto `notification_deliveries` rows.

The two pure functions hold the product behaviour, and are the testable core:

- `Coalesce(cat Category, pending []Notification) []Notification` —
  `feed_river` collapses N rows into one *"12 new items across 4 feeds"*;
  `digest` and `lifecycle` pass through untouched. The collapsed row's `data`
  keeps `{feed_id}` when every input row shares one feed, and is otherwise
  empty — which the client reads as "open the feed river root" rather than
  arbitrarily deep-linking to whichever feed sorted first.
- `NextDeliverable(now time.Time, p Prefs) time.Time` — quiet hours become a
  `deliver_after` bump, never a drop.

Neither needs a database or a network, which keeps the noise-control logic
under table-driven unit tests rather than integration tests.

## Jobs — `internal/jobs/notifications.go`

All four run on a **dedicated `notifications` River queue** (`MaxWorkers: 3`),
not `default`. Sharing `default` with enrichment would let a burst of saves
delay every notification; giving notifications their own queue is a two-line
config change now versus a migration under load later.

| Job | Trigger | Behaviour |
|---|---|---|
| `scan_notifications` | periodic, 1 min | `SELECT DISTINCT user_id` over due pending rows; enqueues one flush per user. Mirrors `ScanDigestsWorker`. |
| `flush_notifications{user_id}` | from scan | Load prefs → quiet-hours check → coalesce → cap check → route → ledger → stamp `sent_at`. |
| `check_receipts` | periodic, 15 min | Fetch Expo receipts for tickets from `notification_deliveries` where `ticket_id <> ''` and `sent_at > now() - 1 hour`; `DeviceNotRegistered` sets `push_devices.failed_at`. Overlapping runs re-check the same tickets, which is harmless — setting `failed_at` twice is idempotent — so this needs no "checked" column, just the bounded window. |
| `prune_notifications` | periodic, daily | Delete `notifications` with `sent_at < now() - 30 days` (deliveries cascade), **and** abandoned rows: `sent_at IS NULL AND attempts >= 3 AND created_at < now() - 7 days`. Without the second clause, permanently-failed rows never leave the table and sit in the pending partial index forever. |

Per-user flush jobs (rather than one global loop) give per-user retry isolation
and stop one slow Expo call from head-of-line blocking every other user.

### Cap precedence

`lifecycle` **bypasses the daily cap**. A "we gave up enriching your save"
notification silently swallowed because feed river spent the day's budget is
the single failure mode that would make the feature untrustworthy.
`feed_river` over cap defers to the next day rather than dropping. `digest`
counts against the cap and is only capped out in pathological cases.

### Delivery semantics — at-least-once

The send is an HTTP call, so it cannot sit inside the transaction that stamps
`sent_at`. The flush claims rows (`UPDATE … SET attempts = attempts + 1 …
RETURNING`), sends, then stamps. A crash between send and stamp can re-send
once; `attempts >= 3` gives up and leaves `last_error` populated. Duplicates
are rare and bounded, and a repeated notification is a smaller harm than a lost
one.

Idempotency holds at both levels CLAUDE.md requires: re-running a producer
collapses on `dedupe_key`, and re-running a flush skips already-stamped rows.

## Producers

One insert each; producers know nothing about channels, preferences, or
retries.

- **`digest.go`** — after a successful digest send, insert a `digest` row.
  Dedupe key `digest:<lens_id>:<date>`. The existing e-mail path is untouched.
- **feeds service** — on new river items, insert one `feed_river` row **per
  feed**, dedupe key `feed_river:<feed_id>:<date-hour>`.

  Per-feed rather than one row per hour, because the partial unique index makes
  a duplicate insert a no-op: a single `feed_river:<date-hour>` key would keep
  whatever title the *first* insert wrote ("1 new item") and silently discard
  every later one, so the *"12 new items across 4 feeds"* message could never
  materialise. Emitting a row per feed gives `Coalesce` the N rows it needs to
  actually count, and still collapses repeat polls of the same feed within the
  hour.
- **`enrich.go`** — on *terminal* failure only (River retries exhausted),
  insert a `lifecycle` row. Successful enrichment stays silent: the item simply
  appears in the Library, which is the point of *capture is sacred*.

### Offline-queue drain is client-side, not a producer

"Your offline queue drained 6 saves" cannot be a server push — the server sees
an ordinary `POST /api/assets` and has no way to know it was a drain. It is a
**local** notification scheduled by the mobile client via `expo-notifications`,
hooking the existing drain path from PR #44. Worth having; involves no
substrate.

## API contract — `openapi.yaml`

Edit the spec first, then `task generate`, then implement the handler. Never
the other way round.

**Extend `Settings` and `PatchSettingsRequest`** rather than adding a parallel
preferences endpoint — the documented PATCH semantics already fit per-key
preferences:

```yaml
notifyDigest:     { type: string, enum: [off, push, email, both] }
notifyFeedRiver:  { type: string, enum: [off, push, email, both] }
notifyLifecycle:  { type: string, enum: [off, push, email, both] }
notifyQuietHours: { type: string }
notifyTimezone:   { type: string }
notifyDailyCap:   { type: integer }
```

**Two new operations:**

- `POST /push-devices` — `{token, platform}`, idempotent upsert on `token`,
  tied to the calling API key. → `204`.
- `POST /push-devices/unregister` — `{token}` → `204`.

Deliberately *not* `DELETE /push-devices/{token}`: an Expo token is literally
`ExponentPushToken[xxx]`, and bracket characters in a path segment are an
encoding trap across four generated clients for no benefit. Since signing out
revokes the API key and cascades the device row, explicit unregister exists for
"mute this device", not for cleanup.

## Mobile — `apps/mobile`

Adds `expo-notifications`. **Requires a fresh dev build** (native module), as
with the maps and share-extension work before it.

**Permission timing is the part that matters.** iOS grants exactly one shot at
the system prompt; once denied, the only recovery is sending the user into
Settings. Therefore: no cold prompt on launch. A "Notifications" row in the
app's settings screen, off by default, and the OS prompt fires only when the
user turns it on. On grant → `getExpoPushTokenAsync({ projectId })` →
`POST /push-devices`.

Tap handling maps `data` onto `expo-router`: `item_id` → `/item/[id]`,
`lens_id` → the lens view, `feed_id` → the feed river. Android needs an
explicit notification channel (cobalt accent, default importance) or
notifications arrive silently.

## Web — `apps/web`

Preference toggles on the existing settings page, through the regenerated
`packages/api-client`. No service worker, no new dependency.

## Error handling

- **Expo transport failure** — the flush job returns the error; River retries
  the flush, not the producer. `attempts >= 3` gives up with `last_error` set.
- **Partial fan-out** — a push failing while e-mail succeeds writes one `ok`
  row and one failed row; the notification is still stamped `sent_at`. Delivery
  is best-effort per channel, not all-or-nothing.
- **`DeviceNotRegistered`** — surfaces asynchronously in the receipt, never at
  send time. `check_receipts` sets `failed_at`, and the device drops out of the
  target query via the partial index.
- **No devices registered but `push` selected** — not an error. The push
  channel yields zero targets and the notification is stamped sent. If the
  preference is `both`, e-mail still goes.
- **Unparseable `notify.timezone`** — fall back to UTC and log once; never
  block delivery on a bad preference value.
- **Nothing configured (`NOTIFY_CHANNELS` empty)** — `noop` logs and succeeds.
  Producers keep inserting; rows are stamped sent. No error path.

## Testing

| Layer | Approach |
|---|---|
| `Coalesce`, `NextDeliverable` | Table-driven, pure, no DB. Quiet-hours edge cases: midnight wrap, DST transitions, empty range, cap boundary. |
| Router | Fake `Sender` mirroring `ai/fake.go`; assert per-channel fan-out and partial-failure ledger rows. |
| `expo.go` | `httptest` server: <=100-token batching, ticket parsing, receipt → `failed_at`. |
| Store | Real Postgres per CLAUDE.md — in particular the partial unique index actually collapsing a duplicate producer insert. |
| Jobs | An idempotency test per job (run twice, same result), as CLAUDE.md requires. |
| Mobile | `jest-expo` harness from PR #44: registration posts once; deep-link mapping is total over the `data` shapes. |

DST is the easiest thing here to get wrong. Quiet hours are wall-clock in the
user's zone, so `NextDeliverable` must do its arithmetic in `notify.timezone`,
not as UTC plus a fixed offset.

## Rollout

1. `NOTIFY_CHANNELS` (empty → noop) and `EXPO_ACCESS_TOKEN` (optional)
   documented in `docs/self-hosting.md`.
2. **Both added to the `api` service block in `docker-compose.yml`.** New API
   env vars that skip that block never reach the container — this has bitten
   the project twice, so it is an explicit step, not an assumed one.
3. Ship server-side first: substrate + producers land with `NOTIFY_CHANNELS`
   unset, so the outbox fills and prunes with no delivery. Verifies the write
   path under real traffic before anyone receives anything.
4. Mobile dev build with `expo-notifications`; register a device; enable
   `expo` on the hosted instance.
5. `feed_river` stays default-`off`; turn it on for the maintainer's own
   account first and live with the volume for a week before recommending it.

Stock `docker compose up` with nothing configured behaves exactly as it does
today.
