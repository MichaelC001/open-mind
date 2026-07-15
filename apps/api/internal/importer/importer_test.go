package importer

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func urls(links []Link) []string {
	out := make([]string, len(links))
	for i, l := range links {
		out[i] = l.URL
	}
	return out
}

func TestParseNetscapeHTML(t *testing.T) {
	data := []byte(`<!DOCTYPE NETSCAPE-Bookmark-file-1>
<DL><p>
  <DT><A HREF="https://example.com/a" ADD_DATE="1700000000" TAGS="go, rust">First &amp; foremost</A>
  <DT><A TAGS="reading" HREF="https://example.com/b">Second</A>
  <DT><A HREF="https://example.com/c">No tags</A>
  <DT><A HREF="">empty href skipped</A>
</DL>`)
	links, err := Parse("pocket_export.html", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := urls(links); len(got) != 3 || got[0] != "https://example.com/a" || got[1] != "https://example.com/b" || got[2] != "https://example.com/c" {
		t.Fatalf("urls = %v", got)
	}
	if links[0].Title != "First & foremost" {
		t.Errorf("title = %q, want entity-decoded", links[0].Title)
	}
	if got := links[0].Tags; !reflect.DeepEqual(got, []string{"go", "rust"}) {
		t.Errorf("tags[0] = %v, want [go rust]", got)
	}
	// TAGS before HREF must still parse (attribute order is irrelevant).
	if got := links[1].Tags; !reflect.DeepEqual(got, []string{"reading"}) {
		t.Errorf("tags[1] = %v, want [reading]", got)
	}
	// No TAGS attribute → nil.
	if links[2].Tags != nil {
		t.Errorf("tags[2] = %v, want nil", links[2].Tags)
	}
}

func TestParseCSV(t *testing.T) {
	data := []byte("title,url,time_added,tags\n" +
		"Hello World,https://example.com/x,1700000000,go|rust\n" +
		"No URL row,,123,\n" +
		"Another,https://example.com/y,124,\n")
	links, err := Parse("pocket.csv", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := urls(links)
	if len(got) != 2 || got[0] != "https://example.com/x" || got[1] != "https://example.com/y" {
		t.Fatalf("urls = %v", got)
	}
	if links[0].Title != "Hello World" {
		t.Errorf("title = %q", links[0].Title)
	}
	if got := links[0].Tags; !reflect.DeepEqual(got, []string{"go", "rust"}) {
		t.Errorf("tags[0] = %v, want [go rust]", got)
	}
	// Row with an empty tags cell → nil.
	if links[1].Tags != nil {
		t.Errorf("tags[1] = %v, want nil", links[1].Tags)
	}
}

func TestParseCSVRaindropSpaceSeparatedTags(t *testing.T) {
	// Raindrop separates tags with spaces (no comma in the cell).
	data := []byte("url,title,tags\nhttps://example.com/r,Read this,go rust web\n")
	links, err := Parse("raindrop.csv", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1", len(links))
	}
	if got := links[0].Tags; !reflect.DeepEqual(got, []string{"go", "rust", "web"}) {
		t.Errorf("tags = %v, want [go rust web]", got)
	}
}

func TestParseCSVDetectedByContent(t *testing.T) {
	// No .csv extension, but a header naming a URL column.
	data := []byte("id,URL,folder\n1,https://example.com/z,inbox\n")
	links, err := Parse("export.txt", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := urls(links); len(got) != 1 || got[0] != "https://example.com/z" {
		t.Fatalf("urls = %v", got)
	}
	// No tags column → nil.
	if links[0].Tags != nil {
		t.Errorf("tags = %v, want nil", links[0].Tags)
	}
}

func TestParsePlainTextURLs(t *testing.T) {
	data := []byte("https://example.com/1\n\n  https://example.com/2  \nnot a url\nftp://skip.me\n")
	links, err := Parse("urls.txt", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := urls(links)
	if len(got) != 2 || got[0] != "https://example.com/1" || got[1] != "https://example.com/2" {
		t.Fatalf("urls = %v", got)
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse("empty.txt", []byte("nothing here\njust prose\n")); err != ErrEmpty {
		t.Errorf("err = %v, want ErrEmpty", err)
	}
}

// omnivoreZip builds an in-memory zip from name→content pairs, in order.
func omnivoreZip(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e[0])
		if err != nil {
			t.Fatalf("zip create %s: %v", e[0], err)
		}
		if _, err := f.Write([]byte(e[1])); err != nil {
			t.Fatalf("zip write %s: %v", e[0], err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestParseOmnivoreZip(t *testing.T) {
	data := omnivoreZip(t, [][2]string{
		{"metadata_0_to_1.json", `[
			{"url":"https://example.com/a","title":"A","labels":["go","reading"],"state":"SUCCEEDED"},
			{"url":"https://example.com/deleted","title":"Gone","state":"DELETED"},
			{"url":"","title":"No URL","state":"SUCCEEDED"}
		]`},
		{"content/a.html", "<html>ignored</html>"},
		{"highlights/a.md", "> ignored"},
		{"metadata_2_to_3.json", `[
			{"url":"https://example.com/b","title":"B","labels":[{"name":"web"}]},
			{"url":"https://example.com/c","title":"C"}
		]`},
	})
	links, err := Parse("omnivore-export.zip", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := urls(links); len(got) != 3 || got[0] != "https://example.com/a" || got[1] != "https://example.com/b" || got[2] != "https://example.com/c" {
		t.Fatalf("urls = %v", got)
	}
	if links[0].Title != "A" {
		t.Errorf("title = %q", links[0].Title)
	}
	if got := links[0].Tags; !reflect.DeepEqual(got, []string{"go", "reading"}) {
		t.Errorf("tags[0] = %v, want [go reading]", got)
	}
	// Object-shaped labels ({"name": ...}) also work.
	if got := links[1].Tags; !reflect.DeepEqual(got, []string{"web"}) {
		t.Errorf("tags[1] = %v, want [web]", got)
	}
	// No labels → nil.
	if links[2].Tags != nil {
		t.Errorf("tags[2] = %v, want nil", links[2].Tags)
	}
}

func TestParseOmnivoreZipDetectedByMagic(t *testing.T) {
	// No .zip extension; the PK magic alone must route to the zip parser.
	data := omnivoreZip(t, [][2]string{
		{"metadata_0_to_0.json", `[{"url":"https://example.com/m"}]`},
	})
	links, err := Parse("export", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := urls(links); len(got) != 1 || got[0] != "https://example.com/m" {
		t.Fatalf("urls = %v", got)
	}
}

func TestParseOmnivoreZipMalformedEntrySkipped(t *testing.T) {
	data := omnivoreZip(t, [][2]string{
		{"metadata_0_to_0.json", `{not json`},
		{"metadata_1_to_1.json", `[{"url":"https://example.com/ok"}]`},
	})
	links, err := Parse("omnivore.zip", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := urls(links); len(got) != 1 || got[0] != "https://example.com/ok" {
		t.Fatalf("urls = %v", got)
	}
}

func TestParseOmnivoreZipEmpty(t *testing.T) {
	// A zip with no metadata files is not an Omnivore export → ErrEmpty.
	data := omnivoreZip(t, [][2]string{{"readme.txt", "hello"}})
	if _, err := Parse("archive.zip", data); err != ErrEmpty {
		t.Errorf("err = %v, want ErrEmpty", err)
	}
}

func TestParseOmnivoreZipOversizeEntrySkipped(t *testing.T) {
	big := `[{"url":"https://example.com/big","title":"` + strings.Repeat("x", omnivoreMaxEntryBytes) + `"}]`
	data := omnivoreZip(t, [][2]string{
		{"metadata_0_to_0.json", big},
		{"metadata_1_to_1.json", `[{"url":"https://example.com/small"}]`},
	})
	links, err := Parse("omnivore.zip", data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := urls(links); len(got) != 1 || got[0] != "https://example.com/small" {
		t.Fatalf("urls = %v", got)
	}
}
