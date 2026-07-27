# Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver Lens-digest, feed-river, and save-failure notifications to users via Expo mobile push and SMTP e-mail, through one outbox-backed substrate with coalescing, quiet hours, and a daily cap.

**Architecture:** Producers (`digest.go`, feeds service, `enrich.go`) insert rows into a `notifications` outbox table and know nothing about channels. A periodic `scan_notifications` River job fans out one `flush_notifications{user_id}` job per user with due rows; the flush applies preferences → coalescing → quiet hours → daily cap, then routes through `internal/notify` senders (`expo`, `email`, `noop`) and records every attempt in a `notification_deliveries` ledger. Two more periodic jobs reconcile Expo receipts and prune old rows.

**Tech Stack:** Go 1.x (stdlib-first), chi, River (jobs), sqlc + pgx/v5, oapi-codegen, Postgres. Mobile: Expo SDK 57, `expo-notifications`, expo-router. Web: Next.js 15.

**Spec:** `docs/superpowers/specs/20260727-notifications-design.md`

## Global Constraints

- **The contract is the spine.** Never add a Go route that is not in `openapi.yaml`. Edit `openapi.yaml` → run `task generate` → implement the handler. Never hand-edit `apps/api/internal/api/gen.go` or `packages/api-client`.
- **No new required infrastructure.** Postgres only. Expo Push is optional and behind config.
- **`noop` must keep the app fully functional.** `NOTIFY_CHANNELS` unset → notifications are stamped sent, nothing is delivered, nothing errors.
- **Multi-tenant.** Every store method takes `ctx` and is scoped by `user_id`. A query without a `user_id` predicate is a bug.
- **No inline SQL in handlers or jobs.** All queries go through sqlc in `internal/store/queries/`.
- **Errors wrap:** `fmt.Errorf("doing x: %w", err)`.
- **Comment style:** no banner/divider comments (`// ===== Section =====`). Write prose comments that explain *why*, matching the density of surrounding code.
- **Every job needs an idempotency test** (run twice, same result).
- **Categories** are exactly `digest`, `feed_river`, `lifecycle`. **Channels** are exactly `expo`, `email`.
- **Preference values** are exactly `off`, `push`, `email`, `both`.
- **Defaults:** `notify.digest=push`, `notify.feed_river=off`, `notify.lifecycle=push`, `notify.quiet_hours=""`, `notify.timezone=UTC`, `notify.daily_cap=10`.
- Go work runs from `apps/api` with plain `go`. Store tests need Postgres: `TEST_DATABASE_URL`, defaulting to `postgres://openmind:openmind@localhost:5433/openmind_test`.

---

## File Structure

**Create:**
- `apps/api/internal/store/migrations/0020_notifications.sql` — three tables + indexes
- `apps/api/internal/store/queries/notifications.sql` — sqlc queries for outbox, devices, ledger
- `apps/api/internal/notify/notify.go` — `Notification`, `Category`, `Device`, `Target`, `Result`, `Sender`
- `apps/api/internal/notify/prefs.go` — typed view over `user_settings`
- `apps/api/internal/notify/window.go` — `NextDeliverable` (pure)
- `apps/api/internal/notify/coalesce.go` — `Coalesce` (pure)
- `apps/api/internal/notify/noop.go` — noop sender
- `apps/api/internal/notify/fake.go` — recording sender for tests
- `apps/api/internal/notify/email.go` — wraps `internal/mailer`
- `apps/api/internal/notify/expo.go` — Expo Push + receipts
- `apps/api/internal/notify/router.go` — per-user fan-out
- `apps/api/internal/jobs/notifications.go` — four workers
- `apps/api/internal/api/pushdevices.go` — two handlers

**Modify:**
- `openapi.yaml` — `Settings`/`PatchSettingsRequest` fields, two `/push-devices` operations
- `apps/api/internal/api/middleware.go` — carry API key ID in context
- `apps/api/internal/api/auth.go` — attach API key ID
- `apps/api/internal/api/settings.go` — notification preference fields
- `apps/api/internal/jobs/enrich.go` — register workers + `notifications` queue + periodic jobs
- `apps/api/internal/jobs/digest.go` — `digest` producer
- `apps/api/internal/feeds/service.go` — `feed_river` producer
- `apps/api/cmd/openmind/main.go` — build the router from env
- `docker-compose.yml` — `NOTIFY_CHANNELS`, `EXPO_ACCESS_TOKEN` in the `api` block
- `.env.example`, `docs/self-hosting.md` — document both
- `apps/mobile/*` — registration, permission UI, tap routing
- `apps/web/*` — preference toggles

---

## Task 1: Schema and queries

**Files:**
- Create: `apps/api/internal/store/migrations/0020_notifications.sql`
- Create: `apps/api/internal/store/queries/notifications.sql`
- Test: `apps/api/internal/store/notifications_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: sqlc-generated `db.EnqueueNotificationParams`, `db.ListDueNotificationsRow`, `db.ClaimNotificationsParams`, `db.MarkNotificationsSentParams`, `db.MarkNotificationsFailedParams`, `db.DeferNotificationsParams`, `db.CountDeliveriesSinceParams`, `db.RecordDeliveryParams`, `db.UpsertPushDeviceParams`, `db.DeletePushDeviceParams`, `db.ListPushDevicesRow`, `db.ListRecentTicketsRow`, plus the parameterless `db.ListUsersWithDueNotifications`, `db.PruneNotifications`, and the single-argument `db.MarkPushDeviceFailed(ctx, token)` and `db.GetUserEmail(ctx, userID)`.

- [ ] **Step 1: Write the migration**

Create `apps/api/internal/store/migrations/0020_notifications.sql`:

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

CREATE TABLE notification_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    channel         text NOT NULL,
    -- token is the push destination this row is about. The receipt job needs
    -- it to retire a dead device, and the ledger is the only place the
    -- ticket-to-token mapping survives. Empty for e-mail.
    token           text NOT NULL DEFAULT '',
    ticket_id       text NOT NULL DEFAULT '',
    sent_at         timestamptz NOT NULL DEFAULT now(),
    ok              bool NOT NULL,
    error           text NOT NULL DEFAULT ''
);
CREATE INDEX notification_deliveries_cap_idx
    ON notification_deliveries (user_id, sent_at DESC);
```

- [ ] **Step 2: Write the sqlc queries**

Create `apps/api/internal/store/queries/notifications.sql`:

```sql
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
```

- [ ] **Step 3: Write the failing store test**

Create `apps/api/internal/store/notifications_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// TestEnqueueNotificationDedupes proves the partial unique index actually
// collapses a duplicate producer insert while the first row is still pending,
// and that the same key is insertable again once the first row is sent.
func TestEnqueueNotificationDedupes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	arg := db.EnqueueNotificationParams{
		UserID:    uid,
		Category:  "feed_river",
		DedupeKey: "feed_river:abc:2026-07-27T09",
		Title:     "1 new item",
		Body:      "",
		Data:      []byte(`{}`),
	}
	for range 3 {
		if err := s.Queries.EnqueueNotification(ctx, arg); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	due, err := s.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("pending rows = %d, want 1", len(due))
	}

	if err := s.Queries.MarkNotificationsSent(ctx, db.MarkNotificationsSentParams{UserID: uid, Ids: []uuid.UUID{due[0].ID}}); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if err := s.Queries.EnqueueNotification(ctx, arg); err != nil {
		t.Fatalf("re-enqueue after send: %v", err)
	}
	due2, err := s.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due 2: %v", err)
	}
	if len(due2) != 1 {
		t.Fatalf("pending rows after re-enqueue = %d, want 1", len(due2))
	}
}

// TestListPushDevicesSkipsFailed proves a device marked DeviceNotRegistered
// drops out of the target query.
func TestListPushDevicesSkipsFailed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	for _, tok := range []string{"ExponentPushToken[a]", "ExponentPushToken[b]"} {
		if err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: tok, Platform: "ios"}); err != nil {
			t.Fatalf("upsert %s: %v", tok, err)
		}
	}
	if err := s.Queries.MarkPushDeviceFailed(ctx, "ExponentPushToken[a]"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	devices, err := s.Queries.ListPushDevices(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 || devices[0].Token != "ExponentPushToken[b]" {
		t.Fatalf("devices = %+v, want only token b", devices)
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

```bash
cd apps/api && go test ./internal/store/ -run 'TestEnqueueNotificationDedupes|TestListPushDevicesSkipsFailed' -v
```

Expected: FAIL — `s.Queries.EnqueueNotification undefined` (sqlc has not generated yet).

- [ ] **Step 5: Generate sqlc code**

```bash
task generate
```

This regenerates `apps/api/internal/store/db/notifications.sql.go`. Do not hand-edit it.

- [ ] **Step 6: Run the test to verify it passes**

Postgres must be up (`task dev` or `docker compose up db`).

```bash
cd apps/api && go test ./internal/store/ -run 'TestEnqueueNotificationDedupes|TestListPushDevicesSkipsFailed' -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal/store/migrations/0020_notifications.sql \
        apps/api/internal/store/queries/notifications.sql \
        apps/api/internal/store/db/ \
        apps/api/internal/store/notifications_test.go
git commit -m "feat(store): notifications outbox, push devices, and delivery ledger"
```

---

## Task 2: Core types and the two pure functions

**Files:**
- Create: `apps/api/internal/notify/notify.go`, `prefs.go`, `window.go`, `coalesce.go`
- Test: `apps/api/internal/notify/prefs_test.go`, `window_test.go`, `coalesce_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 (pure package, no store access).
- Produces:
  - `type Category string` with `CategoryDigest`, `CategoryFeedRiver`, `CategoryLifecycle`
  - `type Notification struct { ID uuid.UUID; UserID uuid.UUID; Category Category; DedupeKey, Title, Body string; Data map[string]any; SourceIDs []uuid.UUID }`
  - `type Device struct { Token, Platform string }`
  - `type Target struct { Devices []Device; Email string }`
  - `type Result struct { Channel, Token, TicketID string; OK bool; Err error }`
  - `type Sender interface { Name() string; Send(ctx context.Context, n Notification, t Target) ([]Result, error) }`
  - `type Prefs struct { Digest, FeedRiver, Lifecycle Channels; QuietFrom, QuietTo string; Location *time.Location; DailyCap int }`
  - `type Channels struct { Push, Email bool }`
  - `func ParsePrefs(rows map[string]string) Prefs`
  - `func (p Prefs) For(c Category) Channels`
  - `func NextDeliverable(now time.Time, p Prefs) time.Time`
  - `func Coalesce(c Category, pending []Notification) []Notification`

- [ ] **Step 1: Write the failing prefs test**

Create `apps/api/internal/notify/prefs_test.go`:

```go
package notify

import "testing"

func TestParsePrefsDefaults(t *testing.T) {
	p := ParsePrefs(nil)
	if got := p.For(CategoryDigest); !got.Push || got.Email {
		t.Errorf("digest default = %+v, want push only", got)
	}
	if got := p.For(CategoryFeedRiver); got.Push || got.Email {
		t.Errorf("feed_river default = %+v, want off", got)
	}
	if got := p.For(CategoryLifecycle); !got.Push || got.Email {
		t.Errorf("lifecycle default = %+v, want push only", got)
	}
	if p.DailyCap != 10 {
		t.Errorf("DailyCap = %d, want 10", p.DailyCap)
	}
	if p.Location.String() != "UTC" {
		t.Errorf("Location = %s, want UTC", p.Location)
	}
}

func TestParsePrefsOverrides(t *testing.T) {
	p := ParsePrefs(map[string]string{
		"notify.digest":      "both",
		"notify.feed_river":  "email",
		"notify.lifecycle":   "off",
		"notify.quiet_hours": "22:00-07:00",
		"notify.timezone":    "Europe/London",
		"notify.daily_cap":   "3",
	})
	if got := p.For(CategoryDigest); !got.Push || !got.Email {
		t.Errorf("digest = %+v, want both", got)
	}
	if got := p.For(CategoryFeedRiver); got.Push || !got.Email {
		t.Errorf("feed_river = %+v, want email only", got)
	}
	if got := p.For(CategoryLifecycle); got.Push || got.Email {
		t.Errorf("lifecycle = %+v, want off", got)
	}
	if p.QuietFrom != "22:00" || p.QuietTo != "07:00" {
		t.Errorf("quiet = %s-%s, want 22:00-07:00", p.QuietFrom, p.QuietTo)
	}
	if p.Location.String() != "Europe/London" {
		t.Errorf("Location = %s, want Europe/London", p.Location)
	}
	if p.DailyCap != 3 {
		t.Errorf("DailyCap = %d, want 3", p.DailyCap)
	}
}

// A bad timezone or cap must never block delivery — fall back to the default.
func TestParsePrefsBadValuesFallBack(t *testing.T) {
	p := ParsePrefs(map[string]string{
		"notify.timezone":    "Mars/Olympus",
		"notify.daily_cap":   "not-a-number",
		"notify.quiet_hours": "garbage",
	})
	if p.Location.String() != "UTC" {
		t.Errorf("Location = %s, want UTC fallback", p.Location)
	}
	if p.DailyCap != 10 {
		t.Errorf("DailyCap = %d, want 10 fallback", p.DailyCap)
	}
	if p.QuietFrom != "" || p.QuietTo != "" {
		t.Errorf("quiet = %s-%s, want empty on garbage", p.QuietFrom, p.QuietTo)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd apps/api && go test ./internal/notify/ -run TestParsePrefs -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `notify.go`**

Create `apps/api/internal/notify/notify.go`:

```go
// Package notify delivers user-facing notifications over pluggable channels.
// It mirrors internal/ai: a Sender interface per channel, a noop that keeps
// the app fully functional with nothing configured, and a fake for tests.
//
// Senders never touch the store. The router resolves targets and writes the
// delivery ledger, so a Sender is a pure "given this message and these
// addresses, deliver" adapter.
package notify

import (
	"context"

	"github.com/google/uuid"
)

// Category groups notifications for preference and coalescing purposes.
type Category string

const (
	CategoryDigest    Category = "digest"
	CategoryFeedRiver Category = "feed_river"
	CategoryLifecycle Category = "lifecycle"
)

// Notification is one user-facing message. SourceIDs carries the outbox row
// IDs it was built from — one for a pass-through category, many for a
// coalesced feed-river message — so the flush knows which rows to stamp.
type Notification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Category  Category
	DedupeKey string
	Title     string
	Body      string
	Data      map[string]any
	SourceIDs []uuid.UUID
}

// Device is one registered push target.
type Device struct {
	Token    string
	Platform string
}

// Target holds the resolved destinations for a single user.
type Target struct {
	Devices []Device
	Email   string
}

// Result is the outcome for one destination. Token is empty for e-mail.
// TicketID is set only by Expo, which reports delivery failures later via a
// separate receipts call.
type Result struct {
	Channel  string
	Token    string
	TicketID string
	OK       bool
	Err      error
}

