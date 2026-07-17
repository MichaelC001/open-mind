package enrich

import (
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// pickLeadImageURL prefers an Open Graph / metadata image when present,
// otherwise falls back to the first usable <img> in the extracted article
// body. Many blogs (e.g. simonwillison.net) ship in-article screenshots
// without og:image — without this fallback cards get no lead image.
func pickLeadImageURL(metaImage string, contentNode *html.Node, pageURL *url.URL) string {
	if metaImage != "" {
		return metaImage
	}
	return firstContentImageURL(contentNode, pageURL)
}

// firstContentImageURL walks the extracted content tree in document order and
// returns the first absolute http(s) image URL that does not look like a
// tracking pixel or spacer.
func firstContentImageURL(n *html.Node, pageURL *url.URL) string {
	var found string
	walkHTML(n, func(node *html.Node) bool {
		if node.Type != html.ElementNode || node.Data != "img" {
			return true
		}
		if isLikelyTrackingPixel(node) {
			return true
		}
		src := imgCandidateSrc(node)
		if abs := absoluteImageURL(pageURL, src); abs != "" {
			found = abs
			return false
		}
		return true
	})
	return found
}

func walkHTML(n *html.Node, fn func(*html.Node) bool) bool {
	if n == nil {
		return true
	}
	if !fn(n) {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if !walkHTML(c, fn) {
			return false
		}
	}
	return true
}

func imgCandidateSrc(img *html.Node) string {
	for _, key := range []string{"src", "data-src"} {
		if v := strings.TrimSpace(attr(img, key)); v != "" {
			return v
		}
	}
	// Lazy-loaded images sometimes only publish candidates in srcset.
	if srcset := strings.TrimSpace(attr(img, "srcset")); srcset != "" {
		return firstSrcsetURL(srcset)
	}
	if srcset := strings.TrimSpace(attr(img, "data-srcset")); srcset != "" {
		return firstSrcsetURL(srcset)
	}
	return ""
}

func firstSrcsetURL(srcset string) string {
	// srcset: "url1 1x, url2 2x" or "url1 320w, url2 640w"
	for _, part := range strings.Split(srcset, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func absoluteImageURL(page *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "data:") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if page != nil {
		u = page.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	return u.String()
}

func isLikelyTrackingPixel(img *html.Node) bool {
	w := strings.TrimSpace(attr(img, "width"))
	h := strings.TrimSpace(attr(img, "height"))
	if isOneOrZero(w) && isOneOrZero(h) {
		return true
	}
	src := strings.ToLower(attr(img, "src") + " " + attr(img, "data-src"))
	for _, needle := range []string{"1x1", "pixel.", "/pixel", "spacer.", "blank.gif", "tracking."} {
		if strings.Contains(src, needle) {
			return true
		}
	}
	return false
}

func isOneOrZero(s string) bool {
	if s == "" {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n <= 1
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
