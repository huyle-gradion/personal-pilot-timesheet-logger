package timesheet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func powMergeDetail(number int, mergedAt, mergedBy string) map[string]any {
	item := map[string]any{
		"number": number, "title": "synthetic merge",
		"html_url": fmt.Sprintf("https://example.invalid/o/r/pull/%d", number),
	}
	if mergedAt != "" {
		item["merged_at"] = mergedAt
	}
	if mergedBy != "" {
		item["merged_by"] = map[string]any{"login": mergedBy}
	}
	return item
}

func powMergeSearchDoc(total int, incomplete bool, items ...map[string]any) map[string]any {
	return map[string]any{"total_count": total, "incomplete_results": incomplete, "items": items}
}

func powMergeSearchHandler(t *testing.T, doc map[string]any) http.HandlerFunc {
	t.Helper()
	candidates := []map[string]any{}
	if details, ok := doc["items"].([]map[string]any); ok {
		for _, detail := range details {
			candidates = append(candidates, map[string]any{
				"number": detail["number"], "title": detail["title"], "html_url": detail["html_url"],
				"pull_request": map[string]any{"url": "https://untrusted.invalid/pulls/999", "merged_at": detail["merged_at"]},
			})
		}
	}
	search := powMergeSearchDoc(doc["total_count"].(int), doc["incomplete_results"].(bool), candidates...)
	return func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("q"); !strings.Contains(q, "merged:") {
			t.Errorf("search query %q must use the documented merged: qualifier", q)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(search); err != nil {
			t.Errorf("write search: %v", err)
		}
	}
}

func newPowMergeFixture(t *testing.T, events http.HandlerFunc, doc map[string]any) *githubStatusFixture {
	t.Helper()
	f := newGithubStatusFixture(t, events, powMergeSearchHandler(t, doc))
	if details, ok := doc["items"].([]map[string]any); ok {
		registerPowMergeDetails(t, f, details)
	}
	return f
}

func registerPowMergeDetails(t *testing.T, f *githubStatusFixture, details []map[string]any) {
	t.Helper()
	for _, detail := range details {
		f.mux.HandleFunc(fmt.Sprintf("/repos/o/r/pulls/%d", detail["number"]), func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(detail); err != nil {
				t.Errorf("write PR detail: %v", err)
			}
		})
	}
}