// Sender delivers notifications over exactly one channel.
type Sender interface {
	Name() string
	Send(ctx context.Context, n Notification, t Target) ([]Result, error)
}
```

- [ ] **Step 4: Write `prefs.go`**

Create `apps/api/internal/notify/prefs.go`:

```go
package notify

import (
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// user_settings keys backing Prefs.
const (
	KeyDigest     = "notify.digest"
	KeyFeedRiver  = "notify.feed_river"
	KeyLifecycle  = "notify.lifecycle"
	KeyQuietHours = "notify.quiet_hours"
	KeyTimezone   = "notify.timezone"
	KeyDailyCap   = "notify.daily_cap"
)

// defaultDailyCap is the number of successful deliveries a user receives per
// day before feed-river notifications defer to tomorrow.
const defaultDailyCap = 10

// Channels is the resolved per-category delivery choice.
type Channels struct {
	Push  bool
	Email bool
}

// Prefs is a typed view over the caller's notify.* user_settings rows.
type Prefs struct {
	Digest    Channels
	FeedRiver Channels
	Lifecycle Channels
	QuietFrom string
	QuietTo   string
	Location  *time.Location
	DailyCap  int
}

// For returns the channels enabled for c.
func (p Prefs) For(c Category) Channels {
	switch c {
	case CategoryDigest:
		return p.Digest
	case CategoryFeedRiver:
		return p.FeedRiver
	case CategoryLifecycle:
		return p.Lifecycle
	default:
		return Channels{}
	}
}

// ParsePrefs maps raw user_settings rows onto Prefs. Absent keys take the
// documented default, and unparseable values fall back rather than erroring:
// a bad preference must never block delivery.
func ParsePrefs(rows map[string]string) Prefs {
	p := Prefs{
		Digest:    Channels{Push: true},
		FeedRiver: Channels{},
		Lifecycle: Channels{Push: true},
		Location:  time.UTC,
		DailyCap:  defaultDailyCap,
	}
	if v, ok := rows[KeyDigest]; ok {
		p.Digest = parseChannels(v)
	}
	if v, ok := rows[KeyFeedRiver]; ok {
		p.FeedRiver = parseChannels(v)
	}
	if v, ok := rows[KeyLifecycle]; ok {
		p.Lifecycle = parseChannels(v)
	}
	if v := rows[KeyQuietHours]; v != "" {
		if from, to, ok := parseQuietHours(v); ok {
			p.QuietFrom, p.QuietTo = from, to
		} else {
			slog.Warn("notify: unparseable quiet_hours, ignoring", "value", v)
		}
	}
	if v := rows[KeyTimezone]; v != "" {
		if loc, err := time.LoadLocation(v); err == nil {
			p.Location = loc
		} else {
			slog.Warn("notify: unknown timezone, using UTC", "value", v)
		}
	}
	if v := rows[KeyDailyCap]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.DailyCap = n
		} else {
			slog.Warn("notify: unparseable daily_cap, using default", "value", v)
		}
	}
	return p
}

// parseChannels maps off|push|email|both onto Channels. An unrecognised value
// is treated as off, which fails closed (silence) rather than spamming.
func parseChannels(v string) Channels {
	switch v {
	case "push":
		return Channels{Push: true}
	case "email":
		return Channels{Email: true}
	case "both":
		return Channels{Push: true, Email: true}
	default:
		return Channels{}
	}
}

// parseQuietHours splits "22:00-07:00" and validates both halves are HH:MM.
func parseQuietHours(v string) (string, string, bool) {
	from, to, found := strings.Cut(v, "-")
	if !found || !validHHMM(from) || !validHHMM(to) {
		return "", "", false
	}
	return from, to, true
}

// validHHMM reports whether s is a 24-hour HH:MM clock time.
func validHHMM(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil
}
```

- [ ] **Step 5: Run prefs tests to verify they pass**

```bash
cd apps/api && go test ./internal/notify/ -run TestParsePrefs -v
```

Expected: PASS.

- [ ] **Step 6: Write the failing window test**

Create `apps/api/internal/notify/window_test.go`:

```go
package notify

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
	return loc
}

func TestNextDeliverable(t *testing.T) {
	london := mustLoad(t, "Europe/London")

	tests := []struct {
		name  string
		now   time.Time
		prefs Prefs
		want  time.Time
	}{
		{
			name:  "no quiet hours passes through",
			now:   time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC},
			want:  time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC),
		},
		{
			name:  "outside quiet window passes through",
			now:   time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "inside overnight window before midnight defers to morning",
			now:   time.Date(2026, 7, 27, 23, 30, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 28, 7, 0, 0, 0, time.UTC),
		},
		{
			name:  "inside overnight window after midnight defers to same morning",
			now:   time.Date(2026, 7, 27, 2, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 27, 7, 0, 0, 0, time.UTC),
		},
		{
			name:  "same-day window defers to its end",
			now:   time.Date(2026, 7, 27, 13, 0, 0, 0, time.UTC),
			prefs: Prefs{Location: time.UTC, QuietFrom: "09:00", QuietTo: "17:00"},
			want:  time.Date(2026, 7, 27, 17, 0, 0, 0, time.UTC),
		},
		{
			// Quiet hours are wall-clock in the user's zone. 23:30 UTC in July
			// is 00:30 BST, which is inside 22:00-07:00 London, and the window
			// ends at 07:00 BST = 06:00 UTC the same London day.
			name:  "wall-clock arithmetic happens in the user's zone",
			now:   time.Date(2026, 7, 27, 23, 30, 0, 0, time.UTC),
			prefs: Prefs{Location: london, QuietFrom: "22:00", QuietTo: "07:00"},
			want:  time.Date(2026, 7, 28, 6, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextDeliverable(tt.now, tt.prefs)
			if !got.Equal(tt.want) {
				t.Errorf("NextDeliverable = %s, want %s", got.UTC(), tt.want.UTC())
			}
		})
	}
}
```

- [ ] **Step 7: Run to verify it fails**

```bash
cd apps/api && go test ./internal/notify/ -run TestNextDeliverable -v
```

Expected: FAIL — `undefined: NextDeliverable`.

- [ ] **Step 8: Write `window.go`**

Create `apps/api/internal/notify/window.go`:

```go
package notify

import "time"

// NextDeliverable returns the earliest time at or after now that falls outside
// the user's quiet hours. When quiet hours are unset, or now is already
// outside them, it returns now unchanged.
//
// The arithmetic is deliberately done on wall-clock components in the user's
// own location rather than as a UTC offset: "no pushes after 22:00" means
// 22:00 where the user is, and that must keep meaning that across a DST
// transition, when the UTC offset itself changes.
func NextDeliverable(now time.Time, p Prefs) time.Time {
	if p.QuietFrom == "" || p.QuietTo == "" {
		return now
	}
	loc := p.Location
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)

	from, okFrom := clockMinutes(p.QuietFrom)
	to, okTo := clockMinutes(p.QuietTo)
	if !okFrom || !okTo || from == to {
		return now
	}

	cur := local.Hour()*60 + local.Minute()
	overnight := from > to

	inQuiet := false
	switch {
	case overnight:
		// e.g. 22:00-07:00 wraps midnight: quiet if at/after 22:00 OR before 07:00.
		inQuiet = cur >= from || cur < to
	default:
		inQuiet = cur >= from && cur < to
	}
	if !inQuiet {
		return now
	}

	// The window ends at `to` on the current local day, except when we are in
	// the pre-midnight leg of an overnight window, where it ends tomorrow.
	day := local
	if overnight && cur >= from {
		day = local.AddDate(0, 0, 1)
	}
	end := time.Date(day.Year(), day.Month(), day.Day(), to/60, to%60, 0, 0, loc)
	if !end.After(now) {
		return now
	}
	return end
}

// clockMinutes converts "HH:MM" to minutes past local midnight.
func clockMinutes(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}
```

- [ ] **Step 9: Run window tests to verify they pass**

```bash
cd apps/api && go test ./internal/notify/ -run TestNextDeliverable -v
```

Expected: PASS, all six subtests.

- [ ] **Step 10: Write the failing coalesce test**

Create `apps/api/internal/notify/coalesce_test.go`:

```go
package notify

import (
	"testing"

	"github.com/google/uuid"
)

func TestCoalescePassesThroughNonRiverCategories(t *testing.T) {
	in := []Notification{
		{ID: uuid.New(), Category: CategoryDigest, Title: "Design digest"},
		{ID: uuid.New(), Category: CategoryDigest, Title: "Reading digest"},
	}
	got := Coalesce(CategoryDigest, in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (digest must not collapse)", len(got))
	}
}

