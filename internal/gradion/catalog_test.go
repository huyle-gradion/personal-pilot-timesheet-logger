package gradion

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// classificationFixture builds the verified live shape: a structured reference
// envelope (ID + Name) PLUS the plain-text rendering (Code + Activity Type).
func classificationFixture(entries ...struct{ id, name, code, activity string }) callOutcome {
	resources := make([]map[string]any, 0, len(entries))
	var text strings.Builder
	for _, e := range entries {
		resources = append(resources, map[string]any{"id": e.id, "kind": "classification", "label": e.name})
		fmt.Fprintf(&text, "- %s (%s) [%s] · %s\n", e.name, e.code, e.activity, e.id)
	}
	rb, _ := json.Marshal(map[string]any{"resources": resources})
	return callOutcome{Structured: rb, Text: text.String()}
}

func catalogFake(t *testing.T, out callOutcome) *Client {
	t.Helper()
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolGetClassifications {
			return callOutcome{}
		}
		return out
	}
	return newTestClient(t, f)
}

func TestClassificationCatalog_ParsesCodeAndActivityType(t *testing.T) {
	out := classificationFixture(
		struct{ id, name, code, activity string }{"uuid-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"},
		struct{ id, name, code, activity string }{"uuid-2", "Client Alpha", "ALPHA-1", "client project"},
	)
	c := catalogFake(t, out)

	cat, err := c.ClassificationCatalog(context.Background(), "")
	if err != nil {
		t.Fatalf("ClassificationCatalog: %v", err)
	}
	if len(cat.Options) != 2 {
		t.Fatalf("options = %d, want 2: %+v", len(cat.Options), cat.Options)
	}
	got := cat.Options[0]
	if got.ID != "uuid-1" || got.Name != "Gradion Intern Academy 2026" || got.Code != "INTGRADI2610" || got.ActivityType != "internal project" {
		t.Errorf("option[0] = %+v", got)
	}
	if cat.Options[1].Code != "ALPHA-1" || cat.Options[1].ActivityType != "client project" {
		t.Errorf("option[1] = %+v", cat.Options[1])
	}
	if !cat.Complete {
		t.Error("a non-truncated catalog must be Complete")
	}
	if cat.Source != "live" {
		t.Errorf("source = %q, want live", cat.Source)
	}
}

func TestClassificationCatalog_TruncatedHeaderIsPartial(t *testing.T) {
	base := classificationFixture(struct{ id, name, code, activity string }{"uuid-1", "A", "C1", "internal project"})
	base.Text = "20+ classifications found (showing 20 — refine search to narrow)\n" + base.Text
	c := catalogFake(t, base)

	cat, err := c.ClassificationCatalog(context.Background(), "")
	if err != nil {
		t.Fatalf("ClassificationCatalog: %v", err)
	}
	if cat.Complete {
		t.Errorf("a truncation header must make the catalog partial, got %+v", cat)
	}
	if len(cat.Options) != 1 {
		t.Fatalf("the header must not be parsed as an option, got %+v", cat.Options)
	}
}

func TestClassificationCatalog_MalformedTextFailsClosed(t *testing.T) {
	c := catalogFake(t, callOutcome{Text: "totally unparseable garbage with no entries"})
	if _, err := c.ClassificationCatalog(context.Background(), ""); CodeOf(err) != CodeCatalogUnparsed {
		t.Fatalf("expected %s, got %v", CodeCatalogUnparsed, err)
	}
}

func TestClassificationCatalog_ResultCapIsPartial(t *testing.T) {
	entries := make([]struct{ id, name, code, activity string }, 0, MaxClassificationResults)
	for i := 0; i < MaxClassificationResults; i++ {
		entries = append(entries, struct{ id, name, code, activity string }{
			fmt.Sprintf("uuid-%d", i), fmt.Sprintf("N%d", i), fmt.Sprintf("C%d", i), "internal project",
		})
	}
	c := catalogFake(t, classificationFixture(entries...))

	cat, err := c.ClassificationCatalog(context.Background(), "")
	if err != nil {
		t.Fatalf("ClassificationCatalog: %v", err)
	}
	if len(cat.Options) != MaxClassificationResults {
		t.Fatalf("options = %d, want %d", len(cat.Options), MaxClassificationResults)
	}
	if cat.Complete {
		t.Error("reaching the tool result cap must mark the catalog partial")
	}
}

func TestClassificationCatalog_EmptyCatalogIsNotAnError(t *testing.T) {
	c := catalogFake(t, callOutcome{Text: "0 classifications found"})
	cat, err := c.ClassificationCatalog(context.Background(), "")
	if err != nil {
		t.Fatalf("an empty catalog must not error: %v", err)
	}
	if len(cat.Options) != 0 || !cat.Complete {
		t.Errorf("want an empty, complete catalog, got %+v", cat)
	}
}

func TestClassificationCatalog_StructuredOnlyStillWorks(t *testing.T) {
	// No text block: ID + Name survive; Code/ActivityType are empty (text is the
	// only source). This preserves the legacy Classifications() behavior.
	rb, _ := json.Marshal(map[string]any{"resources": []map[string]any{
		{"id": "c1", "kind": "classification", "label": ""},
		{"id": "c2", "kind": "classification", "label": "Project Two"},
	}})
	c := catalogFake(t, callOutcome{Structured: rb})
	cat, err := c.ClassificationCatalog(context.Background(), "")
	if err != nil {
		t.Fatalf("ClassificationCatalog: %v", err)
	}
	if len(cat.Options) != 2 || cat.Options[0].ID != "c1" || cat.Options[1].Name != "Project Two" {
		t.Errorf("options = %+v", cat.Options)
	}
}

// F6: a structured envelope with NO text block is unverifiable (the text block is
// the only source of Code + Activity Type), so the catalog is PARTIAL.
func TestClassificationCatalog_StructuredWithoutTextIsPartial(t *testing.T) {
	rb, _ := json.Marshal(map[string]any{"resources": []map[string]any{
		{"id": "c1", "kind": "classification", "label": "Some Project"},
	}})
	c := catalogFake(t, callOutcome{Structured: rb})
	cat, err := c.ClassificationCatalog(context.Background(), "")
	if err != nil {
		t.Fatalf("ClassificationCatalog: %v", err)
	}
	if len(cat.Options) != 1 || cat.Options[0].Code != "" || cat.Options[0].ActivityType != "" {
		t.Fatalf("options = %+v", cat.Options)
	}
	if cat.Complete {
		t.Errorf("a structured-only catalog (no text) must be PARTIAL, got %+v", cat)
	}
}

func TestClassificationCatalog_SearchArgSent(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, args map[string]any) callOutcome {
		if name != ToolGetClassifications {
			return callOutcome{}
		}
		if args["search"] != "intern" {
			t.Errorf("search arg = %v, want intern", args["search"])
		}
		return classificationFixture(struct{ id, name, code, activity string }{"u1", "I", "INTGRADI2610", "internal project"})
	}
	c := newTestClient(t, f)
	if _, err := c.ClassificationCatalog(context.Background(), "intern"); err != nil {
		t.Fatalf("ClassificationCatalog: %v", err)
	}
}
