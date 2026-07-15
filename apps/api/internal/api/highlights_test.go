package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rohithgilla12/openmind/api/internal/api"
	"github.com/rohithgilla12/openmind/api/internal/store/db"
)

// postHighlight POSTs a highlight creation request against an item.
func postHighlight(t *testing.T, baseURL, id, body string) *http.Response {
	t.Helper()
	return postJSON(t, baseURL+"/items/"+id+"/highlights", body)
}

// deleteHighlight DELETEs a highlight by id.
func deleteHighlight(t *testing.T, baseURL, id string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/highlights/"+id, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	return resp
}

func TestCreateHighlightCreatesQuoteAndLink(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	article := seedEnriched(t, s, "an article", "the body of the article", "article", nil)
	id := article.ID.String()

	resp := postHighlight(t, srv.URL, id, `{"exact":"the selected text","prefix":"before ","suffix":" after","offsetHint":42}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var out api.CreateHighlightResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Highlight.Exact != "the selected text" {
		t.Errorf("highlight.exact = %q, want %q", out.Highlight.Exact, "the selected text")
	}
	if out.QuoteItem.CardType == nil || string(*out.QuoteItem.CardType) != "quote" {
		t.Errorf("quoteItem.cardType = %v, want quote", out.QuoteItem.CardType)
	}
	if out.QuoteItem.Status != "pending" {
		t.Errorf("quoteItem.status = %v, want pending", out.QuoteItem.Status)
	}

	links := getLinks(t, srv.URL, id)
	if !containsID(links, out.QuoteItem.Id.String()) {
		t.Errorf("source links = %v, want to contain quote item %v", links, out.QuoteItem.Id)
	}

	var offsetHint int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT offset_hint FROM highlights WHERE id = $1`, out.Highlight.Id).Scan(&offsetHint); err != nil {
		t.Fatalf("querying highlight: %v", err)
	}
	if offsetHint != 42 {
		t.Errorf("offset_hint = %d, want 42", offsetHint)
	}
}

func TestCreateHighlightValidation(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	article := seedEnriched(t, s, "an article", "body", "article", nil)
	id := article.ID.String()

	resp := postHighlight(t, srv.URL, id, `{"exact":""}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty exact status = %d, want 400", resp.StatusCode)
	}
	resp2 := postHighlight(t, srv.URL, id, `{"exact":"   "}`)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("whitespace exact status = %d, want 400", resp2.StatusCode)
	}

	longExact := strings.Repeat("a", 2001)
	resp3 := postHighlight(t, srv.URL, id, `{"exact":"`+longExact+`"}`)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("2001-rune exact status = %d, want 400", resp3.StatusCode)
	}

	longPrefix := strings.Repeat("p", 200)
	resp4 := postHighlight(t, srv.URL, id, `{"exact":"ok","prefix":"`+longPrefix+`"}`)
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusCreated {
		t.Fatalf("200-rune prefix status = %d, want 201", resp4.StatusCode)
	}
	var out api.CreateHighlightResponse
	if err := json.NewDecoder(resp4.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len([]rune(out.Highlight.Prefix)) != 64 {
		t.Errorf("stored prefix rune length = %d, want 64 (truncated)", len([]rune(out.Highlight.Prefix)))
	}
}

func TestCreateHighlightUnknownItem(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	resp := postHighlight(t, srv.URL, "11111111-1111-1111-1111-111111111111", `{"exact":"x"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("random uuid status = %d, want 404", resp.StatusCode)
	}

	otherID := seedOtherUserItem(t, s, "not yours")
	resp2 := postHighlight(t, srv.URL, otherID, `{"exact":"x"}`)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("cross-tenant status = %d, want 404", resp2.StatusCode)
	}
}

func TestListItemHighlights(t *testing.T) {
	s, rc, _ := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	article := seedEnriched(t, s, "an article", "body", "article", nil)
	id := article.ID.String()

	postHighlight(t, srv.URL, id, `{"exact":"first"}`).Body.Close()
	postHighlight(t, srv.URL, id, `{"exact":"second"}`).Body.Close()

	resp, err := http.Get(srv.URL + "/items/" + id + "/highlights")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out []api.Highlight
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d highlights, want 2", len(out))
	}
	if out[0].Exact != "first" || out[1].Exact != "second" {
		t.Errorf("order = [%q, %q], want oldest-first [first, second]", out[0].Exact, out[1].Exact)
	}

	resp2, err := http.Get(srv.URL + "/items/11111111-1111-1111-1111-111111111111/highlights")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("unknown item status = %d, want 404", resp2.StatusCode)
	}

	otherID := seedOtherUserItem(t, s, "not yours")
	resp3, err := http.Get(srv.URL + "/items/" + otherID + "/highlights")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("other user's item status = %d, want 404 (not empty list)", resp3.StatusCode)
	}
}