func TestCoalesceFeedRiverAcrossFeeds(t *testing.T) {
	in := []Notification{
		{ID: uuid.New(), UserID: uuid.Nil, Category: CategoryFeedRiver, Title: "5 new items", Data: map[string]any{"feed_id": "a", "count": float64(5)}},
		{ID: uuid.New(), Category: CategoryFeedRiver, Title: "4 new items", Data: map[string]any{"feed_id": "b", "count": float64(4)}},
		{ID: uuid.New(), Category: CategoryFeedRiver, Title: "3 new items", Data: map[string]any{"feed_id": "c", "count": float64(3)}},
	}
	got := Coalesce(CategoryFeedRiver, in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "12 new items across 3 feeds" {
		t.Errorf("Title = %q, want %q", got[0].Title, "12 new items across 3 feeds")
	}
	if len(got[0].SourceIDs) != 3 {
		t.Errorf("SourceIDs = %d, want 3", len(got[0].SourceIDs))
	}
	// Mixed feeds must not deep-link to an arbitrary one.
	if _, ok := got[0].Data["feed_id"]; ok {
		t.Errorf("Data carries feed_id %v for a mixed-feed roll-up; want none", got[0].Data["feed_id"])
	}
}

func TestCoalesceFeedRiverSingleFeedKeepsDeepLink(t *testing.T) {
	in := []Notification{
		{ID: uuid.New(), Category: CategoryFeedRiver, Data: map[string]any{"feed_id": "a", "count": float64(2)}},
		{ID: uuid.New(), Category: CategoryFeedRiver, Data: map[string]any{"feed_id": "a", "count": float64(1)}},
	}
	got := Coalesce(CategoryFeedRiver, in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "3 new items in a feed you follow" {
		t.Errorf("Title = %q", got[0].Title)
	}
	if got[0].Data["feed_id"] != "a" {
		t.Errorf("feed_id = %v, want a", got[0].Data["feed_id"])
	}
}

func TestCoalesceEmpty(t *testing.T) {
	if got := Coalesce(CategoryFeedRiver, nil); len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
```

- [ ] **Step 11: Run to verify it fails**

```bash
cd apps/api && go test ./internal/notify/ -run TestCoalesce -v
```

Expected: FAIL — `undefined: Coalesce`.

- [ ] **Step 12: Write `coalesce.go`**

Create `apps/api/internal/notify/coalesce.go`:

```go
package notify

import (
	"fmt"

	"github.com/google/uuid"
)

// Coalesce collapses a user's pending notifications for one category into the
// messages that will actually be delivered.
//
// feed_river is the only category that collapses: producers emit one row per
// feed per hour, and delivering three separate "new items" pushes for one
// polling round is exactly the noise this feature has to avoid. digest and
// lifecycle pass through untouched — each carries distinct, non-summarisable
// information.
func Coalesce(c Category, pending []Notification) []Notification {
	if len(pending) == 0 {
		return nil
	}
	if c != CategoryFeedRiver {
		return pending
	}

	total := 0
	feeds := map[string]struct{}{}
	ids := make([]uuid.UUID, 0, len(pending))
	for _, n := range pending {
		total += countOf(n)
		if fid, ok := n.Data["feed_id"].(string); ok && fid != "" {
			feeds[fid] = struct{}{}
		}
		ids = append(ids, n.ID)
	}

	out := Notification{
		UserID:    pending[0].UserID,
		Category:  CategoryFeedRiver,
		DedupeKey: pending[0].DedupeKey,
		Body:      "",
		Data:      map[string]any{},
		SourceIDs: ids,
	}

	// A roll-up spanning several feeds has no single sensible deep-link
	// target, so it carries no feed_id and the client opens the river root.
	if len(feeds) == 1 {
		for fid := range feeds {
			out.Data["feed_id"] = fid
		}
		out.Title = fmt.Sprintf("%s in a feed you follow", plural(total, "new item", "new items"))
	} else {
		out.Title = fmt.Sprintf("%s across %s", plural(total, "new item", "new items"), plural(len(feeds), "feed", "feeds"))
	}
	return []Notification{out}
}

// countOf reads the producer's item count off the payload, defaulting to 1 so
// a malformed row still contributes to the roll-up rather than vanishing.
func countOf(n Notification) int {
	switch v := n.Data["count"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 1
	}
}

// plural renders "1 new item" / "12 new items".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
```

- [ ] **Step 13: Run all notify tests to verify they pass**

```bash
cd apps/api && go test ./internal/notify/ -v
```

Expected: PASS.

- [ ] **Step 14: Commit**

```bash
git add apps/api/internal/notify/
git commit -m "feat(notify): core types, preferences, quiet hours, and coalescing"
```

---

## Task 3: Senders — noop, fake, email, expo

**Files:**
- Create: `apps/api/internal/notify/noop.go`, `fake.go`, `email.go`, `expo.go`
- Test: `apps/api/internal/notify/expo_test.go`, `email_test.go`

**Interfaces:**
- Consumes: `Sender`, `Notification`, `Target`, `Result` from Task 2.
- Produces:
  - `func NewNoop() Sender`
  - `type Fake struct { ChannelName string; Sent []Notification; Targets []Target; Err error }` with `func NewFake() *Fake` (defaults `ChannelName` to `"fake"`; tests override it to `"expo"`/`"email"`)
  - `func NewEmail(m mailer.Mailer) Sender`
  - `type Expo struct { BaseURL, AccessToken string; HTTP *http.Client }`, `func NewExpo(accessToken string) *Expo`
  - `func (e *Expo) Receipts(ctx context.Context, ticketIDs []string) (map[string]string, error)` — ticket ID → error code, empty string when delivered

- [ ] **Step 1: Write `noop.go` and `fake.go`**

Create `apps/api/internal/notify/noop.go`:

```go
package notify

import (
	"context"
	"log/slog"
)

// noopSender logs and succeeds. It is what runs when NOTIFY_CHANNELS is unset,
// and it is what keeps the app fully functional with no delivery configured:
// producers keep enqueueing, the flush keeps stamping, nothing is sent, and
// nothing errors.
type noopSender struct{}

// NewNoop returns a Sender that delivers nothing and always succeeds.
func NewNoop() Sender { return noopSender{} }

func (noopSender) Name() string { return "noop" }

func (noopSender) Send(_ context.Context, n Notification, _ Target) ([]Result, error) {
	slog.Debug("notify(noop): dropping notification", "category", n.Category, "title", n.Title)
	return nil, nil
}
```

Create `apps/api/internal/notify/fake.go`:

```go
package notify

import "context"

// Fake is a recording Sender for tests, mirroring ai.Fake. It performs no I/O
// and captures what it was asked to deliver so callers can assert fan-out.
type Fake struct {
	ChannelName string
	Sent        []Notification
	Targets     []Target
	Err         error
}

// NewFake returns a recording Sender named "fake".
func NewFake() *Fake { return &Fake{ChannelName: "fake"} }

func (f *Fake) Name() string { return f.ChannelName }

// Send records the call. When Err is set it is returned unchanged, which lets
// tests exercise the router's partial-failure path.
func (f *Fake) Send(_ context.Context, n Notification, t Target) ([]Result, error) {
	f.Sent = append(f.Sent, n)
	f.Targets = append(f.Targets, t)
	if f.Err != nil {
		return nil, f.Err
	}
	return []Result{{Channel: f.ChannelName, OK: true}}, nil
}
```

- [ ] **Step 2: Write the failing email sender test**

Create `apps/api/internal/notify/email_test.go`:

```go
package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/rohithgilla12/openmind/api/internal/mailer"
)

type stubMailer struct {
	got mailer.Message
	err error
}

func (s *stubMailer) Send(_ context.Context, msg mailer.Message) error {
	s.got = msg
	return s.err
}

func TestEmailSenderComposesMessage(t *testing.T) {
	m := &stubMailer{}
	s := NewEmail(m)
	n := Notification{Title: "12 new items across 3 feeds", Body: "Take a look when you have a minute."}

	results, err := s.Send(context.Background(), n, Target{Email: "reader@example.com"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if m.got.To != "reader@example.com" {
		t.Errorf("To = %q", m.got.To)
	}
	if m.got.Subject != n.Title {
		t.Errorf("Subject = %q, want %q", m.got.Subject, n.Title)
	}
	if m.got.BodyText != n.Body {
		t.Errorf("BodyText = %q", m.got.BodyText)
	}
	if len(results) != 1 || !results[0].OK || results[0].Channel != "email" {
		t.Errorf("results = %+v", results)
	}
}

// No address is not an error: the user simply has no e-mail destination, and
// the notification is still considered handled for this channel.
func TestEmailSenderNoAddressIsNoop(t *testing.T) {
	m := &stubMailer{}
	results, err := NewEmail(m).Send(context.Background(), Notification{Title: "x"}, Target{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %+v, want none", results)
	}
	if m.got.To != "" {
		t.Errorf("mailer was called with To = %q", m.got.To)
	}
}

func TestEmailSenderRecordsFailure(t *testing.T) {
	m := &stubMailer{err: errors.New("smtp down")}
	results, err := NewEmail(m).Send(context.Background(), Notification{Title: "x"}, Target{Email: "a@b.c"})
	if err != nil {
		t.Fatalf("Send returned err %v; want the failure reported in Result", err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results = %+v, want one failed result", results)
	}
	if results[0].Err == nil {
		t.Error("Result.Err is nil, want the smtp error")
	}
}
```

- [ ] **Step 3: Run to verify it fails**

```bash
cd apps/api && go test ./internal/notify/ -run TestEmailSender -v
```

Expected: FAIL — `undefined: NewEmail`.

- [ ] **Step 4: Write `email.go`**

Create `apps/api/internal/notify/email.go`:

```go
package notify

import (
	"context"

	"github.com/rohithgilla12/openmind/api/internal/mailer"
)

// emailSender delivers notifications as plain-text e-mail over the existing
// SMTP mailer. It is deliberately plain: the rich HTML digest e-mail is sent
// by the digest job on its own path and is untouched by the substrate.
type emailSender struct {
	m mailer.Mailer
}

// NewEmail returns a Sender that delivers over m.
func NewEmail(m mailer.Mailer) Sender { return &emailSender{m: m} }

func (*emailSender) Name() string { return "email" }

// Send delivers to t.Email. A transport failure is reported as a failed
// Result rather than an error return, so one channel failing never aborts the
// fan-out to the other.
func (s *emailSender) Send(ctx context.Context, n Notification, t Target) ([]Result, error) {
	if t.Email == "" {
		return nil, nil
	}
	err := s.m.Send(ctx, mailer.Message{To: t.Email, Subject: n.Title, BodyText: n.Body})
	res := Result{Channel: "email", OK: err == nil, Err: err}
	return []Result{res}, nil
}
```

- [ ] **Step 5: Run email tests to verify they pass**

```bash
cd apps/api && go test ./internal/notify/ -run TestEmailSender -v
```

Expected: PASS.

- [ ] **Step 6: Write the failing Expo test**

Create `apps/api/internal/notify/expo_test.go`:

```go
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestExpoBatchesTokens proves the sender splits >100 tokens across requests:
// the Expo Push API caps a single call at 100 messages, so a naive one-request
// send would silently drop targets.
func TestExpoBatchesTokens(t *testing.T) {
	var batchSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msgs []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		batchSizes = append(batchSizes, len(msgs))
		data := make([]map[string]any, len(msgs))
		for i := range msgs {
			data[i] = map[string]any{"status": "ok", "id": fmt.Sprintf("ticket-%d", i)}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	e := NewExpo("")
	e.BaseURL = srv.URL

	devices := make([]Device, 250)
	for i := range devices {
		devices[i] = Device{Token: fmt.Sprintf("ExponentPushToken[%d]", i), Platform: "ios"}
	}

	results, err := e.Send(context.Background(), Notification{Title: "hi"}, Target{Devices: devices})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 250 {
		t.Errorf("results = %d, want 250", len(results))
	}
	want := []int{100, 100, 50}
	if len(batchSizes) != len(want) {
		t.Fatalf("batches = %v, want %v", batchSizes, want)
	}
	for i, n := range want {
		if batchSizes[i] != n {
			t.Errorf("batch %d = %d, want %d", i, batchSizes[i], n)
		}
	}
}

// A per-message error in the ticket response marks only that token failed.
func TestExpoPerTicketErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"status": "ok", "id": "ticket-a"},
			{"status": "error", "message": "bad token", "details": map[string]any{"error": "DeviceNotRegistered"}},
		}})
	}))
	defer srv.Close()

	e := NewExpo("")
	e.BaseURL = srv.URL
	results, err := e.Send(context.Background(), Notification{Title: "hi"}, Target{Devices: []Device{
		{Token: "ExponentPushToken[a]"}, {Token: "ExponentPushToken[b]"},
	}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if !results[0].OK || results[0].TicketID != "ticket-a" {
		t.Errorf("results[0] = %+v", results[0])
	}
	if results[1].OK || results[1].Err == nil {
		t.Errorf("results[1] = %+v, want failure", results[1])
	}
}

// Receipts translate a ticket ID to its terminal error code, which is the only
// place DeviceNotRegistered actually surfaces.
func TestExpoReceipts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"ticket-a": map[string]any{"status": "ok"},
			"ticket-b": map[string]any{"status": "error", "details": map[string]any{"error": "DeviceNotRegistered"}},
		}})
	}))
	defer srv.Close()

	e := NewExpo("")
	e.BaseURL = srv.URL
	got, err := e.Receipts(context.Background(), []string{"ticket-a", "ticket-b"})
	if err != nil {
		t.Fatalf("Receipts: %v", err)
	}
	if got["ticket-a"] != "" {
		t.Errorf("ticket-a = %q, want empty", got["ticket-a"])
	}
	if got["ticket-b"] != "DeviceNotRegistered" {
		t.Errorf("ticket-b = %q", got["ticket-b"])
	}
}
```

- [ ] **Step 7: Run to verify it fails**

```bash
cd apps/api && go test ./internal/notify/ -run TestExpo -v
```

Expected: FAIL — `undefined: NewExpo`.

- [ ] **Step 8: Write `expo.go`**

Create `apps/api/internal/notify/expo.go`:

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// expoPushURL and expoReceiptURL are Expo's hosted push endpoints.
const (
	expoPushURL    = "https://exp.host/--/api/v2/push/send"
	expoReceiptURL = "https://exp.host/--/api/v2/push/getReceipts"
)

// expoBatchSize is the maximum number of messages Expo accepts per request.
const expoBatchSize = 100

// expoTimeout bounds a single push or receipts call.
const expoTimeout = 20 * time.Second

// Expo delivers over the Expo Push service. BaseURL is overridable so tests
// can point it at an httptest server; when empty the hosted endpoints are used.
//
// Delivery is two-phase: a send returns *tickets*, and terminal failures such
// as DeviceNotRegistered only appear later in the *receipts*. Callers must
// poll Receipts to learn which tokens are dead.
type Expo struct {
	BaseURL     string
	AccessToken string
	HTTP        *http.Client
}

// NewExpo returns an Expo sender. accessToken may be empty; Expo accepts
// unauthenticated pushes, and the token only adds send-security.
func NewExpo(accessToken string) *Expo {
	return &Expo{AccessToken: accessToken, HTTP: &http.Client{Timeout: expoTimeout}}
}

func (*Expo) Name() string { return "expo" }

// expoMessage is one push in a batch.
type expoMessage struct {
	To    string         `json:"to"`
	Title string         `json:"title"`
	Body  string         `json:"body,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

// expoTicket is one entry in the send response.
type expoTicket struct {
	Status  string `json:"status"`
	ID      string `json:"id"`
	Message string `json:"message"`
	Details struct {
		Error string `json:"error"`
	} `json:"details"`
}

// Send pushes n to every device in t, in batches of expoBatchSize. It returns
// one Result per device, in the same order as t.Devices. A whole-batch
// transport failure marks that batch's devices failed but does not abort the
// remaining batches — partial delivery beats none.
func (e *Expo) Send(ctx context.Context, n Notification, t Target) ([]Result, error) {
	if len(t.Devices) == 0 {
		return nil, nil
	}
	results := make([]Result, 0, len(t.Devices))
	for start := 0; start < len(t.Devices); start += expoBatchSize {
		end := min(start+expoBatchSize, len(t.Devices))
		batch := t.Devices[start:end]

		msgs := make([]expoMessage, len(batch))
		for i, d := range batch {
			msgs[i] = expoMessage{To: d.Token, Title: n.Title, Body: n.Body, Data: n.Data}
		}

		tickets, err := e.postBatch(ctx, msgs)
		if err != nil {
			for _, d := range batch {
				results = append(results, Result{Channel: "expo", Token: d.Token, OK: false, Err: err})
			}
			continue
		}
		for i, d := range batch {
			if i >= len(tickets) {
				results = append(results, Result{Channel: "expo", Token: d.Token, OK: false, Err: errors.New("expo: missing ticket for token")})
				continue
			}
			tk := tickets[i]
			if tk.Status != "ok" {
				msg := tk.Message
				if tk.Details.Error != "" {
					msg = tk.Details.Error
				}
				results = append(results, Result{Channel: "expo", Token: d.Token, OK: false, Err: errors.New("expo: " + msg)})
				continue
			}
			results = append(results, Result{Channel: "expo", Token: d.Token, TicketID: tk.ID, OK: true})
		}
	}
	return results, nil
}

// postBatch sends one batch and returns its tickets.
func (e *Expo) postBatch(ctx context.Context, msgs []expoMessage) ([]expoTicket, error) {
	body, err := json.Marshal(msgs)
	if err != nil {
		return nil, fmt.Errorf("marshalling push batch: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url(expoPushURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.AccessToken)
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting push batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expo push: unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Data []expoTicket `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding push response: %w", err)
	}
	return out.Data, nil
}

// Receipts maps each ticket ID to its terminal error code, or to the empty
// string when the push was delivered. Unknown ticket IDs are simply absent
// from the result.
func (e *Expo) Receipts(ctx context.Context, ticketIDs []string) (map[string]string, error) {
	if len(ticketIDs) == 0 {
		return map[string]string{}, nil
	}
	body, err := json.Marshal(map[string]any{"ids": ticketIDs})
	if err != nil {
		return nil, fmt.Errorf("marshalling receipts request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url(expoReceiptURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building receipts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.AccessToken)
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching receipts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expo receipts: unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Data map[string]struct {
			Status  string `json:"status"`
			Details struct {
				Error string `json:"error"`
			} `json:"details"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding receipts: %w", err)
	}
	codes := make(map[string]string, len(out.Data))
	for id, r := range out.Data {
		if r.Status == "ok" {
			codes[id] = ""
			continue
		}
		codes[id] = r.Details.Error
	}
	return codes, nil
}

// url returns the override BaseURL when set, otherwise the hosted endpoint.
// Tests point BaseURL at an httptest server, which serves both paths.
func (e *Expo) url(hosted string) string {
	if e.BaseURL != "" {
		return e.BaseURL
	}
	return hosted
}

func (e *Expo) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return &http.Client{Timeout: expoTimeout}
}
```

- [ ] **Step 9: Run all notify tests to verify they pass**

```bash
cd apps/api && go test ./internal/notify/ -v
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add apps/api/internal/notify/
git commit -m "feat(notify): noop, fake, email, and Expo push senders"
```

---

## Task 4: Router

**Files:**
- Create: `apps/api/internal/notify/router.go`
- Test: `apps/api/internal/notify/router_test.go`

**Interfaces:**
- Consumes: `Sender`, `Prefs`, `Channels`, `Notification`, `Target`, `Result` from Tasks 2–3.
- Produces:
  - `type Router struct { Push Sender; Email Sender }`
  - `func NewRouter(push, email Sender) *Router`
  - `func (r *Router) Deliver(ctx context.Context, n Notification, ch Channels, t Target) []Result`
  - `func (r *Router) Enabled() bool`

- [ ] **Step 1: Write the failing router test**

Create `apps/api/internal/notify/router_test.go`:

```go
package notify

import (
	"context"
	"errors"
	"testing"
)

func TestRouterFansOutToBothChannels(t *testing.T) {
	push, email := NewFake(), NewFake()
	push.ChannelName, email.ChannelName = "expo", "email"
	r := NewRouter(push, email)

	results := r.Deliver(context.Background(), Notification{Title: "hi"},
		Channels{Push: true, Email: true},
		Target{Devices: []Device{{Token: "t"}}, Email: "a@b.c"})

	if len(push.Sent) != 1 {
		t.Errorf("push calls = %d, want 1", len(push.Sent))
	}
	if len(email.Sent) != 1 {
		t.Errorf("email calls = %d, want 1", len(email.Sent))
	}
	if len(results) != 2 {
		t.Errorf("results = %d, want 2", len(results))
	}
}

func TestRouterRespectsDisabledChannel(t *testing.T) {
	push, email := NewFake(), NewFake()
	r := NewRouter(push, email)

	r.Deliver(context.Background(), Notification{Title: "hi"},
		Channels{Push: true}, Target{Devices: []Device{{Token: "t"}}, Email: "a@b.c"})

	if len(email.Sent) != 0 {
		t.Errorf("email was called %d times despite being disabled", len(email.Sent))
	}
	if len(push.Sent) != 1 {
		t.Errorf("push calls = %d, want 1", len(push.Sent))
	}
}

