package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	appmcp "github.com/rohithgilla12/openmind/api/internal/mcp"
	"github.com/rohithgilla12/openmind/api/internal/search"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

type fakeBackend struct {
	saved    []db.Item
	items    []db.Item
	lenses   []db.Lense
	failLens bool
	notFound bool

	tagged        map[string][]string
	pinned        map[string]bool
	deleted       []string
	drifted       int
	createdLenses []db.Lense
	deletedLenses []string
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func (f *fakeBackend) Save(_ context.Context, _ uuid.UUID, url, note string) (db.Item, error) {
	it := db.Item{ID: uuid.New(), Url: url, Body: note, Status: "pending", CreatedAt: ts(time.Unix(0, 0))}
	f.saved = append(f.saved, it)
	return it, nil
}
func (f *fakeBackend) Search(_ context.Context, _ uuid.UUID, q, color string, parse bool) (appmcp.SearchOutcome, error) {
	rs := make([]search.Result, 0, len(f.items))
	for _, it := range f.items {
		rs = append(rs, search.Result{Item: it, Score: 1})
	}
	u := ""
	if parse {
		u = "text " + q
	}
	return appmcp.SearchOutcome{Results: rs, Understood: u}, nil
}
func (f *fakeBackend) ListRecent(_ context.Context, _ uuid.UUID, limit int) ([]db.Item, error) {
	if limit < len(f.items) {
		return f.items[:limit], nil
	}
	return f.items, nil
}
func (f *fakeBackend) GetItem(_ context.Context, _ uuid.UUID, id uuid.UUID) (db.Item, error) {
	if f.notFound {
		return db.Item{}, appmcp.ErrNotFound
	}
	return db.Item{ID: id, Url: "https://x", Title: "Doomed", Status: "enriched", Body: "hello body", CreatedAt: ts(time.Unix(0, 0))}, nil
}
func (f *fakeBackend) ListLenses(_ context.Context, _ uuid.UUID) ([]db.Lense, error) {
	return f.lenses, nil
}
func (f *fakeBackend) RunLens(_ context.Context, _ uuid.UUID, id uuid.UUID) ([]search.Result, error) {
	if f.failLens {
		return nil, appmcp.ErrNotFound
	}
	return []search.Result{{Item: db.Item{ID: id, Url: "https://l", Status: "enriched", CreatedAt: ts(time.Unix(0, 0))}, Score: 2}}, nil
}

func (f *fakeBackend) SetUserTags(_ context.Context, _ uuid.UUID, id uuid.UUID, tags []string) (db.Item, error) {
	if f.notFound {
		return db.Item{}, appmcp.ErrNotFound
	}
	if f.tagged == nil {
		f.tagged = map[string][]string{}
	}
	f.tagged[id.String()] = tags
	return db.Item{ID: id, UserTags: tags, Status: "enriched", CreatedAt: ts(time.Unix(0, 0))}, nil
}

func (f *fakeBackend) SetPinned(_ context.Context, _ uuid.UUID, id uuid.UUID, pinned bool) (db.Item, error) {
	if f.notFound {
		return db.Item{}, appmcp.ErrNotFound
	}
	if f.pinned == nil {
		f.pinned = map[string]bool{}
	}
	f.pinned[id.String()] = pinned
	return db.Item{ID: id, Status: "enriched", CreatedAt: ts(time.Unix(0, 0))}, nil
}

func (f *fakeBackend) DeleteItem(_ context.Context, _ uuid.UUID, id uuid.UUID) (db.Item, error) {
	if f.notFound {
		return db.Item{}, appmcp.ErrNotFound
	}
	f.deleted = append(f.deleted, id.String())
	return db.Item{ID: id, Title: "Doomed", Url: "https://x/doomed", CreatedAt: ts(time.Unix(0, 0))}, nil
}

func (f *fakeBackend) CreateLens(_ context.Context, _ uuid.UUID, name string, rule appmcp.LensRule) (db.Lense, error) {
	l := db.Lense{ID: uuid.New(), Name: name, Rule: []byte(`{"q":"` + rule.Q + `"}`)}
	f.createdLenses = append(f.createdLenses, l)
	return l, nil
}

func (f *fakeBackend) DeleteLens(_ context.Context, _ uuid.UUID, id uuid.UUID) (db.Lense, error) {
	if f.notFound {
		return db.Lense{}, appmcp.ErrNotFound
	}
	f.deletedLenses = append(f.deletedLenses, id.String())
	return db.Lense{ID: id, Name: "Old lens", Rule: []byte(`{"q":"old"}`)}, nil
}

func (f *fakeBackend) GetDesk(_ context.Context, _ uuid.UUID) ([]db.Item, error) {
	return f.items, nil
}

func (f *fakeBackend) GetDrift(_ context.Context, _ uuid.UUID) ([]db.Item, int, error) {
	return f.items, 42, nil
}

func (f *fakeBackend) Related(_ context.Context, _ uuid.UUID, id uuid.UUID) ([]appmcp.RelatedResult, error) {
	if f.notFound {
		return nil, appmcp.ErrNotFound
	}
	return []appmcp.RelatedResult{
		{Item: db.Item{ID: uuid.New(), Url: "https://near", Status: "enriched", CreatedAt: ts(time.Unix(0, 0))}, Distance: 0.1},
		{Item: db.Item{ID: uuid.New(), Url: "https://far", Status: "enriched", CreatedAt: ts(time.Unix(0, 0))}, Distance: 0.4},
	}, nil
}

// connect spins the MCP handler on an httptest server and returns a connected
// client session that talks the real Streamable HTTP transport.
func connect(t *testing.T, b appmcp.Backend) *sdk.ClientSession {
	t.Helper()
	h := appmcp.NewHandler(b, func(context.Context) uuid.UUID { return uuid.New() })
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func call(t *testing.T, sess *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func TestToolsListHasSix(t *testing.T) {
	sess := connect(t, &fakeBackend{})
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 14 {
		t.Fatalf("want 14 tools, got %d", len(res.Tools))
	}
}

func TestSaveItemRequiresExactlyOne(t *testing.T) {
	sess := connect(t, &fakeBackend{})
	// neither
	if r := call(t, sess, "save_item", map[string]any{}); !r.IsError {
		t.Fatal("expected error when neither url nor note given")
	}
	// both
	if r := call(t, sess, "save_item", map[string]any{"url": "https://x", "note": "n"}); !r.IsError {
		t.Fatal("expected error when both url and note given")
	}
	// exactly one
	if r := call(t, sess, "save_item", map[string]any{"url": "https://x"}); r.IsError {
		t.Fatal("unexpected error saving a url")
	}
}

func TestGetItemNotFound(t *testing.T) {
	sess := connect(t, &fakeBackend{notFound: true})
	r := call(t, sess, "get_item", map[string]any{"id": uuid.New().String()})
	if !r.IsError {
		t.Fatal("expected not-found tool error")
	}
}

func TestGetItemBadUUID(t *testing.T) {
	sess := connect(t, &fakeBackend{})
	r := call(t, sess, "get_item", map[string]any{"id": "not-a-uuid"})
	if !r.IsError {
		t.Fatal("expected bad-uuid tool error")
	}
}

func TestSearchEmpty(t *testing.T) {
	sess := connect(t, &fakeBackend{})
	r := call(t, sess, "search_items", map[string]any{})
	if !r.IsError {
		t.Fatal("expected error when no query or color")
	}
}

func decode(t *testing.T, r *sdk.CallToolResult, out any) {
	t.Helper()
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
}

func resultText(r *sdk.CallToolResult) string {
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestSetUserTagsTool(t *testing.T) {
	fake := &fakeBackend{}
	sess := connect(t, fake)
	id := uuid.New()

	r := call(t, sess, "set_user_tags", map[string]any{"id": id.String(), "tags": []string{"a", "b"}})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out appmcp.ItemSummary
	decode(t, r, &out)
	if len(out.UserTags) != 2 || out.UserTags[0] != "a" || out.UserTags[1] != "b" {
		t.Fatalf("unexpected userTags: %+v", out.UserTags)
	}
	if got := fake.tagged[id.String()]; len(got) != 2 {
		t.Fatalf("fake.tagged not updated: %+v", fake.tagged)
	}

	if r := call(t, sess, "set_user_tags", map[string]any{"id": "not-a-uuid", "tags": []string{}}); !r.IsError {
		t.Fatal("expected bad-uuid tool error")
	}

	fakeNF := &fakeBackend{notFound: true}
	sessNF := connect(t, fakeNF)
	r = call(t, sessNF, "set_user_tags", map[string]any{"id": uuid.New().String(), "tags": []string{}})
	if !r.IsError || !strings.Contains(resultText(r), "item not found") {
		t.Fatalf("expected item not found error, got %+v", r)
	}
}

func TestPinItemTool(t *testing.T) {
	fake := &fakeBackend{}
	sess := connect(t, fake)
	id := uuid.New()

	r := call(t, sess, "pin_item", map[string]any{"id": id.String(), "pinned": true})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	if !fake.pinned[id.String()] {
		t.Fatalf("expected pinned true for %s: %+v", id, fake.pinned)
	}

	r = call(t, sess, "pin_item", map[string]any{"id": id.String(), "pinned": false})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	if fake.pinned[id.String()] {
		t.Fatalf("expected pinned false for %s: %+v", id, fake.pinned)
	}
}

func TestDeleteItemConfirmFlow(t *testing.T) {
	fake := &fakeBackend{}
	sess := connect(t, fake)
	id := uuid.New()

	r := call(t, sess, "delete_item", map[string]any{"id": id.String()})
	if !r.IsError {
		t.Fatal("expected refusal without confirm")
	}
	if !strings.Contains(resultText(r), `refusing to delete "Doomed"`) {
		t.Fatalf("expected refusal message, got %q", resultText(r))
	}
	if len(fake.deleted) != 0 {
		t.Fatalf("expected no deletion without confirm, got %+v", fake.deleted)
	}

	r = call(t, sess, "delete_item", map[string]any{"id": id.String(), "confirm": true})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out appmcp.DeletedItem
	decode(t, r, &out)
	if !out.Deleted {
		t.Fatalf("expected deleted:true, got %+v", out)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != id.String() {
		t.Fatalf("expected fake.deleted to contain id, got %+v", fake.deleted)
	}
}

func TestCreateLensTool(t *testing.T) {
	fake := &fakeBackend{}
	sess := connect(t, fake)

	r := call(t, sess, "create_lens", map[string]any{"name": "Go stuff", "rule": map[string]any{"q": "golang"}})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out appmcp.LensInfo
	decode(t, r, &out)
	if out.Name != "Go stuff" {
		t.Fatalf("unexpected name: %+v", out)
	}
	if len(fake.createdLenses) != 1 {
		t.Fatalf("expected one created lens, got %+v", fake.createdLenses)
	}

	if r := call(t, sess, "create_lens", map[string]any{"name": "", "rule": map[string]any{"q": "x"}}); !r.IsError {
		t.Fatal("expected error for empty name")
	}
}

func TestDeleteLensTool(t *testing.T) {
	fake := &fakeBackend{}
	sess := connect(t, fake)
	id := uuid.New()

	r := call(t, sess, "delete_lens", map[string]any{"id": id.String()})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out appmcp.DeletedLens
	decode(t, r, &out)
	if !out.Deleted || out.Lens.Name != "Old lens" {
		t.Fatalf("unexpected result: %+v", out)
	}

	fakeNF := &fakeBackend{notFound: true}
	sessNF := connect(t, fakeNF)
	r = call(t, sessNF, "delete_lens", map[string]any{"id": uuid.New().String()})
	if !r.IsError || !strings.Contains(resultText(r), "lens not found") {
		t.Fatalf("expected lens not found error, got %+v", r)
	}
}

func TestGetDeskTool(t *testing.T) {
	items := []db.Item{{ID: uuid.New(), Url: "https://a", Status: "enriched", CreatedAt: ts(time.Unix(0, 0))}}
	fake := &fakeBackend{items: items}
	sess := connect(t, fake)

	r := call(t, sess, "get_desk", map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out struct {
		Items []appmcp.ItemSummary `json:"items"`
	}
	decode(t, r, &out)
	if len(out.Items) != 1 || out.Items[0].ID != items[0].ID.String() {
		t.Fatalf("unexpected items: %+v", out.Items)
	}
}

func TestGetDriftReadOnly(t *testing.T) {
	items := []db.Item{{ID: uuid.New(), Url: "https://a", Status: "enriched", CreatedAt: ts(time.Unix(0, 0))}}
	fake := &fakeBackend{items: items}
	sess := connect(t, fake)

	r := call(t, sess, "get_drift", map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out struct {
		Items []appmcp.ItemSummary `json:"items"`
		Total int                  `json:"total"`
	}
	decode(t, r, &out)
	if out.Total != 42 || len(out.Items) != 1 {
		t.Fatalf("unexpected result: %+v", out)
	}
	if fake.drifted != 0 {
		t.Fatalf("get_drift must not mutate: drifted=%d", fake.drifted)
	}
}

func TestRelatedItemsTool(t *testing.T) {
	fake := &fakeBackend{}
	sess := connect(t, fake)

	r := call(t, sess, "related_items", map[string]any{"id": uuid.New().String()})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out struct {
		Results []appmcp.RelatedHit `json:"results"`
	}
	decode(t, r, &out)
	if len(out.Results) != 2 {
		t.Fatalf("expected 2 related hits, got %+v", out.Results)
	}
	if out.Results[0].Distance != 0.1 || out.Results[1].Distance != 0.4 {
		t.Fatalf("unexpected distances: %+v", out.Results)
	}

	if r := call(t, sess, "related_items", map[string]any{"id": "not-a-uuid"}); !r.IsError {
		t.Fatal("expected bad-uuid tool error")
	}

	fakeNF := &fakeBackend{notFound: true}
	sessNF := connect(t, fakeNF)
	r = call(t, sessNF, "related_items", map[string]any{"id": uuid.New().String()})
	if !r.IsError || !strings.Contains(resultText(r), "item not found") {
		t.Fatalf("expected item not found error, got %+v", r)
	}
}

func TestListLensesMalformedRule(t *testing.T) {
	fake := &fakeBackend{lenses: []db.Lense{{ID: uuid.New(), Name: "Bad", Rule: []byte(`{bad`)}}}
	sess := connect(t, fake)

	r := call(t, sess, "list_lenses", map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %+v", r)
	}
	var out struct {
		Lenses []appmcp.LensInfo `json:"lenses"`
	}
	decode(t, r, &out)
	if len(out.Lenses) != 1 {
		t.Fatalf("expected malformed lens still listed, got %+v", out.Lenses)
	}
	if out.Lenses[0].RuleError == "" {
		t.Fatalf("expected RuleError to be set, got %+v", out.Lenses[0])
	}
}

func TestNewServerServesSameRegistryAsHandler(t *testing.T) {
	fake := &fakeBackend{}
	srv := appmcp.NewServer(fake, func(context.Context) uuid.UUID { return uuid.New() })

	serverT, clientT := sdk.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = ss.Wait() }()
	defer func() { _ = ss.Close() }()

	client := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 14 {
		t.Fatalf("tools = %d, want 14", len(tools.Tools))
	}
	prompts, err := cs.ListPrompts(ctx, &sdk.ListPromptsParams{})
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "find_and_summarise" {
		t.Fatalf("prompts = %+v", prompts.Prompts)
	}
}
