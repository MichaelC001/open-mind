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