// One channel erroring must not stop the other, and must surface as a failed
// Result so the ledger records why.
func TestRouterPartialFailure(t *testing.T) {
	push, email := NewFake(), NewFake()
	push.ChannelName, email.ChannelName = "expo", "email"
	push.Err = errors.New("expo down")
	r := NewRouter(push, email)

	results := r.Deliver(context.Background(), Notification{Title: "hi"},
		Channels{Push: true, Email: true},
		Target{Devices: []Device{{Token: "t"}}, Email: "a@b.c"})

	if len(email.Sent) != 1 {
		t.Fatalf("email calls = %d, want 1 despite push failing", len(email.Sent))
	}
	var failed, ok int
	for _, res := range results {
		if res.OK {
			ok++
		} else {
			failed++
		}
	}
	if failed != 1 || ok != 1 {
		t.Errorf("results = %+v, want one ok and one failed", results)
	}
}

func TestRouterNoChannelsEnabled(t *testing.T) {
	push, email := NewFake(), NewFake()
	r := NewRouter(push, email)
	results := r.Deliver(context.Background(), Notification{Title: "hi"}, Channels{}, Target{Email: "a@b.c"})
	if len(results) != 0 {
		t.Errorf("results = %+v, want none", results)
	}
	if len(push.Sent)+len(email.Sent) != 0 {
		t.Error("a sender was called with no channels enabled")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd apps/api && go test ./internal/notify/ -run TestRouter -v
```

Expected: FAIL — `undefined: NewRouter`.

- [ ] **Step 3: Write `router.go`**

Create `apps/api/internal/notify/router.go`:

```go
package notify

import "context"

// Router fans one notification out across the channels a user has enabled.
// It owns no state beyond its senders; target resolution and ledger writes
// belong to the caller (the flush job), which keeps senders and the router
// free of store access.
type Router struct {
	Push  Sender
	Email Sender
}

// NewRouter returns a Router over the given senders. Either may be a noop.
func NewRouter(push, email Sender) *Router {
	return &Router{Push: push, Email: email}
}

// Enabled reports whether any real (non-noop) channel is configured. The flush
// job uses this only for logging; delivery is correct either way.
func (r *Router) Enabled() bool {
	return (r.Push != nil && r.Push.Name() != "noop") || (r.Email != nil && r.Email.Name() != "noop")
}

// Deliver sends n over every channel enabled in ch and returns one Result per
// destination attempted. A sender returning an error is converted into a
// single failed Result for its channel rather than aborting the fan-out: one
// channel being down must never suppress the other.
func (r *Router) Deliver(ctx context.Context, n Notification, ch Channels, t Target) []Result {
	var out []Result
	if ch.Push && r.Push != nil {
		out = append(out, collect(ctx, r.Push, n, t)...)
	}
	if ch.Email && r.Email != nil {
		out = append(out, collect(ctx, r.Email, n, t)...)
	}
	return out
}

// collect runs one sender and normalises its outcome into Results.
func collect(ctx context.Context, s Sender, n Notification, t Target) []Result {
	res, err := s.Send(ctx, n, t)
	if err != nil {
		return []Result{{Channel: s.Name(), OK: false, Err: err}}
	}
	return res
}
```

- [ ] **Step 4: Run router tests to verify they pass**

```bash
cd apps/api && go test ./internal/notify/ -run TestRouter -v
```

Expected: PASS.

- [ ] **Step 5: Run the whole package and vet**

```bash
cd apps/api && go test ./internal/notify/ && go vet ./internal/notify/
```

Expected: PASS, no vet findings.

- [ ] **Step 6: Commit**

```bash
git add apps/api/internal/notify/router.go apps/api/internal/notify/router_test.go
git commit -m "feat(notify): per-user channel fan-out router"
```

---

## Task 5: Scan and flush jobs

**Files:**
- Create: `apps/api/internal/jobs/notifications.go`
- Test: `apps/api/internal/jobs/notifications_test.go`
- Modify: `apps/api/internal/jobs/enrich.go` (register workers, `notifications` queue, periodic jobs)

**Interfaces:**
- Consumes: `db.*` queries from Task 1; `notify.Router`, `notify.ParsePrefs`, `notify.Coalesce`, `notify.NextDeliverable` from Tasks 2–4.
- Produces:
  - `type ScanNotificationsArgs struct{}` with `Kind() string` → `"scan_notifications"`
  - `type FlushNotificationsArgs struct{ UserID uuid.UUID }` with `Kind() string` → `"flush_notifications"`
  - `type NotifyDeps struct { Router *notify.Router; Configured bool }`
  - `type ScanNotificationsWorker struct { river.WorkerDefaults[ScanNotificationsArgs]; Store *store.Store; River *river.Client[pgx.Tx] }`
  - `type FlushNotificationsWorker struct { river.WorkerDefaults[FlushNotificationsArgs]; Store *store.Store; Deps NotifyDeps }`
  - `const notifyQueue = "notifications"`

- [ ] **Step 1: Write `notifications.go` (scan + flush)**

Create `apps/api/internal/jobs/notifications.go`:

```go
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/notify"
	"github.com/rohithgilla12/openmind/api/internal/store"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// notifyQueue keeps notification work off the default queue, so a burst of
// enrichment jobs cannot delay delivery.
const notifyQueue = "notifications"

// notifyScanInterval is how often due users are looked for. One minute keeps
// "immediate" categories feeling immediate while staying a trivially cheap
// indexed query.
const notifyScanInterval = time.Minute

// receiptInterval is how often Expo receipts are reconciled. Expo needs a few
// minutes before receipts are meaningful, so this is deliberately slow.
const receiptInterval = 15 * time.Minute

// pruneInterval is how often old notification rows are deleted.
const pruneInterval = 24 * time.Hour

// notifyMaxAttempts caps River retries for a flush job. It matches the
// attempts < 3 predicate in the due-row queries so a row and its job give up
// together.
const notifyMaxAttempts = 3

// NotifyDeps carries the delivery router into the workers. Configured is false
// when NOTIFY_CHANNELS is unset; the flush still runs and still stamps rows
// (via the noop router), which keeps the outbox from growing unbounded on an
// install that never delivers anything.
type NotifyDeps struct {
	Router     *notify.Router
	Configured bool
}

// ScanNotificationsArgs is the periodic fan-out job. It carries no state.
type ScanNotificationsArgs struct{}

// Kind identifies the job type in River.
func (ScanNotificationsArgs) Kind() string { return "scan_notifications" }

// InsertOpts pins the job to the notifications queue.
func (ScanNotificationsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// ScanNotificationsWorker finds users with due outbox rows and enqueues one
// flush per user. Per-user jobs (rather than one global loop) give per-user
// retry isolation and stop one slow Expo call head-of-line blocking everyone.
type ScanNotificationsWorker struct {
	river.WorkerDefaults[ScanNotificationsArgs]
	Store *store.Store
	River *river.Client[pgx.Tx]
}

// Work enqueues a flush per due user. A single user's enqueue failing is
// logged and skipped rather than failing the whole scan: the next tick retries
// them, and one bad user must not stall everyone else's notifications.
func (w *ScanNotificationsWorker) Work(ctx context.Context, _ *river.Job[ScanNotificationsArgs]) error {
	users, err := w.Store.Queries.ListUsersWithDueNotifications(ctx)
	if err != nil {
		return fmt.Errorf("listing users with due notifications: %w", err)
	}
	for _, uid := range users {
		if _, err := w.River.Insert(ctx, FlushNotificationsArgs{UserID: uid}, &river.InsertOpts{
			Queue:       notifyQueue,
			MaxAttempts: notifyMaxAttempts,
		}); err != nil {
			slog.Error("scan_notifications: enqueueing flush", "user_id", uid, "err", err)
		}
	}
	return nil
}

// FlushNotificationsArgs delivers one user's due notifications.
type FlushNotificationsArgs struct {
	UserID uuid.UUID `json:"user_id"`
}

// Kind identifies the job type in River.
func (FlushNotificationsArgs) Kind() string { return "flush_notifications" }

// InsertOpts pins the job to the notifications queue.
func (FlushNotificationsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// FlushNotificationsWorker applies preferences, coalescing, quiet hours, and
// the daily cap to one user's due rows, then delivers what survives.
type FlushNotificationsWorker struct {
	river.WorkerDefaults[FlushNotificationsArgs]
	Store *store.Store
	Deps  NotifyDeps
}

// Work is the delivery pipeline for one user.
//
// Delivery is at-least-once. The send is an HTTP call and cannot sit inside
// the transaction that stamps sent_at, so rows are claimed (attempts+1), sent,
// then stamped. A crash between send and stamp can re-send once; attempts
// reaching notifyMaxAttempts gives up with last_error set. Repeating a
// notification is a smaller harm than losing one.
func (w *FlushNotificationsWorker) Work(ctx context.Context, job *river.Job[FlushNotificationsArgs]) error {
	uid := job.Args.UserID

	prefs, err := w.loadPrefs(ctx, uid)
	if err != nil {
		return err
	}

	due, err := w.Store.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		return fmt.Errorf("listing due notifications: %w", err)
	}
	if len(due) == 0 {
		return nil
	}

	// Quiet hours defer rather than drop: bump every due row past the window
	// and let a later scan pick them up.
	now := time.Now()
	if next := notify.NextDeliverable(now, prefs); next.After(now) {
		ids := make([]uuid.UUID, len(due))
		for i, row := range due {
			ids[i] = row.ID
		}
		if err := w.Store.Queries.DeferNotifications(ctx, db.DeferNotificationsParams{
			UserID: uid, DeliverAfter: pgTimestamp(next), Ids: ids,
		}); err != nil {
			return fmt.Errorf("deferring for quiet hours: %w", err)
		}
		return nil
	}

	sentToday, err := w.Store.Queries.CountDeliveriesSince(ctx, db.CountDeliveriesSinceParams{
		UserID: uid, SentAt: pgTimestamp(startOfDay(now, prefs.Location)),
	})
	if err != nil {
		return fmt.Errorf("counting today's deliveries: %w", err)
	}

	target, err := w.resolveTarget(ctx, uid)
	if err != nil {
		return err
	}

	budget := prefs.DailyCap - int(sentToday)
	for _, cat := range []notify.Category{notify.CategoryLifecycle, notify.CategoryDigest, notify.CategoryFeedRiver} {
		pending := rowsFor(uid, cat, due)
		if len(pending) == 0 {
			continue
		}
		ch := prefs.For(cat)
		if !ch.Push && !ch.Email {
			// The user has this category switched off. Stamp the rows so they
			// do not accumulate forever in the pending index.
			if err := w.stamp(ctx, uid, idsOf(pending)); err != nil {
				return err
			}
			continue
		}

		// lifecycle bypasses the cap: a "we gave up on your save" swallowed
		// because feed river spent the budget is the one failure mode that
		// makes the whole feature untrustworthy.
		if cat != notify.CategoryLifecycle && budget <= 0 {
			if err := w.Store.Queries.DeferNotifications(ctx, db.DeferNotificationsParams{
				UserID: uid, DeliverAfter: pgTimestamp(startOfDay(now, prefs.Location).AddDate(0, 0, 1)), Ids: idsOf(pending),
			}); err != nil {
				return fmt.Errorf("deferring over-cap notifications: %w", err)
			}
			continue
		}

		for _, n := range notify.Coalesce(cat, pending) {
			if err := w.deliverOne(ctx, uid, n, ch, target); err != nil {
				return err
			}
			if cat != notify.CategoryLifecycle {
				budget--
			}
		}
	}
	return nil
}

// deliverOne claims, delivers, records the ledger, and stamps one message.
func (w *FlushNotificationsWorker) deliverOne(ctx context.Context, uid uuid.UUID, n notify.Notification, ch notify.Channels, target notify.Target) error {
	if err := w.Store.Queries.ClaimNotifications(ctx, db.ClaimNotificationsParams{UserID: uid, Ids: n.SourceIDs}); err != nil {
		return fmt.Errorf("claiming notifications: %w", err)
	}

	results := w.Deps.Router.Deliver(ctx, n, ch, target)

	anyFailed := false
	for _, res := range results {
		errText := ""
		if res.Err != nil {
			errText = res.Err.Error()
			anyFailed = true
		}
		// The ledger is per source row so the cap count and the audit trail
		// both stay accurate for a coalesced message.
		for _, srcID := range n.SourceIDs {
			if err := w.Store.Queries.RecordDelivery(ctx, db.RecordDeliveryParams{
				UserID: uid, NotificationID: srcID, Channel: res.Channel,
				Token: res.Token, TicketID: res.TicketID, Ok: res.OK, Error: errText,
			}); err != nil {
				return fmt.Errorf("recording delivery: %w", err)
			}
		}
	}

	if anyFailed {
		if err := w.Store.Queries.MarkNotificationsFailed(ctx, db.MarkNotificationsFailedParams{
			UserID: uid, LastError: "one or more channels failed", Ids: n.SourceIDs,
		}); err != nil {
			return fmt.Errorf("marking failed: %w", err)
		}
	}
	return w.stamp(ctx, uid, n.SourceIDs)
}

// stamp marks rows delivered.
func (w *FlushNotificationsWorker) stamp(ctx context.Context, uid uuid.UUID, ids []uuid.UUID) error {
	if err := w.Store.Queries.MarkNotificationsSent(ctx, db.MarkNotificationsSentParams{UserID: uid, Ids: ids}); err != nil {
		return fmt.Errorf("marking notifications sent: %w", err)
	}
	return nil
}

// loadPrefs reads the caller's notify.* settings.
func (w *FlushNotificationsWorker) loadPrefs(ctx context.Context, uid uuid.UUID) (notify.Prefs, error) {
	rows, err := w.Store.Queries.ListUserSettings(ctx, uid)
	if err != nil {
		return notify.Prefs{}, fmt.Errorf("listing user settings: %w", err)
	}
	kv := make(map[string]string, len(rows))
	for _, row := range rows {
		kv[row.Key] = row.Value
	}
	return notify.ParsePrefs(kv), nil
}

// resolveTarget collects the user's live devices and e-mail address.
func (w *FlushNotificationsWorker) resolveTarget(ctx context.Context, uid uuid.UUID) (notify.Target, error) {
	devices, err := w.Store.Queries.ListPushDevices(ctx, uid)
	if err != nil {
		return notify.Target{}, fmt.Errorf("listing push devices: %w", err)
	}
	t := notify.Target{Devices: make([]notify.Device, len(devices))}
	for i, d := range devices {
		t.Devices[i] = notify.Device{Token: d.Token, Platform: d.Platform}
	}
	// A missing or empty account e-mail is not an error: the e-mail channel
	// simply has no target, and push still goes.
	if email, err := w.Store.Queries.GetUserEmail(ctx, uid); err == nil {
		t.Email = email
	}
	return t, nil
}

// rowsFor selects the due rows belonging to one category and maps them onto
// notify.Notification values.
func rowsFor(uid uuid.UUID, cat notify.Category, due []db.ListDueNotificationsRow) []notify.Notification {
	var out []notify.Notification
	for _, row := range due {
		if notify.Category(row.Category) != cat {
			continue
		}
		data := map[string]any{}
		if len(row.Data) > 0 {
			if err := json.Unmarshal(row.Data, &data); err != nil {
				slog.Warn("flush_notifications: unreadable data payload", "notification_id", row.ID, "err", err)
				data = map[string]any{}
			}
		}
		out = append(out, notify.Notification{
			ID: row.ID, UserID: uid, Category: cat, DedupeKey: row.DedupeKey,
			Title: row.Title, Body: row.Body, Data: data, SourceIDs: []uuid.UUID{row.ID},
		})
	}
	return out
}

// idsOf flattens the source row IDs of a set of notifications.
func idsOf(ns []notify.Notification) []uuid.UUID {
	var out []uuid.UUID
	for _, n := range ns {
		out = append(out, n.SourceIDs...)
	}
	return out
}

// startOfDay returns local midnight for t in loc — the boundary the daily cap
// counts from, so the cap resets when the user's day does, not at UTC midnight.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}
```

- [ ] **Step 2: Add the `pgTimestamp` helper if it does not already exist**

Check first:

```bash
cd apps/api && grep -rn "func pgTimestamp" internal/jobs/
```

If absent, append to `apps/api/internal/jobs/notifications.go`:

```go
// pgTimestamp wraps a time for pgx's timestamptz parameters.
func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
```

and add `"github.com/jackc/pgx/v5/pgtype"` to the import block.

- [ ] **Step 3: Register the workers, queue, and periodic jobs**

In `apps/api/internal/jobs/enrich.go`, inside `NewRiverClient`'s `if workersOn {` block:

Change the signature to accept the deps — replace:

```go
func NewRiverClient(pool *pgxpool.Pool, p *enrich.Pipeline, feedService FeedRefresher, kindleDeps KindleDeps, geocoder geo.Geocoder, reelMode reelmedia.Mode, reelExtractor *reelmedia.Extractor, workersOn bool) (*river.Client[pgx.Tx], error) {
```

with:

```go
func NewRiverClient(pool *pgxpool.Pool, p *enrich.Pipeline, feedService FeedRefresher, kindleDeps KindleDeps, notifyDeps NotifyDeps, geocoder geo.Geocoder, reelMode reelmedia.Mode, reelExtractor *reelmedia.Extractor, workersOn bool) (*river.Client[pgx.Tx], error) {
```

Declare the scan worker alongside the existing ones:

```go
	var notifyScanWorker *ScanNotificationsWorker
```

Register inside `if workersOn {`, after the existing `river.AddWorker(workers, scanWorker)` line:

```go
		notifyScanWorker = &ScanNotificationsWorker{Store: p.Store}
		river.AddWorker(workers, notifyScanWorker)
		river.AddWorker(workers, &FlushNotificationsWorker{Store: p.Store, Deps: notifyDeps})
		river.AddWorker(workers, &CheckReceiptsWorker{Store: p.Store, Deps: notifyDeps})
		river.AddWorker(workers, &PruneNotificationsWorker{Store: p.Store})
```

Extend the queue map — replace:

```go
		cfg.Queues = map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 5}}
```

with:

```go
		cfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 5},
			notifyQueue:        {MaxWorkers: 3},
		}
```

Append three periodic jobs to `cfg.PeriodicJobs`:

```go
			river.NewPeriodicJob(
				river.PeriodicInterval(notifyScanInterval),
				func() (river.JobArgs, *river.InsertOpts) { return ScanNotificationsArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true},
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(receiptInterval),
				func() (river.JobArgs, *river.InsertOpts) { return CheckReceiptsArgs{}, nil },
				nil,
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(pruneInterval),
				func() (river.JobArgs, *river.InsertOpts) { return PruneNotificationsArgs{}, nil },
				nil,
			),
```

And after the client is built, alongside the existing `scanWorker.River = client`:

```go
		notifyScanWorker.River = client
```

Note: `CheckReceiptsWorker`, `PruneNotificationsWorker`, `CheckReceiptsArgs`, and `PruneNotificationsArgs` are written in Task 6. Until then this will not compile — complete Task 6 before running the build. If you prefer a green build at each step, add the four Task 6 symbols now as empty stubs and fill them in Task 6.

- [ ] **Step 4: Write the flush job tests**

Create `apps/api/internal/jobs/notifications_test.go`:

```go
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/notify"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// flushFixture builds a worker over a real store with one user, a registered
// device, and recording senders.
func flushFixture(t *testing.T) (*FlushNotificationsWorker, uuid.UUID, *notify.Fake, *notify.Fake) {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	if err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{
		UserID: uid, Token: "ExponentPushToken[x]", Platform: "ios",
	}); err != nil {
		t.Fatalf("upsert device: %v", err)
	}
	push, email := notify.NewFake(), notify.NewFake()
	push.ChannelName, email.ChannelName = "expo", "email"
	w := &FlushNotificationsWorker{
		Store: s,
		Deps:  NotifyDeps{Router: notify.NewRouter(push, email), Configured: true},
	}
	return w, uid, push, email
}

func enqueue(t *testing.T, w *FlushNotificationsWorker, uid uuid.UUID, cat, key, title string, data string) {
	t.Helper()
	if err := w.Store.Queries.EnqueueNotification(context.Background(), db.EnqueueNotificationParams{
		UserID: uid, Category: cat, DedupeKey: key, Title: title, Body: "", Data: []byte(data),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func TestFlushDeliversAndStamps(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	enqueue(t, w, uid, "digest", "digest:lens-a:2026-07-27", "Design digest — 7 new saves", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Fatalf("push sends = %d, want 1", len(push.Sent))
	}
	due, err := w.Store.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("pending after flush = %d, want 0", len(due))
	}
}

// Running the flush twice must not deliver twice — CLAUDE.md's idempotency rule.
func TestFlushIsIdempotent(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	enqueue(t, w, uid, "digest", "digest:lens-a:2026-07-27", "Design digest", `{}`)

	job := &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("first Work: %v", err)
	}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("second Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Errorf("push sends = %d after two flushes, want 1", len(push.Sent))
	}
}

// feed_river defaults to off: rows must be stamped, not delivered, so they do
// not accumulate in the pending index forever.
func TestFlushStampsDisabledCategoryWithoutDelivering(t *testing.T) {
	w, uid, push, email := flushFixture(t)
	ctx := context.Background()
	enqueue(t, w, uid, "feed_river", "feed_river:f1:2026-07-27T09", "5 new items", `{"feed_id":"f1","count":5}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent)+len(email.Sent) != 0 {
		t.Errorf("delivered %d messages for a disabled category", len(push.Sent)+len(email.Sent))
	}
	due, _ := w.Store.Queries.ListDueNotifications(ctx, uid)
	if len(due) != 0 {
		t.Errorf("pending = %d, want 0 (disabled rows must be stamped)", len(due))
	}
}

func TestFlushCoalescesFeedRiver(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyFeedRiver, Value: "push",
	}); err != nil {
		t.Fatalf("set pref: %v", err)
	}
	enqueue(t, w, uid, "feed_river", "feed_river:f1:2026-07-27T09", "5 new items", `{"feed_id":"f1","count":5}`)
	enqueue(t, w, uid, "feed_river", "feed_river:f2:2026-07-27T09", "4 new items", `{"feed_id":"f2","count":4}`)
	enqueue(t, w, uid, "feed_river", "feed_river:f3:2026-07-27T09", "3 new items", `{"feed_id":"f3","count":3}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Fatalf("push sends = %d, want 1 coalesced message", len(push.Sent))
	}
	if push.Sent[0].Title != "12 new items across 3 feeds" {
		t.Errorf("Title = %q", push.Sent[0].Title)
	}
}

// Quiet hours defer rather than drop.
func TestFlushDefersDuringQuietHours(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	// A window covering the whole day guarantees "now" is inside it whenever
	// this test runs, without the test having to control the clock.
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyQuietHours, Value: "00:00-23:59",
	}); err != nil {
		t.Fatalf("set quiet hours: %v", err)
	}
	enqueue(t, w, uid, "digest", "digest:lens-a:2026-07-27", "Design digest", `{}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 0 {
		t.Errorf("delivered %d messages during quiet hours", len(push.Sent))
	}
	due, _ := w.Store.Queries.ListDueNotifications(ctx, uid)
	if len(due) != 0 {
		t.Errorf("rows still due = %d; deferral should have pushed deliver_after out", len(due))
	}
}

// lifecycle must ignore an exhausted daily cap.
func TestFlushLifecycleBypassesCap(t *testing.T) {
	w, uid, push, _ := flushFixture(t)
	ctx := context.Background()
	if err := w.Store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
		UserID: uid, Key: notify.KeyDailyCap, Value: "0",
	}); err != nil {
		t.Fatalf("set cap: %v", err)
	}
	enqueue(t, w, uid, "lifecycle", "lifecycle:item-1", "We couldn't process a save", `{"item_id":"item-1"}`)

	if err := w.Work(ctx, &river.Job[FlushNotificationsArgs]{Args: FlushNotificationsArgs{UserID: uid}}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(push.Sent) != 1 {
		t.Errorf("push sends = %d, want 1 (lifecycle bypasses the cap)", len(push.Sent))
	}
	_ = time.Now
}
```

- [ ] **Step 5: Add the `testStore` helper for the jobs package if absent**

```bash
cd apps/api && grep -rn "func testStore" internal/jobs/
```

If absent, create `apps/api/internal/jobs/store_test_helper_test.go`:

```go
package jobs

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rohithgilla12/openmind/api/internal/store"
)

// testStore connects to the test database, migrates, and truncates the tables
// these tests touch.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://openmind:openmind@localhost:5433/openmind_test"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE notifications, notification_deliveries, push_devices, user_settings CASCADE`); err != nil {
		t.Fatalf("truncating: %v", err)
	}
	return store.New(pool)
}
```

- [ ] **Step 6: Run the flush tests**

Complete Task 6 first if the build fails on the missing receipt/prune symbols.

```bash
cd apps/api && go test ./internal/jobs/ -run TestFlush -v
```

Expected: PASS, all six tests.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal/jobs/notifications.go \
        apps/api/internal/jobs/notifications_test.go \
        apps/api/internal/jobs/store_test_helper_test.go \
        apps/api/internal/jobs/enrich.go
git commit -m "feat(jobs): notification scan and flush workers on a dedicated queue"
```

---

## Task 6: Receipt reconciliation and pruning

**Files:**
- Modify: `apps/api/internal/jobs/notifications.go`
- Test: `apps/api/internal/jobs/notifications_receipts_test.go`

**Interfaces:**
- Consumes: `NotifyDeps`, `notifyQueue` from Task 5; `notify.Expo.Receipts` from Task 3; `db.ListRecentTickets`, `db.MarkPushDeviceFailed`, `db.PruneNotifications` from Task 1.
- Produces:
  - `type CheckReceiptsArgs struct{}` / `type CheckReceiptsWorker struct{...}`
  - `type PruneNotificationsArgs struct{}` / `type PruneNotificationsWorker struct{...}`
  - `type ReceiptChecker interface { Receipts(ctx context.Context, ticketIDs []string) (map[string]string, error) }`

- [ ] **Step 1: Add `ReceiptChecker` to `NotifyDeps`**

In `apps/api/internal/jobs/notifications.go`, extend the struct:

```go
// ReceiptChecker resolves Expo ticket IDs to their terminal error codes. It is
// an interface rather than *notify.Expo so tests can substitute a stub.
type ReceiptChecker interface {
	Receipts(ctx context.Context, ticketIDs []string) (map[string]string, error)
}

// NotifyDeps carries the delivery router into the workers. Configured is false
// when NOTIFY_CHANNELS is unset; the flush still runs and still stamps rows
// (via the noop router), which keeps the outbox from growing unbounded on an
// install that never delivers anything. Receipts is nil when Expo is not
// configured, in which case the receipt job is a no-op.
type NotifyDeps struct {
	Router     *notify.Router
	Receipts   ReceiptChecker
	Configured bool
}
```

- [ ] **Step 2: Write the receipt and prune workers**

Append to `apps/api/internal/jobs/notifications.go`:

```go
// CheckReceiptsArgs is the periodic Expo receipt reconciliation job.
type CheckReceiptsArgs struct{}

// Kind identifies the job type in River.
func (CheckReceiptsArgs) Kind() string { return "check_receipts" }

// InsertOpts pins the job to the notifications queue.
func (CheckReceiptsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// CheckReceiptsWorker reconciles Expo delivery receipts. Expo reports terminal
// failures — most importantly DeviceNotRegistered — only in the receipts,
// never in the send response, so this is the only place a dead token is
// discovered.
//
// Overlapping runs re-check the same tickets, which is harmless: marking a
// device failed twice is idempotent. That is why no "checked" column exists —
// the bounded one-hour lookback is sufficient.
type CheckReceiptsWorker struct {
	river.WorkerDefaults[CheckReceiptsArgs]
	Store *store.Store
	Deps  NotifyDeps
}

// Work fetches receipts for recently sent tickets and retires dead devices.
func (w *CheckReceiptsWorker) Work(ctx context.Context, _ *river.Job[CheckReceiptsArgs]) error {
	if w.Deps.Receipts == nil {
		return nil
	}
	rows, err := w.Store.Queries.ListRecentTickets(ctx)
	if err != nil {
		return fmt.Errorf("listing recent tickets: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	ids := make([]string, 0, len(rows))
	tokenFor := make(map[string]string, len(rows))
	for _, row := range rows {
		ids = append(ids, row.TicketID)
		tokenFor[row.TicketID] = row.Token
	}

	codes, err := w.Deps.Receipts.Receipts(ctx, ids)
	if err != nil {
		return fmt.Errorf("fetching receipts: %w", err)
	}
	for id, code := range codes {
		if code != "DeviceNotRegistered" {
			continue
		}
		token := tokenFor[id]
		if token == "" {
			continue
		}
		if err := w.Store.Queries.MarkPushDeviceFailed(ctx, token); err != nil {
			return fmt.Errorf("marking device failed: %w", err)
		}
		slog.Info("check_receipts: retired unregistered device", "ticket_id", id)
	}
	return nil
}

// PruneNotificationsArgs is the periodic retention job.
type PruneNotificationsArgs struct{}

// Kind identifies the job type in River.
func (PruneNotificationsArgs) Kind() string { return "prune_notifications" }

// InsertOpts pins the job to the notifications queue.
func (PruneNotificationsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: notifyQueue}
}

// PruneNotificationsWorker enforces retention. Without it the outbox and its
// ledger are the fastest-growing tables in the application, and abandoned rows
// would occupy the pending partial index indefinitely.
type PruneNotificationsWorker struct {
	river.WorkerDefaults[PruneNotificationsArgs]
	Store *store.Store
}

// Work deletes aged-out and abandoned notification rows; deliveries cascade.
func (w *PruneNotificationsWorker) Work(ctx context.Context, _ *river.Job[PruneNotificationsArgs]) error {
	n, err := w.Store.Queries.PruneNotifications(ctx)
	if err != nil {
		return fmt.Errorf("pruning notifications: %w", err)
	}
	if n > 0 {
		slog.Info("prune_notifications: deleted rows", "count", n)
	}
	return nil
}
```

- [ ] **Step 3: Write the receipt test**

Create `apps/api/internal/jobs/notifications_receipts_test.go`:

```go
package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// stubReceipts returns a fixed ticket-to-error-code map.
type stubReceipts struct {
	codes map[string]string
	asked []string
}

func (s *stubReceipts) Receipts(_ context.Context, ids []string) (map[string]string, error) {
	s.asked = ids
	return s.codes, nil
}

func TestCheckReceiptsRetiresUnregisteredDevice(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	for _, tok := range []string{"ExponentPushToken[dead]", "ExponentPushToken[live]"} {
		if err := s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: tok, Platform: "ios"}); err != nil {
			t.Fatalf("upsert %s: %v", tok, err)
		}
	}
	if err := s.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
		UserID: uid, Category: "digest", DedupeKey: "d1", Title: "t", Body: "", Data: []byte(`{}`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	due, _ := s.Queries.ListDueNotifications(ctx, uid)
	for _, tc := range []struct{ token, ticket string }{
		{"ExponentPushToken[dead]", "ticket-dead"},
		{"ExponentPushToken[live]", "ticket-live"},
	} {
		if err := s.Queries.RecordDelivery(ctx, db.RecordDeliveryParams{
			UserID: uid, NotificationID: due[0].ID, Channel: "expo",
			Token: tc.token, TicketID: tc.ticket, Ok: true,
		}); err != nil {
			t.Fatalf("record delivery: %v", err)
		}
	}

	w := &CheckReceiptsWorker{Store: s, Deps: NotifyDeps{Receipts: &stubReceipts{codes: map[string]string{
		"ticket-dead": "DeviceNotRegistered",
		"ticket-live": "",
	}}}}
	if err := w.Work(ctx, &river.Job[CheckReceiptsArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	devices, err := s.Queries.ListPushDevices(ctx, uid)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 || devices[0].Token != "ExponentPushToken[live]" {
		t.Errorf("devices = %+v, want only the live token", devices)
	}
}

// Re-running must be safe — the worker deliberately has no "checked" marker.
func TestCheckReceiptsIsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	s.Queries.EnsureUser(ctx, uid)
	s.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{UserID: uid, Token: "ExponentPushToken[dead]", Platform: "ios"})
	s.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
		UserID: uid, Category: "digest", DedupeKey: "d1", Title: "t", Body: "", Data: []byte(`{}`),
	})
	due, _ := s.Queries.ListDueNotifications(ctx, uid)
	s.Queries.RecordDelivery(ctx, db.RecordDeliveryParams{
		UserID: uid, NotificationID: due[0].ID, Channel: "expo",
		Token: "ExponentPushToken[dead]", TicketID: "ticket-dead", Ok: true,
	})

	w := &CheckReceiptsWorker{Store: s, Deps: NotifyDeps{Receipts: &stubReceipts{
		codes: map[string]string{"ticket-dead": "DeviceNotRegistered"},
	}}}
	job := &river.Job[CheckReceiptsArgs]{}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("first Work: %v", err)
	}
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("second Work: %v", err)
	}
	devices, _ := s.Queries.ListPushDevices(ctx, uid)
	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none", devices)
	}
}

// With Expo unconfigured the job must be a silent no-op, not a failure.
func TestCheckReceiptsNoopWithoutExpo(t *testing.T) {
	s := testStore(t)
	w := &CheckReceiptsWorker{Store: s, Deps: NotifyDeps{}}
	if err := w.Work(context.Background(), &river.Job[CheckReceiptsArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
}

func TestPruneNotificationsRunsClean(t *testing.T) {
	s := testStore(t)
	w := &PruneNotificationsWorker{Store: s}
	job := &river.Job[PruneNotificationsArgs]{}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("first Work: %v", err)
	}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("second Work: %v", err)
	}
}
```

- [ ] **Step 4: Run the jobs tests**

```bash
cd apps/api && go test ./internal/jobs/ -v
```

Expected: PASS, including the Task 5 flush tests.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/jobs/ apps/api/internal/store/
git commit -m "feat(jobs): Expo receipt reconciliation and notification retention"
```

---

## Task 7: Push-device registration endpoints

**Files:**
- Modify: `openapi.yaml`
- Modify: `apps/api/internal/api/middleware.go`, `apps/api/internal/api/auth.go`
- Create: `apps/api/internal/api/pushdevices.go`
- Test: `apps/api/internal/api/pushdevices_test.go`

**Interfaces:**
- Consumes: `db.UpsertPushDevice`, `db.DeletePushDevice` from Task 1.
- Produces: `RegisterPushDevice` and `UnregisterPushDevice` handlers on `*Server`; `withAPIKeyID(ctx, id)` / `apiKeyID(ctx)` in `middleware.go`.

- [ ] **Step 1: Add the operations to `openapi.yaml`**

Add under `paths:`, alongside `/settings`:

```yaml
  /push-devices:
    post:
      operationId: registerPushDevice
      description: "Register (or refresh) an Expo push token for the calling device. Idempotent on the token; re-registering clears any prior delivery failure. The row is tied to the calling API key, so signing out removes it."
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/RegisterPushDeviceRequest" }
      responses:
        "204": { description: registered }
        "400": { description: invalid token or platform }
  /push-devices/unregister:
    post:
      operationId: unregisterPushDevice
      description: "Stop delivering push notifications to a token. Deliberately a POST rather than DELETE /push-devices/{token}: an Expo token contains square brackets, which are an encoding hazard in a path segment across the generated clients."
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/UnregisterPushDeviceRequest" }
      responses:
        "204": { description: unregistered }
        "400": { description: missing token }
```

Add under `components: schemas:`:

```yaml
    RegisterPushDeviceRequest:
      type: object
      required: [token, platform]
      properties:
        token: { type: string, description: "Expo push token, e.g. ExponentPushToken[xxx]." }
        platform: { type: string, enum: [ios, android] }
    UnregisterPushDeviceRequest:
      type: object
      required: [token]
      properties:
        token: { type: string }
```

- [ ] **Step 2: Regenerate the contract**

```bash
task generate
```

This adds `RegisterPushDevice`/`UnregisterPushDevice` to `ServerInterface` in `gen.go` and the operations to `packages/api-client`. The build will now fail until the handlers exist — that is the contract doing its job.

- [ ] **Step 3: Carry the API key ID in the request context**

In `apps/api/internal/api/middleware.go`, replace the context-key block:

```go
type ctxKey int

const userIDKey ctxKey = iota
```

with:

```go
type ctxKey int

const (
	userIDKey ctxKey = iota
	apiKeyIDKey
)

// withAPIKeyID returns a context carrying the API key the caller authenticated
// with. Only bearer-key requests have one; Clerk and dev-mode requests do not.
func withAPIKeyID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, apiKeyIDKey, id)
}

