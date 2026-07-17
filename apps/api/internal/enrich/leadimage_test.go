package enrich

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestPickLeadImageURLPrefersMeta(t *testing.T) {
	page, _ := url.Parse("https://example.com/post")
	node := mustParseHTML(t, `<div><img src="https://cdn.example.com/body.jpg"></div>`)
	got := pickLeadImageURL("https://cdn.example.com/og.jpg", node, page)
	if got != "https://cdn.example.com/og.jpg" {
		t.Errorf("got %q, want meta image", got)
	}
}

func TestPickLeadImageURLFallsBackToBodyImg(t *testing.T) {
	page, _ := url.Parse("https://example.com/post")
	node := mustParseHTML(t, `<div><p>hi</p><img alt="shot" src="https://static.example.com/shot.webp"></div>`)
	got := pickLeadImageURL("", node, page)
	if got != "https://static.example.com/shot.webp" {
		t.Errorf("got %q", got)
	}
}

func TestFirstContentImageURLResolvesRelative(t *testing.T) {
	page, _ := url.Parse("https://example.com/blog/post/")
	node := mustParseHTML(t, `<article><img src="../img/cover.png"></article>`)
	got := firstContentImageURL(node, page)
	if got != "https://example.com/blog/img/cover.png" {
		t.Errorf("got %q", got)
	}
}

func TestFirstContentImageURLSkipsTrackingPixel(t *testing.T) {
	page, _ := url.Parse("https://example.com/")
	node := mustParseHTML(t, `<div>
		<img src="https://tracker.example/pixel.gif" width="1" height="1">
		<img src="https://cdn.example.com/hero.jpg">
	</div>`)
	got := firstContentImageURL(node, page)
	if got != "https://cdn.example.com/hero.jpg" {
		t.Errorf("got %q", got)
	}
}

func TestFirstContentImageURLUsesDataSrcAndSrcset(t *testing.T) {
	page, _ := url.Parse("https://example.com/")
	node := mustParseHTML(t, `<div><img data-src="https://cdn.example.com/lazy.jpg"></div>`)
	if got := firstContentImageURL(node, page); got != "https://cdn.example.com/lazy.jpg" {
		t.Errorf("data-src: got %q", got)
	}
	node = mustParseHTML(t, `<div><img srcset="https://cdn.example.com/a.jpg 1x, https://cdn.example.com/a@2x.jpg 2x"></div>`)
	if got := firstContentImageURL(node, page); got != "https://cdn.example.com/a.jpg" {
		t.Errorf("srcset: got %q", got)
	}
}

func TestFirstContentImageURLSkipsDataURI(t *testing.T) {
	page, _ := url.Parse("https://example.com/")
	node := mustParseHTML(t, `<div>
		<img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7">
		<img src="https://cdn.example.com/real.png">
	</div>`)
	got := firstContentImageURL(node, page)
	if got != "https://cdn.example.com/real.png" {
		t.Errorf("got %q", got)
	}
}

func mustParseHTML(t *testing.T, fragment string) *html.Node {
	t.Helper()
	roots, err := html.ParseFragment(strings.NewReader(fragment), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Wrap siblings in a synthetic root so document-order walks see them all.
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range roots {
		root.AppendChild(n)
	}
	return root
}
