// Package ai defines the pluggable AI provider adapter used by the
// enrichment pipeline and search query parsing.
package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Enrichment holds the AI-generated summary and tags for a saved item.
type Enrichment struct {
	Summary string
	Tags    []string
}

// ParsedQuery is the structured interpretation of a natural-language search
// query: the free-text portion to run text/vector search over, an optional
// colour term (name or hex), and any card-type filters the user asked for.
// Providers that cannot interpret queries (e.g. noop) return {Text: q}, which
// keeps search fully functional without an AI backend.
type ParsedQuery struct {
	Text  string
	Color string
	Types []string
}

// Place is a real-world location extracted from saved content (e.g. a cafe
// named in an Instagram reel caption). Hint is the disambiguating locality
// from the same text ("Lisbon", "Shibuya"), empty when the text gives none.
type Place struct {
	Name string
	Hint string
}

// Provider is the adapter interface every AI backend must implement.
// The noop provider keeps the app fully functional without any AI backend
// configured.
type Provider interface {
	Name() string
	Summarise(ctx context.Context, title, body string) (string, error)
	Tag(ctx context.Context, title, body string) ([]string, error)
	Embed(ctx context.Context, text string) ([]float32, error)
	ParseQuery(ctx context.Context, q string) (ParsedQuery, error)
	// ExtractPlaces returns the specific, visitable places named in the given
	// title/caption text. Providers must only return places grounded in the
	// text — never inferred or invented — and return ErrNotSupported when they
	// cannot perform extraction (e.g. noop).
	ExtractPlaces(ctx context.Context, title, caption string) ([]Place, error)
}

// parseQueryInstruction is the shared system prompt for natural-language query
// parsing. It is used verbatim by every provider that can interpret queries so
// their behaviour stays consistent.
const parseQueryInstruction = `You interpret a natural-language search over a personal knowledge library. ` +
	`Split the query into three parts: "text" (the descriptive words to search for, with any colour word or item-type word removed), ` +
	`"color" (a single colour name or #RRGGBB hex string if the user mentions a colour, otherwise ""), and ` +
	`"types" (a subset of [article, product, book, recipe, video, tweet, image, note, quote] the user is asking for, otherwise []). ` +
	`Respond with only a JSON object of the form {"text": string, "color": string, "types": [string]}. ` +
	`Example: "blue book about bread" -> {"text":"bread","color":"blue","types":["book"]}.`

// extractPlacesInstruction is the shared system prompt for place extraction.
// It is used verbatim by every provider that can extract places so their
// behaviour stays consistent. The grounding rule ("only places the text
// names") is the anti-hallucination guard: a place must appear in the caption,
// not be guessed from vibes.
const extractPlacesInstruction = `You extract real-world, visitable places from the caption of a saved social-media video. ` +
	`Return only specific named places a person could visit (cafes, restaurants, bars, hotels, shops, landmarks, parks, museums) that the text itself names. ` +
	`Never invent, infer, or complete place names that are not present in the text; if the text names none, return an empty list. ` +
	`For each place set "hint" to the city/area/country the same text gives for it (or "" if none). ` +
	`Respond with only a JSON object of the form {"places": [{"name": string, "hint": string}]}. ` +
	`Example: "3 cafes you must try in Lisbon: Fabrica, Copenhagen Coffee Lab" -> {"places":[{"name":"Fabrica","hint":"Lisbon"},{"name":"Copenhagen Coffee Lab","hint":"Lisbon"}]}.`

