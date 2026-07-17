package enrich

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsSocialVideoURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.instagram.com/reel/Cabc123/", true},
		{"https://instagram.com/p/Cabc123/", true},
		{"https://www.tiktok.com/@user/video/123", true},
		{"https://vm.tiktok.com/ZM123/", true},
		{"https://vt.tiktok.com/ZM123/", true},
		{"https://www.youtube.com/watch?v=abc", false},
		{"https://blog.example.com/instagram.com", false},
		{"://bad", false},
	}
	for _, tt := range tests {
		if got := IsSocialVideoURL(tt.url); got != tt.want {
			t.Errorf("IsSocialVideoURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestParseOpenGraph(t *testing.T) {
	tests := []struct {
		name string
		html string
		want Extraction
	}{
		{
			name: "full tags",
			html: `<html><head>
				<meta property="og:title" content="chef.eats on Instagram"/>
				<meta property="og:description" content="3 cafes in Lisbon you must try 📍"/>
				<meta property="og:image" content="https://cdn.example.com/thumb.jpg"/>
			</head><body>login wall</body></html>`,
			want: Extraction{
				Title:        "chef.eats on Instagram",
				Body:         "3 cafes in Lisbon you must try 📍",
				LeadImageURL: "https://cdn.example.com/thumb.jpg",
			},
		},
		{
			name: "missing description",
			html: `<head><meta property="og:title" content="a reel"/></head><body></body>`,
			want: Extraction{Title: "a reel"},
		},
		{
			name: "login wall without og tags",
			html: `<html><head><title>Login</title></head><body>Log in to continue</body></html>`,
			want: Extraction{},
		},
		{
			name: "first tag wins",
			html: `<head>
				<meta property="og:title" content="first"/>
				<meta property="og:title" content="second"/>
			</head>`,
			want: Extraction{Title: "first"},
		},
		{
			name: "meta after body start is ignored",
			html: `<head></head><body><meta property="og:title" content="late"/></body>`,
			want: Extraction{},
		},
		{
			name: "not html",
			html: `just plain text, no tags at all`,
			want: Extraction{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOpenGraph(strings.NewReader(tt.html))
			if err != nil {
				t.Fatalf("parseOpenGraph: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseOpenGraph = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtractOpenGraph(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reel":
			_, _ = w.Write([]byte(`<head><meta property="og:title" content="t"/><meta property="og:description" content="d"/></head>`))
		case "/gone":
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	got, err := extractOpenGraph(context.Background(), srv.Client(), srv.URL+"/reel")
	if err != nil {
		t.Fatalf("extractOpenGraph: %v", err)
	}
	if got.Title != "t" || got.Body != "d" {
		t.Errorf("extractOpenGraph = %+v", got)
	}

	if _, err := extractOpenGraph(context.Background(), srv.Client(), srv.URL+"/gone"); err == nil {
		t.Error("expected error for 404 response")
	}
}