// apiKeyID returns the authenticating API key ID and whether one was present.
func apiKeyID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(apiKeyIDKey).(uuid.UUID)
	return id, ok
}
```

In `apps/api/internal/api/auth.go`, change `resolveAPIKey` to return the key ID too:

```go
// resolveAPIKey looks up the user owning a full API key, and the key's own ID.
// Revoked and unknown keys both fail the lookup (GetAPIKeyByHash filters out
// revoked_at IS NOT NULL), so both resolve to the same 401. A successful
// lookup touches last_used_at, throttled to at most once per
// touchLastUsedInterval.
func resolveAPIKey(ctx context.Context, s *store.Store, full string) (uuid.UUID, uuid.UUID, bool) {
	if s == nil {
		return uuid.UUID{}, uuid.UUID{}, false
	}
	row, err := s.Queries.GetAPIKeyByHash(ctx, auth.HashKey(full))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false
	}
	if !row.LastUsedAt.Valid || time.Since(row.LastUsedAt.Time) > touchLastUsedInterval {
		if err := s.Queries.TouchAPIKeyLastUsed(ctx, row.ApiKeyID); err != nil {
			slog.Error("touching api key last_used_at", "err", err)
		}
	}
	return row.UserID, row.ApiKeyID, true
}
```

Find every `resolveAPIKey(` call site:

```bash
cd apps/api && grep -rn "resolveAPIKey(" internal/api/
```

At each call site, capture the new return value and attach it to the context alongside the user ID. The bearer branch becomes:

```go
		if uid, keyID, ok := resolveAPIKey(r.Context(), s, token); ok {
			ctx := withAPIKeyID(withUserID(r.Context(), uid), keyID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
```

- [ ] **Step 4: Write the failing handler test**

Create `apps/api/internal/api/pushdevices_test.go`. Match the existing harness in `apps/api/internal/api/settings_test.go` for server construction — read it first and mirror its setup helper:

```bash
cd apps/api && sed -n '1,40p' internal/api/settings_test.go
```

Then write:

```go
package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterPushDeviceIsIdempotent(t *testing.T) {
	srv, uid := newTestServer(t)
	body := `{"token":"ExponentPushToken[abc]","platform":"ios"}`

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/push-devices", bytes.NewBufferString(body))
		req = req.WithContext(withUserID(context.Background(), uid))
		w := httptest.NewRecorder()
		srv.RegisterPushDevice(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body)
		}
	}

	devices, err := srv.store.Queries.ListPushDevices(context.Background(), uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("devices = %d, want 1 after two registrations", len(devices))
	}
}

func TestRegisterPushDeviceRejectsBadPlatform(t *testing.T) {
	srv, uid := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/push-devices", bytes.NewBufferString(`{"token":"t","platform":"blackberry"}`))
	req = req.WithContext(withUserID(context.Background(), uid))
	w := httptest.NewRecorder()
	srv.RegisterPushDevice(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestUnregisterPushDevice(t *testing.T) {
	srv, uid := newTestServer(t)
	ctx := withUserID(context.Background(), uid)

	reg := httptest.NewRequest(http.MethodPost, "/push-devices", bytes.NewBufferString(`{"token":"ExponentPushToken[abc]","platform":"ios"}`))
	srv.RegisterPushDevice(httptest.NewRecorder(), reg.WithContext(ctx))

	req := httptest.NewRequest(http.MethodPost, "/push-devices/unregister", bytes.NewBufferString(`{"token":"ExponentPushToken[abc]"}`))
	w := httptest.NewRecorder()
	srv.UnregisterPushDevice(w, req.WithContext(ctx))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	devices, _ := srv.store.Queries.ListPushDevices(context.Background(), uid)
	if len(devices) != 0 {
		t.Errorf("devices = %d, want 0", len(devices))
	}
}
```

If `newTestServer` does not exist under that name in `settings_test.go`, use whatever helper that file uses and adjust these three tests to match — do not invent a second harness.

- [ ] **Step 5: Run to verify it fails**

```bash
cd apps/api && go test ./internal/api/ -run TestRegisterPushDevice -v
```

Expected: FAIL — `srv.RegisterPushDevice undefined`.

- [ ] **Step 6: Write `pushdevices.go`**

Create `apps/api/internal/api/pushdevices.go`:

```go
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// maxPushTokenLen bounds the stored token. Expo tokens are ~50 characters;
// this is generous headroom that still refuses junk.
const maxPushTokenLen = 512

// RegisterPushDevice records an Expo push token for the calling device. It is
// idempotent on the token and clears any prior failure marker, so a client
// that re-registers after reinstalling starts receiving pushes again.
func (s *Server) RegisterPushDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Token == "" || len(req.Token) > maxPushTokenLen {
		writeError(w, http.StatusBadRequest, "invalid push token")
		return
	}
	if req.Platform != "ios" && req.Platform != "android" {
		writeError(w, http.StatusBadRequest, "platform must be ios or android")
		return
	}

	// Tying the row to the calling API key means signing out (which revokes
	// the key) cascades the device away, rather than leaving a token pushing
	// at a device that is no longer signed in. Clerk and dev-mode callers have
	// no key, and simply get a null reference.
	var keyID uuid.NullUUID
	if id, ok := apiKeyID(ctx); ok {
		keyID = uuid.NullUUID{UUID: id, Valid: true}
	}

	if err := s.store.Queries.UpsertPushDevice(ctx, db.UpsertPushDeviceParams{
		UserID:   userID(ctx),
		ApiKeyID: keyID,
		Token:    req.Token,
		Platform: req.Platform,
	}); err != nil {
		slog.Error("upserting push device", "err", err)
		writeError(w, http.StatusInternalServerError, "could not register device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnregisterPushDevice stops delivering to a token. Removing a token that is
// not registered is not an error: the caller's desired end state is reached
// either way.
func (s *Server) UnregisterPushDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "missing token")
		return
	}
	if _, err := s.store.Queries.DeletePushDevice(ctx, db.DeletePushDeviceParams{
		UserID: userID(ctx), Token: req.Token,
	}); err != nil {
		slog.Error("deleting push device", "err", err)
		writeError(w, http.StatusInternalServerError, "could not unregister device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

If sqlc typed `api_key_id` as `pgtype.UUID` rather than `uuid.NullUUID`, adapt the two lines accordingly — check the generated `db.UpsertPushDeviceParams`.

- [ ] **Step 7: Run to verify it passes**

```bash
cd apps/api && go test ./internal/api/ -run 'PushDevice' -v
```

Expected: PASS.

- [ ] **Step 8: Build and run the full API suite**

```bash
cd apps/api && go build ./... && go test ./...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add openapi.yaml apps/api/internal/api/ packages/api-client/
git commit -m "feat(api): push-device registration endpoints"
```

---

## Task 8: Notification preferences on /settings

**Files:**
- Modify: `openapi.yaml`, `apps/api/internal/api/settings.go`
- Test: `apps/api/internal/api/settings_test.go`

**Interfaces:**
- Consumes: `notify.Key*` constants from Task 2; existing `ListUserSettings`/`UpsertUserSetting`/`DeleteUserSetting`.
- Produces: six new fields on the `Settings` response and `PatchSettingsRequest`.

- [ ] **Step 1: Extend the schemas in `openapi.yaml`**

Replace the `Settings` and `PatchSettingsRequest` schema bodies:

```yaml
    Settings:
      type: object
      properties:
        kindleEmail: { type: string, format: email, description: "Destination e-mail for Send-to-Kindle digests; absent if not configured." }
        notifyDigest: { type: string, enum: [off, push, email, both], description: "Channels for Lens digest notifications. Default push." }
        notifyFeedRiver: { type: string, enum: [off, push, email, both], description: "Channels for coalesced feed-river activity. Default off." }
        notifyLifecycle: { type: string, enum: [off, push, email, both], description: "Channels for save-failure notifications. Default push. Exempt from the daily cap." }
        notifyQuietHours: { type: string, description: "Wall-clock range in the user's timezone, e.g. 22:00-07:00. Empty means none." }
        notifyTimezone: { type: string, description: "IANA timezone used for quiet hours and the daily-cap reset, e.g. Europe/London." }
        notifyDailyCap: { type: integer, description: "Maximum successful deliveries per day before non-lifecycle notifications defer. Default 10." }
    PatchSettingsRequest:
      type: object
      properties:
        kindleEmail: { type: string, format: email }
        notifyDigest: { type: string, enum: [off, push, email, both] }
        notifyFeedRiver: { type: string, enum: [off, push, email, both] }
        notifyLifecycle: { type: string, enum: [off, push, email, both] }
        notifyQuietHours: { type: string }
        notifyTimezone: { type: string }
        notifyDailyCap: { type: integer }
```

- [ ] **Step 2: Regenerate**

```bash
task generate
```

- [ ] **Step 3: Write the failing settings test**

Append to `apps/api/internal/api/settings_test.go`, adapting the harness call to match the file's existing helper:

```go
func TestPatchSettingsNotificationPrefs(t *testing.T) {
	srv, uid := newTestServer(t)
	ctx := withUserID(context.Background(), uid)

	body := `{"notifyDigest":"both","notifyFeedRiver":"push","notifyQuietHours":"22:00-07:00","notifyTimezone":"Europe/London","notifyDailyCap":5}`
	req := httptest.NewRequest(http.MethodPatch, "/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	srv.PatchSettings(w, req.WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body)
	}

	var got Settings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.NotifyDigest == nil || *got.NotifyDigest != "both" {
		t.Errorf("NotifyDigest = %v, want both", got.NotifyDigest)
	}
	if got.NotifyDailyCap == nil || *got.NotifyDailyCap != 5 {
		t.Errorf("NotifyDailyCap = %v, want 5", got.NotifyDailyCap)
	}
}

func TestPatchSettingsRejectsBadQuietHours(t *testing.T) {
	srv, uid := newTestServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/settings", bytes.NewBufferString(`{"notifyQuietHours":"bedtime"}`))
	w := httptest.NewRecorder()
	srv.PatchSettings(w, req.WithContext(withUserID(context.Background(), uid)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPatchSettingsRejectsBadTimezone(t *testing.T) {
	srv, uid := newTestServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/settings", bytes.NewBufferString(`{"notifyTimezone":"Mars/Olympus"}`))
	w := httptest.NewRecorder()
	srv.PatchSettings(w, req.WithContext(withUserID(context.Background(), uid)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
```

- [ ] **Step 4: Run to verify it fails**

```bash
cd apps/api && go test ./internal/api/ -run TestPatchSettings -v
```

Expected: FAIL — the notify fields are not read or written yet.

- [ ] **Step 5: Extend `currentSettings` in `settings.go`**

Replace the mapping loop:

```go
	out := Settings{}
	for _, row := range rows {
		switch row.Key {
		case kindleSettingKey:
			email := openapi_types.Email(row.Value)
			out.KindleEmail = &email
		case notify.KeyDigest:
			v := row.Value
			out.NotifyDigest = &v
		case notify.KeyFeedRiver:
			v := row.Value
			out.NotifyFeedRiver = &v
		case notify.KeyLifecycle:
			v := row.Value
			out.NotifyLifecycle = &v
		case notify.KeyQuietHours:
			v := row.Value
			out.NotifyQuietHours = &v
		case notify.KeyTimezone:
			v := row.Value
			out.NotifyTimezone = &v
		case notify.KeyDailyCap:
			if n, err := strconv.Atoi(row.Value); err == nil {
				out.NotifyDailyCap = &n
			}
		}
	}
```

Add imports: `"strconv"` and `"github.com/rohithgilla12/openmind/api/internal/notify"`.

If oapi-codegen typed the enum fields as named types rather than `string`, cast accordingly — check the regenerated `Settings` struct in `gen.go` before writing this.

- [ ] **Step 6: Extend `PatchSettings` in `settings.go`**

Extend the decode struct:

```go
	var req struct {
		KindleEmail      *string `json:"kindleEmail"`
		NotifyDigest     *string `json:"notifyDigest"`
		NotifyFeedRiver  *string `json:"notifyFeedRiver"`
		NotifyLifecycle  *string `json:"notifyLifecycle"`
		NotifyQuietHours *string `json:"notifyQuietHours"`
		NotifyTimezone   *string `json:"notifyTimezone"`
		NotifyDailyCap   *int    `json:"notifyDailyCap"`
	}
```

After the existing `KindleEmail` block, add:

```go
	// Each notify.* preference follows the same rule as kindleEmail: an
	// explicit empty string clears the row (restoring the documented default),
	// any other value is validated then upserted.
	for _, pref := range []struct {
		key   string
		value *string
		valid func(string) bool
	}{
		{notify.KeyDigest, req.NotifyDigest, validChannelPref},
		{notify.KeyFeedRiver, req.NotifyFeedRiver, validChannelPref},
		{notify.KeyLifecycle, req.NotifyLifecycle, validChannelPref},
		{notify.KeyQuietHours, req.NotifyQuietHours, validQuietHours},
		{notify.KeyTimezone, req.NotifyTimezone, validTimezone},
	} {
		if pref.value == nil {
			continue
		}
		if !s.applyPref(w, ctx, uid, pref.key, *pref.value, pref.valid) {
			return
		}
	}

	if req.NotifyDailyCap != nil {
		if *req.NotifyDailyCap < 0 || *req.NotifyDailyCap > 200 {
			writeError(w, http.StatusBadRequest, "notifyDailyCap must be between 0 and 200")
			return
		}
		if err := s.store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{
			UserID: uid, Key: notify.KeyDailyCap, Value: strconv.Itoa(*req.NotifyDailyCap),
		}); err != nil {
			slog.Error("upserting daily cap", "err", err)
			writeError(w, http.StatusInternalServerError, "could not update settings")
			return
		}
	}
```

Add the helpers at the end of `settings.go`:

```go
// applyPref clears or upserts one notify.* preference. It reports false when
// it has already written an error response.
func (s *Server) applyPref(w http.ResponseWriter, ctx context.Context, uid uuid.UUID, key, value string, valid func(string) bool) bool {
	if value == "" {
		if _, err := s.store.Queries.DeleteUserSetting(ctx, db.DeleteUserSettingParams{UserID: uid, Key: key}); err != nil {
			slog.Error("deleting notify setting", "key", key, "err", err)
			writeError(w, http.StatusInternalServerError, "could not update settings")
			return false
		}
		return true
	}
	if !valid(value) {
		writeError(w, http.StatusBadRequest, "invalid value for "+key)
		return false
	}
	if err := s.store.Queries.UpsertUserSetting(ctx, db.UpsertUserSettingParams{UserID: uid, Key: key, Value: value}); err != nil {
		slog.Error("upserting notify setting", "key", key, "err", err)
		writeError(w, http.StatusInternalServerError, "could not update settings")
		return false
	}
	return true
}

// validChannelPref accepts the four documented channel selections.
func validChannelPref(v string) bool {
	switch v {
	case "off", "push", "email", "both":
		return true
	default:
		return false
	}
}

// validQuietHours accepts an HH:MM-HH:MM wall-clock range.
func validQuietHours(v string) bool {
	from, to, found := strings.Cut(v, "-")
	if !found {
		return false
	}
	_, errFrom := time.Parse("15:04", from)
	_, errTo := time.Parse("15:04", to)
	return errFrom == nil && errTo == nil
}

// validTimezone accepts any IANA zone the runtime can load.
func validTimezone(v string) bool {
	_, err := time.LoadLocation(v)
	return err == nil
}
```

Add imports: `"context"`, `"strings"`, `"time"`, `"github.com/google/uuid"`.

- [ ] **Step 7: Run to verify it passes**

```bash
cd apps/api && go test ./internal/api/ -run TestPatchSettings -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add openapi.yaml apps/api/internal/api/settings.go apps/api/internal/api/settings_test.go apps/api/internal/api/gen.go packages/api-client/
git commit -m "feat(api): notification preferences on /settings"
```

---

## Task 9: Wire the three producers

**Files:**
- Modify: `apps/api/internal/jobs/digest.go`, `apps/api/internal/jobs/enrich.go`, `apps/api/internal/feeds/service.go`
- Test: `apps/api/internal/jobs/producers_test.go`

**Interfaces:**
- Consumes: `db.EnqueueNotification` from Task 1; `notify.Category*` from Task 2.
- Produces: a shared `enqueueNotification` helper in `internal/jobs`.

- [ ] **Step 1: Add the shared producer helper**

Append to `apps/api/internal/jobs/notifications.go`:

```go
// enqueueNotification writes one outbox row. Producers call this and nothing
// else: channels, preferences, coalescing, and retries are the flush job's
// concern. ON CONFLICT DO NOTHING in the query makes a re-run a no-op, so
// callers need no idempotency logic of their own.
func enqueueNotification(ctx context.Context, s *store.Store, userID uuid.UUID, cat notify.Category, dedupeKey, title, body string, data map[string]any) error {
	payload := []byte("{}")
	if len(data) > 0 {
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshalling notification data: %w", err)
		}
		payload = encoded
	}
	if err := s.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
		UserID:    userID,
		Category:  string(cat),
		DedupeKey: dedupeKey,
		Title:     title,
		Body:      body,
		Data:      payload,
	}); err != nil {
		return fmt.Errorf("enqueueing notification: %w", err)
	}
	return nil
}
```

`EnqueueNotificationParams` has no `DeliverAfter` field by design — the column takes its `DEFAULT now()`, and deferral happens later via `DeferNotifications`. If you find yourself wanting to set it here, you want a deferral, not a producer change.

- [ ] **Step 2: Add the digest producer**

In `apps/api/internal/jobs/digest.go`, inside `processLens`, after the transaction commits successfully (immediately before `return nil`):

```go
	// A failed notification must never undo a sent digest, so this is
	// deliberately outside the transaction and only logged on error.
	title := fmt.Sprintf("%s digest — %s", lens.Name, plural(len(ids), "new save", "new saves"))
	if err := enqueueNotification(ctx, w.Store, lens.UserID, notify.CategoryDigest,
		fmt.Sprintf("digest:%s:%s", lens.ID, time.Now().UTC().Format("2006-01-02")),
		title, "", map[string]any{"lens_id": lens.ID.String()}); err != nil {
		slog.Error("scan_digests: enqueueing digest notification", "lens_id", lens.ID, "err", err)
	}
	return nil
```

Add a local `plural` helper to `internal/jobs` (the one in `internal/notify` is unexported and not importable):

```go
// plural renders "1 new save" / "7 new saves".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
```

Put it in `notifications.go` and add `"github.com/rohithgilla12/openmind/api/internal/notify"` to `digest.go`'s imports.

- [ ] **Step 3: Add the lifecycle producer**

In `apps/api/internal/jobs/enrich.go`, find `EnrichWorker.Work`. Wrap the terminal-failure path — River exposes the attempt count on the job, so only the final attempt notifies:

```go
func (w *EnrichWorker) Work(ctx context.Context, job *river.Job[EnrichArgs]) error {
	err := w.Pipeline.Run(ctx, job.Args.ItemID)
	if err == nil {
		return nil
	}
	// Only the final attempt notifies: intermediate retries are expected and
	// invisible to the user. Successful enrichment stays silent by design —
	// the item simply appears in the Library.
	if job.Attempt >= job.MaxAttempts {
		item, getErr := w.Pipeline.Store.Queries.GetItemByID(ctx, job.Args.ItemID)
		if getErr == nil {
			if nerr := enqueueNotification(ctx, w.Pipeline.Store, item.UserID, notify.CategoryLifecycle,
				fmt.Sprintf("lifecycle:enrich-failed:%s", item.ID),
				"We couldn't finish processing a save", "It's still in your Library — open it to retry.",
				map[string]any{"item_id": item.ID.String()}); nerr != nil {
				slog.Error("enrich: enqueueing failure notification", "item_id", item.ID, "err", nerr)
			}
		}
	}
	return err
}
```

Check the actual current body of `EnrichWorker.Work` first and adapt — do not blindly replace it:

```bash
cd apps/api && grep -n -A 12 "func (w \*EnrichWorker) Work" internal/jobs/enrich.go
```

If no `GetItemByID` query exists (one scoped only by ID, for a job that has no user context yet), add it to `internal/store/queries/`:

```sql
-- name: GetItemByID :one
-- Unscoped by user_id because the enrichment job knows only an item ID; the
-- result is used solely to discover the owning user for notification routing.
SELECT id, user_id FROM items WHERE id = $1;
```

- [ ] **Step 4: Add the feed-river producer**

In `apps/api/internal/feeds/service.go`, find where new river items are recorded after a poll. Add, once per feed that gained items:

```go
	// One row per feed per hour: the flush coalesces them into a single
	// "N new items across M feeds" message. A single row per hour would be
	// collapsed by the pending dedupe index and could never carry a count.
	dedupe := fmt.Sprintf("feed_river:%s:%s", feed.ID, time.Now().UTC().Format("2006-01-02T15"))
	if err := s.store.Queries.EnqueueNotification(ctx, db.EnqueueNotificationParams{
		UserID:    feed.UserID,
		Category:  "feed_river",
		DedupeKey: dedupe,
		Title:     fmt.Sprintf("%d new items", newCount),
		Body:      "",
		Data:      []byte(fmt.Sprintf(`{"feed_id":%q,"count":%d}`, feed.ID, newCount)),
	}); err != nil {
		slog.Error("poll: enqueueing feed river notification", "feed_id", feed.ID, "err", err)
	}
```

`internal/feeds` cannot import `internal/jobs` (that would be an import cycle), so it calls the query directly. Use the actual variable names from the surrounding code for `feed` and `newCount`; read the function first:

```bash
cd apps/api && grep -n "func (s \*Service)" internal/feeds/service.go
```

- [ ] **Step 5: Write the producer test**

Create `apps/api/internal/jobs/producers_test.go`:

```go
package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rohithgilla12/openmind/api/internal/notify"
)

// A producer running twice must leave exactly one pending row — the outbox
// dedupe is what makes every producer safe to retry.
func TestEnqueueNotificationIsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uuid.New()
	if err := s.Queries.EnsureUser(ctx, uid); err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	for range 2 {
		if err := enqueueNotification(ctx, s, uid, notify.CategoryDigest,
			"digest:lens-a:2026-07-27", "Design digest — 7 new saves", "",
			map[string]any{"lens_id": "lens-a"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	due, err := s.Queries.ListDueNotifications(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("pending = %d, want 1", len(due))
	}
	if due[0].Title != "Design digest — 7 new saves" {
		t.Errorf("Title = %q", due[0].Title)
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "new save", "new saves"); got != "1 new save" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(7, "new save", "new saves"); got != "7 new saves" {
		t.Errorf("plural(7) = %q", got)
	}
}
```

- [ ] **Step 6: Run the whole Go suite**

```bash
cd apps/api && go build ./... && go test ./... && go vet ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal/
git commit -m "feat(jobs): digest, feed-river, and lifecycle notification producers"
```

---

## Task 10: Configuration, compose, and docs

**Files:**
- Modify: `apps/api/cmd/openmind/main.go`, `docker-compose.yml`, `.env.example`, `docs/self-hosting.md`

**Interfaces:**
- Consumes: `notify.NewExpo`, `notify.NewEmail`, `notify.NewNoop`, `notify.NewRouter`, `jobs.NotifyDeps`.
- Produces: `buildNotifyDeps(m mailer.Mailer) jobs.NotifyDeps` in `main.go`.

- [ ] **Step 1: Build the router from env in `main.go`**

Add near the existing `buildMailer`-style helpers (around the SMTP block at line ~227):

```go
// buildNotifyDeps assembles the notification router from NOTIFY_CHANNELS.
// An empty or unset value yields noop senders on both channels, which keeps
// the app fully functional with no delivery configured: producers still
// enqueue, the flush still stamps, and nothing is sent.
func buildNotifyDeps(m mailer.Mailer) jobs.NotifyDeps {
	channels := map[string]bool{}
	for _, c := range strings.Split(os.Getenv("NOTIFY_CHANNELS"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			channels[c] = true
		}
	}

	push, email := notify.NewNoop(), notify.NewNoop()
	var receipts jobs.ReceiptChecker

	if channels["expo"] {
		e := notify.NewExpo(os.Getenv("EXPO_ACCESS_TOKEN"))
		push, receipts = e, e
	}
	if channels["email"] {
		if m == nil {
			slog.Warn("NOTIFY_CHANNELS includes email but SMTP is not configured; e-mail notifications disabled")
		} else {
			email = notify.NewEmail(m)
		}
	}

	configured := len(channels) > 0
	if !configured {
		slog.Info("NOTIFY_CHANNELS unset; notifications will be recorded but not delivered")
	}
	return jobs.NotifyDeps{
		Router:     notify.NewRouter(push, email),
		Receipts:   receipts,
		Configured: configured,
	}
}
```

Add imports as needed: `"strings"`, `"log/slog"`, and the `notify` package.

- [ ] **Step 2: Pass the deps into `NewRiverClient`**

Find the existing call:

```bash
cd apps/api && grep -n "NewRiverClient(" cmd/openmind/main.go
```

Insert `buildNotifyDeps(m)` as the new fifth argument, after `kindleDeps`, matching the signature change from Task 5. Use whatever variable already holds the `mailer.Mailer` at that point — if the mailer is constructed inside `buildKindleDeps`, hoist it to a local first so both can share it.

- [ ] **Step 3: Add the env vars to `docker-compose.yml`**

In the `api` service's `environment:` block, after `KINDLE_EMAIL`:

```yaml
      NOTIFY_CHANNELS: ${NOTIFY_CHANNELS:-}
      EXPO_ACCESS_TOKEN: ${EXPO_ACCESS_TOKEN:-}
```

This step is not optional. A new API env var that is not in this block never reaches the container, and the failure is silent — the feature simply does nothing in Docker while working locally.

- [ ] **Step 4: Document both in `.env.example`**

Append:

```bash
# Notifications. Comma-separated channels: expo, email. Empty disables
# delivery entirely (notifications are still recorded, then pruned).
NOTIFY_CHANNELS=
# Optional. Expo push works unauthenticated; a token adds send-security.
# Create one at https://expo.dev under Account Settings -> Access Tokens.
EXPO_ACCESS_TOKEN=
```

- [ ] **Step 5: Document in `docs/self-hosting.md`**

Add a "Notifications" section covering: the two env vars; that `email` also requires the existing `SMTP_*` settings; that mobile push additionally needs an app build carrying your own Expo project ID (self-hosters building their own binary need their own FCM/APNs credentials); and that leaving `NOTIFY_CHANNELS` empty is a fully supported configuration.

Match the surrounding heading level and prose style of the file.

- [ ] **Step 6: Verify the full build and a clean compose boot**

```bash
cd apps/api && go build ./... && go test ./...
```

Then, from the repo root — ask the user before starting containers, as they usually have an instance running:

```bash
docker compose config | grep -A 2 NOTIFY_CHANNELS
```

Expected: the variable appears in the rendered `api` service environment.

- [ ] **Step 7: Commit**

```bash
git add apps/api/cmd/openmind/main.go docker-compose.yml .env.example docs/self-hosting.md
git commit -m "feat(config): NOTIFY_CHANNELS and EXPO_ACCESS_TOKEN wiring"
```

---

## Task 11: Mobile — registration, permission UI, tap routing

**Files:**
- Modify: `apps/mobile/package.json`, `apps/mobile/app.config.js`
- Create: `apps/mobile/lib/notifications.ts`, `apps/mobile/lib/notifications.test.ts`
- Modify: the mobile settings screen and root layout

**Interfaces:**
- Consumes: `POST /push-devices` and `POST /push-devices/unregister` from the regenerated api-client.
- Produces:
  - `registerForPushAsync(): Promise<{ok: true, token: string} | {ok: false, reason: 'denied' | 'unsupported' | 'error'}>`
  - `routeForNotificationData(data: unknown): string | null`

- [ ] **Step 1: Install the dependency**

```bash
cd apps/mobile && pnpm add expo-notifications
```

Then align versions to the SDK, which has previously caused a launch crash when skipped:

```bash
cd apps/mobile && ./node_modules/.bin/expo install --fix
```

- [ ] **Step 2: Configure the plugin**

In `apps/mobile/app.config.js`, add to the `plugins` array:

```js
      [
        'expo-notifications',
        {
          color: '#1B3FD1',
        },
      ],
```

`#1B3FD1` is the cobalt accent from the design tokens. Do not introduce a new colour.

- [ ] **Step 3: Write the failing deep-link routing test**

Create `apps/mobile/lib/notifications.test.ts`:

```ts
import { routeForNotificationData } from './notifications';

describe('routeForNotificationData', () => {
  it('routes an item notification to the item detail screen', () => {
    expect(routeForNotificationData({ item_id: 'abc' })).toBe('/item/abc');
  });

  it('routes a lens notification to the lens screen', () => {
    expect(routeForNotificationData({ lens_id: 'design' })).toBe('/lens/design');
  });

  it('routes a single-feed river notification to that feed', () => {
    expect(routeForNotificationData({ feed_id: 'f1' })).toBe('/feed/f1');
  });

  it('routes a mixed-feed roll-up to the river root', () => {
    expect(routeForNotificationData({})).toBe('/feed');
  });

  it('returns null for a payload it cannot understand', () => {
    expect(routeForNotificationData(null)).toBeNull();
    expect(routeForNotificationData('nonsense')).toBeNull();
    expect(routeForNotificationData({ item_id: 42 })).toBeNull();
  });
});
```

Confirm the real route paths before finalising this test:

```bash
ls apps/mobile/app && ls apps/mobile/app/\(tabs\) 2>/dev/null
```

Adjust `/item/`, `/lens/`, `/feed/` to whatever expo-router paths actually exist. The test must assert real routes, not invented ones.

- [ ] **Step 4: Run to verify it fails**

```bash
cd apps/mobile && pnpm test -- notifications
```

Expected: FAIL — module not found.

- [ ] **Step 5: Write `lib/notifications.ts`**

Create `apps/mobile/lib/notifications.ts`:

```ts
import * as Notifications from 'expo-notifications';
import Constants from 'expo-constants';
import { Platform } from 'react-native';

export type RegisterResult =
  | { ok: true; token: string }
  | { ok: false; reason: 'denied' | 'unsupported' | 'error' };

/**
 * Requests notification permission and returns an Expo push token.
 *
 * Call this only from an explicit user action (the Notifications toggle in
 * Settings) — never on launch. iOS grants exactly one system prompt, and once
 * it is denied the only recovery is sending the user into system Settings.
 */
export async function registerForPushAsync(): Promise<RegisterResult> {
  try {
    if (Platform.OS === 'android') {
      await Notifications.setNotificationChannelAsync('default', {
        name: 'Openmind',
        importance: Notifications.AndroidImportance.DEFAULT,
        lightColor: '#1B3FD1',
      });
    }

    const existing = await Notifications.getPermissionsAsync();
    let status = existing.status;
    if (status !== 'granted') {
      const asked = await Notifications.requestPermissionsAsync();
      status = asked.status;
    }
    if (status !== 'granted') {
      return { ok: false, reason: 'denied' };
    }

    const projectId =
      Constants.expoConfig?.extra?.eas?.projectId ?? Constants.easConfig?.projectId;
    if (!projectId) {
      return { ok: false, reason: 'unsupported' };
    }

    const { data } = await Notifications.getExpoPushTokenAsync({ projectId });
    return { ok: true, token: data };
  } catch {
    return { ok: false, reason: 'error' };
  }
}

/**
 * Maps a notification's data payload onto an expo-router path.
 *
 * A feed-river roll-up spanning several feeds deliberately carries no
 * feed_id — the server cannot pick one sensibly — so it opens the river root.
 * Returns null when the payload is unrecognised, which the caller treats as
 * "just open the app".
 */
export function routeForNotificationData(data: unknown): string | null {
  if (typeof data !== 'object' || data === null) {
    return null;
  }
  const payload = data as Record<string, unknown>;

  if ('item_id' in payload) {
    return typeof payload.item_id === 'string' ? `/item/${payload.item_id}` : null;
  }
  if ('lens_id' in payload) {
    return typeof payload.lens_id === 'string' ? `/lens/${payload.lens_id}` : null;
  }
  if ('feed_id' in payload) {
    return typeof payload.feed_id === 'string' ? `/feed/${payload.feed_id}` : null;
  }
  return '/feed';
}
```

- [ ] **Step 6: Run to verify it passes**

```bash
cd apps/mobile && pnpm test -- notifications
```

Expected: PASS.

- [ ] **Step 7: Add the Settings toggle**

Find the mobile settings screen:

```bash
ls apps/mobile/app && grep -rln "kindle" apps/mobile/app apps/mobile/components 2>/dev/null
```

Add a "Notifications" row, off by default, that on enable calls `registerForPushAsync()` and then `POST /push-devices` via the generated client, and on disable calls `POST /push-devices/unregister`. On `{ ok: false, reason: 'denied' }`, show a short line explaining that notifications must be re-enabled in system Settings — do not silently leave the toggle on.

Follow the existing screen's component and styling conventions; use design tokens, never hardcoded colours.

- [ ] **Step 8: Add the tap handler**

In the root layout (`apps/mobile/app/_layout.tsx`), register a response listener that routes via `routeForNotificationData` and `router.push`, and handles the cold-start case with `Notifications.getLastNotificationResponseAsync()`. Clean the listener up on unmount.

- [ ] **Step 9: Schedule the offline-drain local notification**

In the existing queue-drain path from PR #44, after a successful drain of N items, schedule a local notification (`Notifications.scheduleNotificationAsync` with a null trigger). This is client-local by necessity: the server sees an ordinary upload and cannot know it was a drain.

Find the drain:

```bash
grep -rn "drain" apps/mobile/lib/
```

- [ ] **Step 10: Typecheck and test**

```bash
cd apps/mobile && pnpm typecheck && pnpm test
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add apps/mobile/
git commit -m "feat(mobile): push registration, permission toggle, and notification routing"
```

**Note:** `expo-notifications` is a native module, so this needs a fresh dev build (`expo run:ios` / EAS preview) before it can be verified on device. Verification is a rollout step, not a plan step.

---

## Task 12: Web preference toggles

**Files:**
- Modify: the web settings page
- Test: a vitest alongside it

**Interfaces:**
- Consumes: `getSettings` / `patchSettings` from the regenerated `@openmind/api-client`, including the six new fields from Task 8.
- Produces: no new exports.

- [ ] **Step 1: Locate the settings page**

```bash
grep -rln "kindleEmail" apps/web/app apps/web/components 2>/dev/null
```

- [ ] **Step 2: Add the controls**

Add a "Notifications" section with:
- Three selects (Digest, Feed activity, Save failures), each `Off / Push / Email / Both`, bound to `notifyDigest`, `notifyFeedRiver`, `notifyLifecycle`.
- A quiet-hours pair of time inputs writing `notifyQuietHours` as `HH:MM-HH:MM`, and clearing it to `""` when either is blank.
- A timezone input defaulting to `Intl.DateTimeFormat().resolvedOptions().timeZone`.
- A daily-cap number input, 0–200.

Copy rules: this is the *configuration* surface only — say plainly that push arrives on mobile and that web does not receive notifications, so nobody expects browser popups. Use design tokens from `packages/ui`; hardcode no colours. Follow the existing page's form patterns rather than introducing a new one.

- [ ] **Step 3: Write the test**

Mirror whatever the existing settings test does. Assert that changing the digest select issues a `patchSettings` call with `notifyDigest` set, and that clearing quiet hours sends `""` rather than omitting the field — omission means "leave unchanged", which would make the control unable to clear.

- [ ] **Step 4: Typecheck, lint, and test**

```bash
pnpm turbo run lint test --filter=web
```

Expected: PASS.

- [ ] **Step 5: Full-repo verification**

```bash
task lint && task test
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/web/
git commit -m "feat(web): notification preference controls on settings"
```

---

## Task 13: Update TODO.md

**Files:**
- Modify: `TODO.md`

- [ ] **Step 1: Move the work to Done and record follow-ups**

Add to `## Done (recent)`, matching the existing entry style (prose, dated, PR reference):

```markdown
- Notifications (PR #NN, merged) — outbox substrate (`notifications`,
  `notification_deliveries`, `push_devices`, migration 0020) with an
  `internal/notify` adapter (`expo`, `email`, `noop`) and four River jobs on a
  dedicated `notifications` queue: scan → per-user flush (coalescing, quiet
  hours, daily cap), Expo receipt reconciliation, and 30-day pruning.
  Producers: Lens digests, feed river (one row per feed per hour, coalesced),
  and enrichment terminal failures. Preferences on `/settings`;
  `feed_river` defaults to off. `NOTIFY_CHANNELS` unset = noop, so stock
  `docker compose up` is unchanged (2026-07-27)
```

Add under `## Later`:

```markdown
- Notifications follow-ups: per-item reminders ("remind me about this save") —
  needs its own spec, own table, item-detail UI; on-device verification of the
  Expo push path (needs a fresh dev build); consider a dedicated River queue
  for delivery if enrichment bursts start delaying flushes
```

- [ ] **Step 2: Commit**

```bash
git add TODO.md
git commit -m "docs: TODO — notifications shipped"
```

---

## Verification Checklist

Before opening the PR:

- [ ] `cd apps/api && go build ./... && go test ./... && go vet ./...` — clean
- [ ] `task lint && task test` — clean
- [ ] `task generate` produces no diff (generated code is committed and current)
- [ ] `docker compose config` shows `NOTIFY_CHANNELS` and `EXPO_ACCESS_TOKEN` on the `api` service
- [ ] With `NOTIFY_CHANNELS` unset, saving an item and running a digest still works end to end, and `notifications` rows are stamped `sent_at`
- [ ] No banner-style divider comments were introduced
- [ ] No hand-edits to `gen.go`, `packages/api-client`, or `internal/store/db`
