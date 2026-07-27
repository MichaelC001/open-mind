package feeds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/rohithgilla12/openmind/api/internal/enrich"
	"github.com/rohithgilla12/openmind/api/internal/jobs"
	"github.com/rohithgilla12/openmind/api/internal/notify"
	"github.com/rohithgilla12/openmind/api/internal/store"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// ErrAlreadySubscribed is returned by Add when the user is already subscribed to
// the given feed URL. Handlers map it to 409.
var ErrAlreadySubscribed = errors.New("already subscribed to feed")

// errNotModified signals a 304 from a conditional poll: the feed body was
// neither downloaded nor parsed; there is nothing new by definition.
var errNotModified = errors.New("feed not modified")

const (
	// fetchTimeout bounds a single feed fetch.
	fetchTimeout = 20 * time.Second
	// maxFeedBytes caps how much of a feed body we read, bounding memory for a
	// hostile or runaway feed.
	maxFeedBytes = 8 << 20
	// maxEntriesPerPoll caps how many new items one feed contributes per poll, so
	// a huge feed can't enqueue an unbounded number of enrichment jobs at once.
	maxEntriesPerPoll = 100
	// maxStatusLen keeps last_status short enough to display in the UI.
	maxStatusLen = 200
)

// Service turns feed subscriptions into saved items. It fetches feeds with an
// SSRF-safe client, parses them, and creates pending article items through the
// normal enrichment pipeline. It is the single place feed polling logic lives;
// the API handlers and the periodic poll job both drive it.
type Service struct {
	Store      *store.Store
	HTTPClient *http.Client
	// River enqueues enrichment jobs. It may be nil (e.g. before the worker
	// binary wires it in, or in tests): when nil, item creation still succeeds
	// and enqueue is skipped.
	River *river.Client[pgx.Tx]
}

// NewService builds a Service with an SSRF-safe HTTP client. Callers set River
// afterwards (the worker binary threads it in; the API leaves it nil-or-set as
// needed).
func NewService(s *store.Store) *Service {
	return &Service{Store: s, HTTPClient: enrich.SafeHTTPClient(fetchTimeout)}
}

// Add subscribes the user to feedURL: it validates the URL, fetches and parses
// the feed, backfills the feed's current entries as pending items, and only
// then persists the feed row. Backfilled items are standalone (deduped by URL,
// idempotent) and don't need the feed row to exist, so ordering backfill before
// persistence means a failure at any point before CreateFeed leaves no feed row
// behind — a retry starts clean rather than tripping ErrAlreadySubscribed on a
// feed that never backfilled. A fetch, parse, or backfill failure returns an
// error and the feed is NOT persisted. A URL the user is already subscribed to
// returns ErrAlreadySubscribed.
func (s *Service) Add(ctx context.Context, userID uuid.UUID, feedURL string) (db.Feed, int, error) {
	feedURL = strings.TrimSpace(feedURL)
	if !validFeedURL(feedURL) {
		return db.Feed{}, 0, fmt.Errorf("invalid feed url %q", feedURL)
	}

	existing, err := s.Store.Queries.ListFeeds(ctx, userID)
	if err != nil {
		return db.Feed{}, 0, fmt.Errorf("listing feeds: %w", err)
	}
	for _, f := range existing {
		if f.Url == feedURL {
			return db.Feed{}, 0, ErrAlreadySubscribed
		}
	}

	parsed, etag, lastModified, cacheMaxAge, err := s.fetchAndParse(ctx, feedURL, "", "")
	if err != nil {
		return db.Feed{}, 0, fmt.Errorf("fetching feed: %w", err)
	}

	added, ids, err := s.saveEntries(ctx, userID, nil, parsed.Entries)
	if err != nil {
		if len(ids) > 0 {
			slog.Warn("backfill failed partway through; already-created items are orphaned (no feed row was persisted)", "user_id", userID, "item_count", len(ids), "item_ids", ids)
		}
		return db.Feed{}, 0, fmt.Errorf("backfilling feed: %w", err)
	}

	feed, err := s.Store.Queries.CreateFeed(ctx, db.CreateFeedParams{
		UserID:  userID,
		Url:     feedURL,
		Title:   parsed.Title,
		SiteUrl: parsed.SiteURL,
	})
	if err != nil {
		return db.Feed{}, 0, fmt.Errorf("creating feed: %w", err)
	}

	// Backfilled items were created without provenance (see the ordering note
	// above); now that the feed row exists, adopt them onto it. A failure here
	// is logged and non-fatal: the items remain Mind-visible (feed_id stays
	// null) rather than being lost, and this batch is not retried.
	if len(ids) > 0 {
		if _, err := s.Store.Queries.AdoptFeedItems(ctx, db.AdoptFeedItemsParams{
			UserID:  userID,
			Column2: ids,
			FeedID:  pgtype.UUID{Bytes: feed.ID, Valid: true},
		}); err != nil {
			slog.Error("adopting backfilled items onto feed; items remain Mind-visible without feed provenance", "feed_id", feed.ID, "user_id", userID, "item_count", len(ids), "err", err)
		}
	}

	polled := nowTS()
	// Subscribing is a user signal of interest: always reset to the poll floor,
	// even when backfill added zero entries (e.g. an empty or fully-deduped
	// feed). Unlike Refresh, added==0 here must not double the interval.
	s.recordStatus(ctx, feed, "ok", etag, lastModified, nextPollInterval(pollFloor, true, cacheMaxAge))
	feed.LastPolledAt, feed.LastStatus = polled, "ok"
	feed.Etag, feed.LastModified = etag, lastModified
	return feed, added, nil
}

