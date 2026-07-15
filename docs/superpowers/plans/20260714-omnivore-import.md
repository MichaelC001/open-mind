# Omnivore Zip Import (Slice A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `POST /import` accepts an Omnivore export zip; every saved page becomes a pending item with Omnivore labels preserved as user tags.

**Architecture:** All logic lands in the pure parser package `apps/api/internal/importer` — `Parse` gains zip detection and a `parseOmnivoreZip` reader over the in-memory bytes. The handler, contract, and store are untouched (the existing `/import` path already dedupes, canonicalises tags, and enqueues enrichment). Web/doc changes are copy-only.

**Tech Stack:** Go stdlib only (`archive/zip`, `encoding/json`). No new dependencies.

**Spec:** `docs/superpowers/specs/20260714-omnivore-import-design.md`

## Global Constraints

- Go stdlib only in the importer; no new go.mod entries.
- Parsing is pure: no network, no AI, no DB.
- Caps: skip any zip entry with uncompressed size > 8 MB (`omnivoreMaxEntryBytes`); stop after 10 000 links (`omnivoreMaxLinks`).
- Skip elements with `state == "DELETED"` or empty `url`; malformed JSON in one metadata entry skips only that entry.
- `labels` accepts both `["a","b"]` and `[{"name":"a"}]` shapes.
- Empty/non-Omnivore zip → existing `ErrEmpty`.
- Go commands run from `apps/api`.

---

### Task 1: Omnivore zip parsing in `internal/importer`

**Files:**
- Modify: `apps/api/internal/importer/importer.go`
- Test: `apps/api/internal/importer/importer_test.go`

**Interfaces:**
- Consumes: existing `Link`, `ErrEmpty`, `Parse(filename string, data []byte) ([]Link, error)`.
- Produces: `Parse` now recognises zips; new unexported `parseOmnivoreZip(data []byte) []Link`. No exported API change.

- [ ] **Step 1: Write the failing tests**

Append to `apps/api/internal/importer/importer_test.go`:

```go
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
```

Also extend the test file's import block to:

```go
import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `apps/api`): `go test ./internal/importer/ -run TestParseOmnivore -v`
Expected: compile FAIL (`undefined: omnivoreMaxEntryBytes`) — that counts; the symbol and behaviour don't exist yet.

- [ ] **Step 3: Implement zip parsing**

In `apps/api/internal/importer/importer.go`:

Extend the package imports:

```go
import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"html"
	"io"
	"path"
	"regexp"
	"strings"
)
```

Add below the existing `urlLineRe` declaration:

```go
// zipMagic is the zip local-file-header signature; Omnivore exports are zips.
var zipMagic = []byte("PK\x03\x04")

const (
	// omnivoreMaxEntryBytes skips any single zip entry larger than this when
	// decompressed — metadata pages are well under 1 MB, so anything bigger is
	// not a metadata file we want in memory.
	omnivoreMaxEntryBytes = 8 << 20
	// omnivoreMaxLinks stops parsing once this many links are collected,
	// mirroring the API handler's per-import cap.
	omnivoreMaxLinks = 10000
)
```

Change the detection `switch` in `Parse` to check zip first:

```go
	var links []Link
	switch {
	case strings.HasSuffix(name, ".zip") || bytes.HasPrefix(data, zipMagic):
		links = parseOmnivoreZip(data)
	case looksHTML:
		links = parseHTML(data)
	case strings.HasSuffix(name, ".csv") || looksCSV(data):
		links = parseCSV(data)
	default:
		links = parseText(data)
	}
```

(Keep the `looksHTML :=` computation as-is above the switch.)

Add at the end of the file:

```go
// omnivoreLabel is one entry of an Omnivore metadata "labels" array. The
// export writes plain strings, but Omnivore's API shape used {"name": ...}
// objects, so both are accepted.
type omnivoreLabel struct {
	Name string
}

func (l *omnivoreLabel) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		l.Name = s
		return nil
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	l.Name = obj.Name
	return nil
}

// omnivoreEntry is one saved page in an Omnivore metadata_*.json array.
type omnivoreEntry struct {
	URL    string          `json:"url"`
	Title  string          `json:"title"`
	State  string          `json:"state"`
	Labels []omnivoreLabel `json:"labels"`
}

// parseOmnivoreZip reads an Omnivore export zip, collecting links from every
// metadata_*.json entry. content/ and highlights/ entries are ignored (the
// archived bodies are a future slice). Malformed or oversized entries are
// skipped rather than failing the whole import.
func parseOmnivoreZip(data []byte) []Link {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil
	}
	var out []Link
	for _, f := range zr.File {
		base := strings.ToLower(path.Base(f.Name))
		if !strings.HasPrefix(base, "metadata_") || !strings.HasSuffix(base, ".json") {
			continue
		}
		if f.UncompressedSize64 > omnivoreMaxEntryBytes {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(rc, omnivoreMaxEntryBytes+1))
		_ = rc.Close()
		if err != nil || len(raw) > omnivoreMaxEntryBytes {
			continue
		}
		var entries []omnivoreEntry
		if err := json.Unmarshal(raw, &entries); err != nil {
			continue
		}
		for _, e := range entries {
			if e.URL == "" || e.State == "DELETED" {
				continue
			}
			var tags []string
			for _, l := range e.Labels {
				if name := strings.TrimSpace(l.Name); name != "" {
					tags = append(tags, name)
				}
			}
			out = append(out, Link{URL: e.URL, Title: strings.TrimSpace(e.Title), Tags: tags})
			if len(out) >= omnivoreMaxLinks {
				return out
			}
		}
	}
	return out
}
```

Update the package doc comment's first paragraph to mention the new format (append to the format list sentence): `..., a plain newline-delimited URL list, and Omnivore export zips (metadata_*.json pages; labels become tags).`

- [ ] **Step 4: Run the full importer test suite**

Run (from `apps/api`): `go test ./internal/importer/ -v`
Expected: all tests PASS, including the pre-existing HTML/CSV/text tests (regression check for the detection-order change).

- [ ] **Step 5: Vet and build**

Run (from `apps/api`): `go vet ./... && go build ./...`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add apps/api/internal/importer/importer.go apps/api/internal/importer/importer_test.go
git commit -m "feat(import): parse Omnivore export zips (metadata pages, labels as tags)"
```

