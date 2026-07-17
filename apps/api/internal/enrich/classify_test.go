package enrich

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name, url string
		want      string
	}{
		{"youtube", "https://www.youtube.com/watch?v=abc", "video"},
		{"tweet", "https://x.com/user/status/123", "tweet"},
		{"twitter", "https://twitter.com/user/status/123", "tweet"},
		{"image ext", "https://cdn.site.com/photo.jpg", "image"},
		{"instagram reel", "https://www.instagram.com/reel/Cabc123/", "video"},
		{"instagram short link", "https://instagr.am/p/Cabc123/", "video"},
		{"tiktok", "https://www.tiktok.com/@user/video/123", "video"},
		{"tiktok share link", "https://vm.tiktok.com/ZM123/", "video"},
		{"default article", "https://blog.example.com/post", "article"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.url, Extraction{}); got != tt.want {
				t.Errorf("Classify(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