// Refresh re-polls one feed and saves any new entries. It never returns a fatal
// error for a bad feed: a fetch or parse failure is recorded in last_status
// ("error: …") and returns (0, nil) so a single broken feed can't break the
// poll loop.
func (s *Service) Refresh(ctx context.Context, feed db.Feed) (int, error) {
	current := time.Duration(feed.PollIntervalMinutes) * time.Minute
	parsed, etag, lastModified, cacheMaxAge, err := s.fetchAndParse(ctx, feed.Url, feed.Etag, feed.LastModified)
	if errors.Is(err, errNotModified) {
		// Healthy no-op: the server confirmed nothing changed, so skip parsing
		// and item creation entirely, keeping the stored validators.
		s.recordStatus(ctx, feed, "ok", feed.Etag, feed.LastModified, nextPollInterval(current, false, cacheMaxAge))
		return 0, nil
	}
	if err != nil {
		// Back off gently on a fetch/parse error — no cache hint is available,
		// and we deliberately don't reset to the floor, so a flaky feed doesn't
		// get hammered.
		s.recordStatus(ctx, feed, "error: "+shortErr(err), feed.Etag, feed.LastModified, nextPollInterval(current, false, 0))
		slog.Warn("feed refresh failed", "feed_id", feed.ID, "url", feed.Url, "err", err)
		return 0, nil
	}
	added, _, err := s.saveEntries(ctx, feed.UserID, &feed.ID, parsed.Entries)
	if err != nil {
		s.recordStatus(ctx, feed, "error: "+shortErr(err), feed.Etag, feed.LastModified, nextPollInterval(current, false, 0))
		slog.Warn("feed refresh failed", "feed_id", feed.ID, "url", feed.Url, "err", err)
		return 0, nil
	}
	s.recordStatus(ctx, feed, "ok", etag, lastModified, nextPollInterval(current, added > 0, cacheMaxAge))
	return added, nil
}

// RefreshDue refreshes every feed whose adaptive schedule has come due
// (next_poll_at <= now). It is the periodic poller's entry point and
// continues past an individual feed's error.
func (s *Service) RefreshDue(ctx context.Context) error {
	due, err := s.Store.Queries.ListFeedsDueForPoll(ctx)
	if err != nil {
		return fmt.Errorf("listing due feeds: %w", err)
	}
	for _, feed := range due {
		if _, err := s.Refresh(ctx, feed); err != nil {
			slog.Error("refreshing feed", "feed_id", feed.ID, "err", err)
		}
	}
	return nil
}

