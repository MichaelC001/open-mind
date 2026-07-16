package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rohithgilla12/openmind/api/internal/api"
)

// vectorLiteral builds a pgvector text literal for a 768-dim vector, with the
// given leading components followed by zero padding — enough to distinguish
// candidates in tests without hand-writing 768 numbers.
func vectorLiteral(components ...float32) string {
	const dims = 768
	parts := make([]string, dims)
	for i := range parts {
		if i < len(components) {
			parts[i] = fmt.Sprintf("%v", components[i])
		} else {
			parts[i] = "0"
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// insertEmbedding inserts an embedding row for itemID directly, bypassing the
// enrichment pipeline (the noop AI provider never produces real embeddings).
func insertEmbedding(t *testing.T, pool *pgxpool.Pool, ctx context.Context, uid uuid.UUID, itemIDStr string, vector string) {
	t.Helper()
	id := uuid.MustParse(itemIDStr)
	_, err := pool.Exec(ctx, `INSERT INTO item_embeddings (item_id, user_id, embedding) VALUES ($1, $2, $3::vector)`, id, uid, vector)
	if err != nil {
		t.Fatalf("seeding embedding: %v", err)
	}
}

func getRelated(t *testing.T, baseURL, id string) []map[string]any {
	t.Helper()
	resp, err := http.Get(baseURL + "/items/" + id + "/related")
	if err != nil {
		t.Fatalf("get related: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode related: %v", err)
	}
	return out
}

func TestRelatedOrdersByDistance(t *testing.T) {
	s, rc, pool := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)
	ctx := t.Context()

	src := createNoteItem(t, srv.URL, "source")
	near := createNoteItem(t, srv.URL, "near")
	mid := createNoteItem(t, srv.URL, "mid")
	far := createNoteItem(t, srv.URL, "far")

	uid := api.DevUserID
	insertEmbedding(t, pool, ctx, uid, src, vectorLiteral(1, 0))
	insertEmbedding(t, pool, ctx, uid, near, vectorLiteral(1, 0.05))
	insertEmbedding(t, pool, ctx, uid, mid, vectorLiteral(1, 0.2))
	insertEmbedding(t, pool, ctx, uid, far, vectorLiteral(1, 0.4))

	related := getRelated(t, srv.URL, src)
	if len(related) != 3 {
		t.Fatalf("related count = %d, want 3: %v", len(related), related)
	}
	gotOrder := []string{itemID(related[0]), itemID(related[1]), itemID(related[2])}
	want := []string{near, mid, far}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("order[%d] = %s, want %s (full order %v, want %v)", i, gotOrder[i], want[i], gotOrder, want)
		}
	}
}

func TestRelatedExcludesSelfAndLinked(t *testing.T) {
	t.Run("linked a<b", func(t *testing.T) {
		s, rc, pool := testDeps(t)
		srv := httptest.NewServer(newSrv(t, s, rc, ""))
		t.Cleanup(srv.Close)
		ctx := t.Context()

		a := createNoteItem(t, srv.URL, "a")
		b := createNoteItem(t, srv.URL, "b")
		uid := api.DevUserID
		insertEmbedding(t, pool, ctx, uid, a, vectorLiteral(1, 0))
		insertEmbedding(t, pool, ctx, uid, b, vectorLiteral(1, 0.05))
		createLink(t, srv.URL, a, b).Body.Close()

		related := getRelated(t, srv.URL, a)
		if len(related) != 0 {
			t.Errorf("related = %v, want empty (self excluded, b linked)", related)
		}
	})

	t.Run("linked b<a ordering across the pair", func(t *testing.T) {
		s, rc, pool := testDeps(t)
		srv := httptest.NewServer(newSrv(t, s, rc, ""))
		t.Cleanup(srv.Close)
		ctx := t.Context()

		a := createNoteItem(t, srv.URL, "a")
		b := createNoteItem(t, srv.URL, "b")
		uid := api.DevUserID
		insertEmbedding(t, pool, ctx, uid, a, vectorLiteral(1, 0))
		insertEmbedding(t, pool, ctx, uid, b, vectorLiteral(1, 0.05))
		// Link from b's perspective this time — the canonical pair storage
		// must still exclude it regardless of link-creation direction.
		createLink(t, srv.URL, b, a).Body.Close()

		related := getRelated(t, srv.URL, a)
		if len(related) != 0 {
			t.Errorf("related = %v, want empty (linked from either direction)", related)
		}
	})
}

func TestRelatedThresholdCutoff(t *testing.T) {
	s, rc, pool := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)
	ctx := t.Context()

	src := createNoteItem(t, srv.URL, "source")
	orth := createNoteItem(t, srv.URL, "orthogonal")
	uid := api.DevUserID
	insertEmbedding(t, pool, ctx, uid, src, vectorLiteral(1, 0))
	insertEmbedding(t, pool, ctx, uid, orth, vectorLiteral(0, 1))

	related := getRelated(t, srv.URL, src)
	if len(related) != 0 {
		t.Errorf("related = %v, want empty (orthogonal candidate beyond threshold)", related)
	}
}

func TestRelatedLimit(t *testing.T) {
	s, rc, pool := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)
	ctx := t.Context()

	src := createNoteItem(t, srv.URL, "source")
	uid := api.DevUserID
	insertEmbedding(t, pool, ctx, uid, src, vectorLiteral(1, 0))
	for i := 0; i < 7; i++ {
		id := createNoteItem(t, srv.URL, fmt.Sprintf("candidate %d", i))
		insertEmbedding(t, pool, ctx, uid, id, vectorLiteral(1, 0.01*float32(i+1)))
	}

	related := getRelated(t, srv.URL, src)
	if len(related) != 5 {
		t.Errorf("related count = %d, want 5", len(related))
	}
}

func TestRelatedNoEmbeddingEmpty(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	src := createNoteItem(t, srv.URL, "no embedding")

	related := getRelated(t, srv.URL, src)
	if related == nil || len(related) != 0 {
		t.Errorf("related = %v, want empty list", related)
	}
}

func TestRelatedScoping(t *testing.T) {
	s, rc, pool := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)
	ctx := t.Context()

	src := createNoteItem(t, srv.URL, "mine")
	uid := api.DevUserID
	insertEmbedding(t, pool, ctx, uid, src, vectorLiteral(1, 0))

	otherID := seedOtherUserItem(t, s, "not yours")
	otherUID := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	insertEmbedding(t, pool, ctx, otherUID, otherID, vectorLiteral(1, 0))

	related := getRelated(t, srv.URL, src)
	if len(related) != 0 {
		t.Errorf("related = %v, want empty (other user's near-identical embedding must not appear)", related)
	}

	resp, err := http.Get(srv.URL + "/items/" + src + "/related")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	resp2, err := http.Get(srv.URL + "/items/" + otherID + "/related")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for other user's item", resp2.StatusCode)
	}
}

func itemID(m map[string]any) string {
	item, ok := m["item"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := item["id"].(string)
	return id
}
