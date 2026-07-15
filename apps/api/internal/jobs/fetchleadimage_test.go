package jobs

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// onePxPNG is a valid 1x1 transparent PNG, used to exercise the content-type
// sniffing path of fetchLeadImage.
var onePxPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestFetchLeadImage(t *testing.T) {
	t.Run("png ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(onePxPNG)
		}))
		defer srv.Close()

		data, contentType := fetchLeadImage(context.Background(), srv.Client(), srv.URL)
		if contentType != "image/png" {
			t.Fatalf("content type = %q, want image/png", contentType)
		}
		if !bytes.Equal(data, onePxPNG) {
			t.Fatalf("data mismatch")
		}
	})

	t.Run("404 nil", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		data, contentType := fetchLeadImage(context.Background(), srv.Client(), srv.URL)
		if data != nil || contentType != "" {
			t.Fatalf("data = %v, contentType = %q, want nil, \"\"", data, contentType)
		}
	})

	t.Run("over 5MB nil", func(t *testing.T) {
		big := bytes.Repeat([]byte{0xff}, 5<<20+10)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(big)
		}))
		defer srv.Close()

		data, contentType := fetchLeadImage(context.Background(), srv.Client(), srv.URL)
		if data != nil || contentType != "" {
			t.Fatalf("data len = %d, contentType = %q, want nil, \"\"", len(data), contentType)
		}
	})

	t.Run("text/html nil", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
		}))
		defer srv.Close()

		data, contentType := fetchLeadImage(context.Background(), srv.Client(), srv.URL)
		if data != nil || contentType != "" {
			t.Fatalf("data = %v, contentType = %q, want nil, \"\"", data, contentType)
		}
	})
}