func TestDeleteHighlightRemovesQuote(t *testing.T) {
	s, rc, pool := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	article := seedEnriched(t, s, "an article", "body", "article", nil)
	id := article.ID.String()

	resp := postHighlight(t, srv.URL, id, `{"exact":"gone soon"}`)
	var created api.CreateHighlightResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()

	del := deleteHighlight(t, srv.URL, created.Highlight.Id.String())
	del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}

	var hlCount, itemCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM highlights WHERE id = $1`, created.Highlight.Id).Scan(&hlCount); err != nil {
		t.Fatalf("counting highlights: %v", err)
	}
	if hlCount != 0 {
		t.Errorf("highlights row count = %d, want 0", hlCount)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM items WHERE id = $1`, created.QuoteItem.Id).Scan(&itemCount); err != nil {
		t.Fatalf("counting quote item: %v", err)
	}
	if itemCount != 0 {
		t.Errorf("quote item count = %d, want 0", itemCount)
	}

	links := getLinks(t, srv.URL, id)
	if containsID(links, created.QuoteItem.Id.String()) {
		t.Errorf("source links = %v, want not to contain deleted quote item", links)
	}

	redelete := deleteHighlight(t, srv.URL, created.Highlight.Id.String())
	redelete.Body.Close()
	if redelete.StatusCode != http.StatusNotFound {
		t.Errorf("re-delete status = %d, want 404", redelete.StatusCode)
	}

	// Cross-tenant delete: seed a highlight for another user, verify 404 and nothing deleted.
	otherUID := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	otherArticleID := seedOtherUserItem(t, s, "other article")
	ctx := context.Background()
	otherQuote, err := s.Queries.CreateQuoteItem(ctx, db.CreateQuoteItemParams{UserID: otherUID, Body: "other exact"})
	if err != nil {
		t.Fatalf("create other quote item: %v", err)
	}
	otherHL, err := s.Queries.CreateHighlight(ctx, db.CreateHighlightParams{
		UserID: otherUID, SourceItemID: uuid.MustParse(otherArticleID), QuoteItemID: otherQuote.ID, Exact: "other exact",
	})
	if err != nil {
		t.Fatalf("create other highlight: %v", err)
	}
	crossDel := deleteHighlight(t, srv.URL, otherHL.ID.String())
	crossDel.Body.Close()
	if crossDel.StatusCode != http.StatusNotFound {
		t.Errorf("cross-tenant delete status = %d, want 404", crossDel.StatusCode)
	}
	var survives int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM highlights WHERE id = $1`, otherHL.ID).Scan(&survives); err != nil {
		t.Fatalf("counting other highlight: %v", err)
	}
	if survives != 1 {
		t.Errorf("other user's highlight count = %d, want 1 (must survive)", survives)
	}
}

func TestDeleteQuoteItemCascadesHighlight(t *testing.T) {
	s, rc, pool := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	article := seedEnriched(t, s, "an article", "body", "article", nil)
	id := article.ID.String()

	resp := postHighlight(t, srv.URL, id, `{"exact":"cascade me"}`)
	var created api.CreateHighlightResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/items/"+created.QuoteItem.Id.String(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete quote item status = %d, want 204", del.StatusCode)
	}

	var hlCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM highlights WHERE id = $1`, created.Highlight.Id).Scan(&hlCount); err != nil {
		t.Fatalf("counting highlights: %v", err)
	}
	if hlCount != 0 {
		t.Errorf("highlights row count = %d, want 0 (FK cascade)", hlCount)
	}

	got, err := http.Get(srv.URL + "/items/" + id)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	defer got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Errorf("source article status = %d, want 200 (unaffected)", got.StatusCode)
	}
}

func TestTwoIdenticalHighlightsAllowed(t *testing.T) {
	s, rc, pool := testDeps(t)
	srv := httptest.NewServer(newSrv(t, s, rc, ""))
	t.Cleanup(srv.Close)

	article := seedEnriched(t, s, "an article", "body", "article", nil)
	id := article.ID.String()

	resp1 := postHighlight(t, srv.URL, id, `{"exact":"same text"}`)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", resp1.StatusCode)
	}
	resp2 := postHighlight(t, srv.URL, id, `{"exact":"same text"}`)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("second status = %d, want 201", resp2.StatusCode)
	}

	var hlCount, quoteCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM highlights WHERE source_item_id = $1`, article.ID).Scan(&hlCount); err != nil {
		t.Fatalf("counting highlights: %v", err)
	}
	if hlCount != 2 {
		t.Errorf("highlights count = %d, want 2", hlCount)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM items WHERE card_type = 'quote' AND body = 'same text'`).Scan(&quoteCount); err != nil {
		t.Fatalf("counting quote items: %v", err)
	}
	if quoteCount != 2 {
		t.Errorf("quote item count = %d, want 2", quoteCount)
	}
}
