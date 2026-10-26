package timesheet

import (
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
)

// TestParseCatalogZeroMatchPhrasings: the provider's zero-match renderings
// (live-observed: "No active classifications found.") are valid empty
// catalogs, not parse failures (issue trail of the empty-catalog fix).
func TestParseCatalogZeroMatchPhrasings(t *testing.T) {
	phrasings := []string{
		"No active classifications found.",
		"No classifications found",
		"0 classifications found",
		"no classifications matched",
		"  0 classifications available  ",
	}
	for _, text := range phrasings {
		cat, err := parseCatalog(mcp.CallResult{Content: text}, "assignment", time.Now().UTC())
		if err != nil {
			t.Fatalf("phrasing %q: unexpected error %v", text, err)
		}
		if len(cat.Options) != 0 || !cat.Complete || cat.CatalogID == "" {
			t.Fatalf("phrasing %q: catalog = %+v, want empty/complete/with digest", text, cat)
		}
		// The digest must be deterministic for the same empty content.
		cat2, err := parseCatalog(mcp.CallResult{Content: text}, "assignment", time.Now().UTC())
		if err != nil || cat2.CatalogID != cat.CatalogID {
			t.Fatalf("phrasing %q: digest not deterministic (%q vs %q)", text, cat.CatalogID, cat2.CatalogID)
		}
		// A different query on the same phrasing yields a different digest
		// (the query is part of the catalog identity).
		cat3, err := parseCatalog(mcp.CallResult{Content: text}, "other", time.Now().UTC())
		if err != nil || cat3.CatalogID == cat.CatalogID {
			t.Fatalf("phrasing %q: digest must vary with query", text)
		}
	}
}

// TestParseCatalogUnknownTextStillFailsClosed: a genuinely unparseable text
// block keeps the fail-closed error, and the provider text is never echoed.
// The diagnostic is a fixed string because provider content is arbitrary: a
// bearer token, an uppercase URL or a JSON field would survive the sanitizer
// and land in an error an agent then quotes.
func TestParseCatalogUnknownTextStillFailsClosed(t *testing.T) {
	for _, text := range []string{
		"total garbage from the provider\nsecond line",
		"https://x.invalid?token=secret only",
		"Authorization: Bearer SYNTHETIC_NOT_A_REAL_CREDENTIAL",
		`{"error":{"token":"SYNTHETIC_NOT_A_REAL_CREDENTIAL"}}`,
		"HTTPS://X.INVALID/secret",
	} {
		_, err := parseCatalog(mcp.CallResult{Content: text}, "q", time.Now().UTC())
		if err == nil || !strings.Contains(err.Error(), "the classification text could not be parsed") {
			t.Fatalf("text %q: err = %v, want the parse error", text, err)
		}
		for _, leak := range []string{"garbage", "secret", "SYNTHETIC_NOT_A_REAL_CREDENTIAL", "https", "HTTPS", "Authorization"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("text %q: err = %v, must not echo provider content (%q)", text, err, leak)
			}
		}
	}
}