---

### Task 2: Web copy + docs

**Files:**
- Modify: `apps/web/app/import/page.tsx` (SOURCES list, line 8)
- Modify: `apps/web/components/ImportForm.tsx:77` (file input `accept`)
- Modify: `docs/self-hosting.md` (new Importing section before `## Exporting your library`, line 171)

**Interfaces:**
- Consumes: nothing from Task 1 at the code level (copy-only).
- Produces: nothing consumed later.

- [ ] **Step 1: Add Omnivore to the supported-sources list**

In `apps/web/app/import/page.tsx`, insert into `SOURCES` after the Pinboard line:

```ts
  "Omnivore — zip export (labels become your tags; archived article bodies aren't used yet, so dead links import as failed cards)",
```

- [ ] **Step 2: Accept .zip in the file picker**

In `apps/web/components/ImportForm.tsx` line 77, change:

```tsx
accept=".html,.htm,.csv,.txt,.zip,text/html,text/csv,text/plain,application/zip"
```

- [ ] **Step 3: Docs**

In `docs/self-hosting.md`, insert before the `## Exporting your library` heading:

```markdown
## Importing

`POST /import` (or the web app's **Import** page) bulk-imports an export file: Netscape bookmark HTML (browsers, Pocket, Raindrop, Pinboard, Instapaper), CSV with a URL column, a plain list of URLs, or an **Omnivore zip export**. Every new URL becomes a pending item and enriches asynchronously; URLs already in your library are skipped, so re-importing is safe.

**Omnivore**: labels are preserved as your tags. Only the saved URLs are imported in the current version — the archived article bodies in the zip are not used yet, so pages whose original URL has since died will import as failed cards.
```

- [ ] **Step 4: Typecheck and build web**

Run: `pnpm turbo run build --filter=web`
Expected: build succeeds.

- [ ] **Step 5: Commit**

```bash
git add apps/web/app/import/page.tsx apps/web/components/ImportForm.tsx docs/self-hosting.md
git commit -m "feat(web,docs): Omnivore zip in import sources, file accept, self-hosting docs"
```

---

### Task 3: E2e verification against the compose stack

**Files:** none created (verification only; TODO.md updated at the end).

- [ ] **Step 1: Bring up the stack**

Ask the user before starting anything — they usually have an instance running. If none is up: `docker compose up -d --build api web`.

- [ ] **Step 2: Craft an Omnivore zip and import it**

From the repo root (use python3 + urllib directly — do NOT trust curl output in this environment; a shell hook has previously fabricated response bodies):

```bash
python3 - <<'EOF'
import io, json, zipfile, urllib.request, uuid
buf = io.BytesIO()
with zipfile.ZipFile(buf, "w") as z:
    z.writestr("metadata_0_to_2.json", json.dumps([
        {"url": "https://danluu.com/why-benchmark/", "title": "Why benchmark", "labels": ["perf", "reading"], "state": "SUCCEEDED"},
        {"url": "https://example.com/deleted", "state": "DELETED"},
        {"url": "https://example.org/omnivore-e2e-" + uuid.uuid4().hex[:8], "labels": [{"name": "web"}]},
    ]))
    z.writestr("content/a.html", "<html>ignored</html>")
data = buf.getvalue()
boundary = "omnivoreboundary"
body = (
    f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"omnivore-export.zip\"\r\n"
    "Content-Type: application/zip\r\n\r\n"
).encode() + data + f"\r\n--{boundary}--\r\n".encode()
req = urllib.request.Request("http://localhost:8080/import", data=body, method="POST",
    headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
print(urllib.request.urlopen(req).read().decode())
EOF
```

Expected: `{"total":3,"imported":2,"skipped":...,"failed":...}` — 2 imported (the DELETED entry is dropped at parse time so total is per parsed links, i.e. total 2, imported 2 on a fresh library; if danluu is already saved, imported 1 / skipped 1).

- [ ] **Step 3: Verify tags landed**

`GET http://localhost:8080/items?limit=5` (python3 urllib, Bearer if the instance has a token) — the imported items carry `userTags` `["perf","reading"]` and `["web"]`.

- [ ] **Step 4: Verify idempotency**

Re-run the Step 2 script body with the SAME urls (fix the uuid to the value from the first run, or re-import the first zip file saved to disk). Expected: `imported: 0`, all skipped.

- [ ] **Step 5: Update TODO.md**

Move the Omnivore line from Next to a Done entry under "Milestone 2 — imports" with a dated summary (what shipped, slice-B follow-up stays in Next as "Omnivore archived-content ingestion"). Commit:

```bash
git add TODO.md
git commit -m "docs: TODO — Omnivore zip import shipped (slice A)"
```
