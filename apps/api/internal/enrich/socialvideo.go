package enrich

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// socialVideoHosts maps the hosts of caption-driven social-video platforms
// (reels, TikToks) to a human label used for the degraded card title. These
// pages are JS-rendered and often login-walled, so the generic article
// extractor is useless on them — but their server-rendered HTML carries the
// caption in OpenGraph meta tags, which is where place extraction reads from.
var socialVideoHosts = map[string]string{
	"instagram.com": "Instagram reel",
	"instagr.am":    "Instagram reel",
	"tiktok.com":    "TikTok video",
	"vm.tiktok.com": "TikTok video",
	"vt.tiktok.com": "TikTok video",
}

// IsSocialVideoURL reports whether the URL belongs to a social-video platform
// whose items go through the OG extractor and qualify for place extraction.
func IsSocialVideoURL(rawURL string) bool {
	_, ok := socialVideoLabel(rawURL)
	return ok
}

// socialVideoLabel returns the degraded-card label for a social-video URL.
func socialVideoLabel(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	label, ok := socialVideoHosts[host]
	return label, ok
}

// extractOpenGraph fetches a page and returns its og:title, og:description,
// and og:image. Social-video pages serve these to anonymous agents even when
// the content itself sits behind a login wall; og:description carries the
// caption, which becomes the item body (searchable, summarisable, and the
// input to place extraction).
func extractOpenGraph(ctx context.Context, client *http.Client, rawURL string) (Extraction, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Extraction{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", "openmind/0.1 (+https://github.com/rohithgilla12/open-mind)")
	resp, err := client.Do(req)
	if err != nil {
		return Extraction{}, fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return Extraction{}, fmt.Errorf("fetching %s: status %d", rawURL, resp.StatusCode)
	}
	og, err := parseOpenGraph(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Extraction{}, fmt.Errorf("parsing %s: %w", rawURL, err)
	}
	return og, nil
}

// parseOpenGraph tokenises HTML and collects the first og:title,
// og:description, and og:image meta tags. Tokenising (rather than building a
// full tree) keeps memory flat on large pages, and meta tags live in <head>,
// so the scan usually ends early.
func parseOpenGraph(r io.Reader) (Extraction, error) {
	var ex Extraction
	tok := html.NewTokenizer(r)
	for {
		switch tok.Next() {
		case html.ErrorToken:
			if tok.Err() == io.EOF {
				return ex, nil
			}
			return ex, tok.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tok.TagName()
			switch string(name) {
			case "meta":
				var property, content string
				for hasAttr {
					var key, val []byte
					key, val, hasAttr = tok.TagAttr()
					switch string(key) {
					case "property", "name":
						property = string(val)
					case "content":
						content = string(val)
					}
				}
				switch property {
				case "og:title":
					if ex.Title == "" {
						ex.Title = content
					}
				case "og:description":
					if ex.Body == "" {
						ex.Body = content
					}
				case "og:image":
					if ex.LeadImageURL == "" {
						ex.LeadImageURL = content
					}
				}
			case "body":
				// Meta tags live in <head>; once the body starts there is
				// nothing left to find.
				return ex, nil
			}
		}
		if ex.Title != "" && ex.Body != "" && ex.LeadImageURL != "" {
			return ex, nil
		}
	}
}

// runSocialVideo enriches a social-video item (Instagram reel, TikTok): OG
// meta tags instead of article extraction, always classified as a video card.
// A fetch/parse failure or a login-wall page with no tags degrades to a bare
// card named after the platform — never a failed item, so a re-run (or a
// later phase with better signals) can only improve it.
func (p *Pipeline) runSocialVideo(ctx context.Context, userID uuid.UUID, item db.Item) error {
	q := p.Store.Queries
	label, _ := socialVideoLabel(item.Url)

	ex, err := extractOpenGraph(ctx, p.httpClient(), item.Url)
	if err != nil {
		slog.Warn("social video: og extraction failed, degrading to bare card", "item_id", item.ID, "err", err)
		ex = Extraction{}
	}
	if ex.Title == "" {
		ex.Title = label
	}

	if err := q.UpdateItemExtraction(ctx, db.UpdateItemExtractionParams{
		UserID: userID, ID: item.ID,
		Title: ex.Title, Body: ex.Body, LeadImageUrl: ex.LeadImageURL, CardType: "video",
	}); err != nil {
		return fmt.Errorf("saving social video extraction: %w", err)
	}
	return p.enrichText(ctx, userID, item.ID, ex.Title, ex.Body)
}