// sanitisePlaces trims, de-duplicates (case-insensitive by name), and drops
// empty or overlong names from a model-proposed place list, returning nil when
// nothing remains.
func sanitisePlaces(in []Place) []Place {
	seen := make(map[string]bool, len(in))
	out := make([]Place, 0, len(in))
	for _, p := range in {
		p.Name = strings.TrimSpace(p.Name)
		p.Hint = strings.TrimSpace(p.Hint)
		key := strings.ToLower(p.Name)
		if p.Name == "" || len(p.Name) > 200 || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cardTypes is the set of card types ParseQuery may extract as filters. It must
// stay in sync with the CardType enum in openapi.yaml.
var cardTypes = map[string]bool{
	"article": true, "product": true, "book": true, "recipe": true,
	"video": true, "tweet": true, "image": true, "note": true, "quote": true,
}

// sanitiseTypes lowercases, trims, de-duplicates, and drops unknown values from
// a model-proposed card-type filter list, returning nil when nothing remains.
func sanitiseTypes(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if cardTypes[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ErrNotSupported is returned by providers that don't implement a given
// operation, e.g. Embed on the noop provider.
var ErrNotSupported = errors.New("ai: operation not supported by provider")

// errUnknownProvider marks a provider name FromEnv does not recognise, so it
// can be skipped rather than aborting chain construction.
var errUnknownProvider = errors.New("ai: unknown provider name")

// FromEnv builds a Provider from the environment. AI_PROVIDERS (comma-ordered,
// e.g. "gemini,openai,noop") takes precedence; otherwise the single AI_PROVIDER
// is used; if neither is set the noop provider is returned. Unknown names are
// warned about and skipped. A single provider with no configured rate limit is
// returned bare (no chain wrapper); otherwise a fallback Chain is built. Each
// provider may carry a client-side cap via AI_RPM_<UPPER(NAME)>.
//
// It takes a context because building some providers (e.g. gemini) makes a
// network round-trip during client construction.
func FromEnv(ctx context.Context) (Provider, error) {
	names := providerNames()
	entries := make([]ChainEntry, 0, len(names))
	for _, name := range names {
		p, err := buildProvider(ctx, name)
		if err != nil {
			if errors.Is(err, errUnknownProvider) {
				slog.Warn("ai: unknown provider name, skipping", "provider", name)
				continue
			}
			return nil, err
		}
		entries = append(entries, ChainEntry{Name: name, Provider: p, RPM: rpmForName(name)})
	}

	if len(entries) == 0 {
		slog.Warn("ai: no usable providers configured, falling back to noop")
		return NewNoop(), nil
	}
	// A lone provider without a rate limit needs no chain wrapper.
	if len(entries) == 1 && entries[0].RPM <= 0 {
		return entries[0].Provider, nil
	}
	return NewChain(entries...), nil
}

// providerNames resolves the ordered provider names from the environment,
// preferring AI_PROVIDERS over AI_PROVIDER and defaulting to noop.
func providerNames() []string {
	if csv := strings.TrimSpace(os.Getenv("AI_PROVIDERS")); csv != "" {
		parts := strings.Split(csv, ",")
		names := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
				names = append(names, p)
			}
		}
		if len(names) > 0 {
			return names
		}
	}
	if single := strings.ToLower(strings.TrimSpace(os.Getenv("AI_PROVIDER"))); single != "" {
		return []string{single}
	}
	return []string{"noop"}
}

// buildProvider constructs a single provider by name, returning
// errUnknownProvider for names it does not recognise.
func buildProvider(ctx context.Context, name string) (Provider, error) {
	switch name {
	case "noop":
		return NewNoop(), nil
	case "gemini":
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ai: provider gemini requires GEMINI_API_KEY")
		}
		p, err := NewGemini(ctx, apiKey)
		if err != nil {
			return nil, fmt.Errorf("building gemini provider: %w", err)
		}
		return p, nil
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		model := os.Getenv("OPENAI_MODEL")
		if apiKey == "" || model == "" {
			return nil, fmt.Errorf("ai: provider openai requires OPENAI_API_KEY and OPENAI_MODEL")
		}
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return NewOpenAI(baseURL, apiKey, model, os.Getenv("OPENAI_EMBED_MODEL")), nil
	default:
		return nil, errUnknownProvider
	}
}

// rpmForName reads the AI_RPM_<UPPER(NAME)> cap for a provider; 0 (no limit)
// when unset, empty, or invalid.
func rpmForName(name string) int {
	v := strings.TrimSpace(os.Getenv("AI_RPM_" + strings.ToUpper(name)))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		slog.Warn("ai: invalid AI_RPM value, ignoring", "provider", name, "value", v)
		return 0
	}
	return n
}