// saveEntries creates a pending article item for each entry URL that is not
// already an item for this user (and not repeated within the batch), enqueuing
// enrichment for each. Enqueue is best-effort — a failed enqueue is logged, not
// fatal, since the item is already saved and can be re-enriched later.
//
// feedID is nil for the pre-persist backfill in Add (the feed row doesn't
// exist yet, so items are created via CreateItem with no provenance) and
// non-nil for Refresh's polls (items are created via CreateFeedItem, stamped
// with the feed's id directly). It returns the count and ids of the items
// created, so a nil-feedID caller can adopt them onto the feed afterwards.
func (s *Service) saveEntries(ctx context.Context, userID uuid.UUID, feedID *uuid.UUID, entries []Entry) (int, []uuid.UUID, error) {
	existing, err := s.Store.Queries.ListItemURLs(ctx, userID)
	if err != nil {
		return 0, nil, fmt.Errorf("listing item urls: %w", err)
	}
	seen := make(map[string]bool, len(existing)+len(entries))
	for _, u := range existing {
		seen[u] = true
	}

	added := 0
	var ids []uuid.UUID
	for _, e := range entries {
		if added >= maxEntriesPerPoll {
			slog.Warn("feed entries truncated at cap", "cap", maxEntriesPerPoll, "user_id", userID)
			break
		}
		if e.URL == "" || seen[e.URL] {
			continue
		}
		seen[e.URL] = true

		var item db.Item
		if feedID != nil {
			item, err = s.Store.Queries.CreateFeedItem(ctx, db.CreateFeedItemParams{
				UserID: userID, Url: e.URL, FeedID: pgtype.UUID{Bytes: *feedID, Valid: true},
			})
		} else {
			item, err = s.Store.Queries.CreateItem(ctx, db.CreateItemParams{UserID: userID, Url: e.URL})
		}
		if err != nil {
			return added, ids, fmt.Errorf("creating item: %w", err)
		}
		if s.River != nil {
			if _, err := s.River.Insert(ctx, jobs.EnrichArgs{UserID: userID, ItemID: item.ID}, nil); err != nil {
				slog.Error("enqueueing enrichment for feed item", "item_id", item.ID, "err", err)
			}
		} else {
			slog.Debug("river client not set; skipping enrichment enqueue", "item_id", item.ID)
		}
		added++
		ids = append(ids, item.ID)
	}

	// feedID is nil for Add's pre-persist backfill (the user is subscribing
	// for the first time) and non-nil for Refresh's polls. Telling someone
	// "50 new items" the instant they subscribe would be the single most
	// annoying thing this feature could do, so only a poll on an existing
	// subscription emits.
	//
	// One row per feed per hour: the flush job's Coalesce sums these into a
	// single "N new items across M feeds" message. A single row per hour
	// would be collapsed by the pending dedupe index and could never carry a
	// count, so Coalesce would have nothing to sum.
	if feedID != nil && added > 0 {
		dedupe := fmt.Sprintf("feed_river:%s:%s", *feedID, time.Now().UTC().Format("2006-01-02T15"))
		if err := jobs.EnqueueNotification(ctx, s.Store, userID, notify.CategoryFeedRiver, dedupe,
			fmt.Sprintf("%d new items", added), "",
			map[string]any{"feed_id": feedID.String(), "count": added}); err != nil {
			slog.Error("saveEntries: enqueueing feed river notification", "feed_id", *feedID, "err", err)
		}
	}
	return added, ids, nil
}

// fetchAndParse fetches feedURL with the SSRF-safe client and parses the body.
// When etag/lastModified are non-empty they are sent as If-None-Match /
// If-Modified-Since; a 304 returns errNotModified without reading the body. On
// success it also returns the response's validators (empty when absent) and
// cacheMaxAge, the origin's remaining Cache-Control freshness lifetime (zero
// when absent), which is a hard lower bound on the next poll interval.
func (s *Service) fetchAndParse(ctx context.Context, feedURL, etag, lastModified string) (Feed, string, string, time.Duration, error) {
	client := s.HTTPClient
	if client == nil {
		client = enrich.SafeHTTPClient(fetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return Feed{}, "", "", 0, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "openmind-feeds/1.0")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Feed{}, "", "", 0, fmt.Errorf("requesting feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return Feed{}, "", "", parseCacheControlMaxAge(resp.Header.Get("Cache-Control"), resp.Header.Get("Age")), errNotModified
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Feed{}, "", "", 0, fmt.Errorf("feed returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return Feed{}, "", "", 0, fmt.Errorf("reading feed body: %w", err)
	}
	parsed, err := Parse(data, feedURL)
	if err != nil {
		return Feed{}, "", "", 0, fmt.Errorf("parsing feed: %w", err)
	}
	return parsed, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"),
		parseCacheControlMaxAge(resp.Header.Get("Cache-Control"), resp.Header.Get("Age")), nil
}

// recordStatus stamps last_polled_at (now), last_status, the conditional-GET
// validators, and the adaptive schedule (next_poll_at, poll_interval_minutes,
// derived from nextInterval) on a feed. A write failure is logged, not
// returned — the poll loop must keep going.
func (s *Service) recordStatus(ctx context.Context, feed db.Feed, status, etag, lastModified string, nextInterval time.Duration) {
	if err := s.Store.Queries.SetFeedPolled(ctx, db.SetFeedPolledParams{
		UserID: feed.UserID, ID: feed.ID, LastPolledAt: nowTS(), LastStatus: status,
		Etag: etag, LastModified: lastModified,
		NextPollAt:          pgtype.Timestamptz{Time: time.Now().Add(nextInterval), Valid: true},
		PollIntervalMinutes: int32(nextInterval / time.Minute),
	}); err != nil {
		slog.Error("recording feed poll status", "feed_id", feed.ID, "err", err)
	}
}

// validFeedURL accepts only absolute http(s) URLs, mirroring the API's URL check.
func validFeedURL(raw string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// shortErr trims an error message so it fits in last_status.
func shortErr(err error) string {
	msg := err.Error()
	if len(msg) > maxStatusLen {
		msg = msg[:maxStatusLen]
	}
	return msg
}

func nowTS() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now(), Valid: true}
}
