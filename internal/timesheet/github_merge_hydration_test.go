package timesheet

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestFetchMergedPullsHydratesMergerWhenSearchReturnsIssueShape(t *testing.T) {
	f := newGithubStatusFixture(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(w, `{
			"total_count": 1,
			"incomplete_results": false,
			"items": [{
				"number": 12,
				"title": "synthetic merge",
				"html_url": "https://example.invalid/o/r/pull/12",
				"pull_request": {
					"url": %q,
					"merged_at": "2026-10-22T10:00:00Z"
				}
			}]
		}`, "http://"+r.Host+"/repos/o/r/pulls/12")
		if err != nil {
			t.Errorf("write search response: %v", err)
		}
	})
	f.mux.HandleFunc("/repos/o/r/pulls/12", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(w, `{
			"number": 12,
			"title": "synthetic merge",
			"html_url": "https://example.invalid/o/r/pull/12",
			"merged_by": {"login": "user"},
			"merged_at": "2026-10-22T10:00:00Z"
		}`)
		if err != nil {
			t.Errorf("write pull detail response: %v", err)
		}
	})

	items, report := fetchMergedPulls(f.run(t), statusTestRepo, "o", "r", statusTestWindow, statusTestWindow.Add(24*time.Hour))

	if report.Family != "merged_pull_requests" || report.QueriedRepo != statusTestRepo || report.Count != 1 || report.Status != StatusComplete {
		t.Errorf("family report = %+v, want complete merged_pull_requests with count 1", report)
	}
	if len(items) != 1 {
		t.Fatalf("retained items = %d, want 1 canonical merge evidence item", len(items))
	}
	item := items[0]
	if item.Ref != "gh:o/r:pr-merge:12" || item.Kind != "merged_pull_request" ||
		item.Resource != "pr:o/r:12" || item.Repo != statusTestRepo || item.Actor != statusTestUser ||
		item.Time != "2026-10-22T10:00:00Z" || item.URL != "https://example.invalid/o/r/pull/12" ||
		item.Title != "synthetic merge" || item.Provenance != "search/issues,repos/pulls/12" {
		t.Fatalf("merge evidence = %+v, want canonical evidence for user merging o/r#12", item)
	}
}
