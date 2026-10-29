package timesheet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func mergeDetailFixture(t *testing.T, numbers []int, detail http.HandlerFunc) *githubStatusFixture {
	t.Helper()
	f := newGithubStatusFixture(t, nil, func(w http.ResponseWriter, r *http.Request) {
		items := make([]map[string]any, 0, len(numbers))
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page <= 0 {
			t.Errorf("invalid search page")
			return
		}
		start := min((page-1)*100, len(numbers))
		end := min(start+100, len(numbers))
		for _, number := range numbers[start:end] {
			items = append(items, map[string]any{"number": number, "pull_request": map[string]any{"url": "https://untrusted.invalid/pulls/999"}})
		}
		if err := json.NewEncoder(w).Encode(powMergeSearchDoc(len(items), false, items...)); err != nil {
			t.Errorf("write search: %v", err)
		}
	})
	f.mux.HandleFunc("/repos/o/r/pulls/", detail)
	return f
}

func TestFetchMergedPullsVerifiesDetailActorAndTime(t *testing.T) {
	for _, tc := range []struct {
		name, login, at, status string
	}{
		{"other actor", "other", "", "empty"},
		{"missing actor", "", "2026-10-22T10:00:00Z", StatusIncomplete},
		{"missing time", statusTestUser, "", StatusIncomplete},
		{"invalid time", statusTestUser, "invalid", StatusIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			f := mergeDetailFixture(t, []int{12}, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/repos/o/r/pulls/12" || r.Header.Get("Authorization") != "Bearer synthetic-token" {
					t.Errorf("unexpected detail request path or authentication")
				}
				if err := json.NewEncoder(w).Encode(powMergeDetail(12, tc.at, tc.login)); err != nil {
					t.Errorf("write detail: %v", err)
				}
			})

			items, report := fetchMergedPulls(f.run(t), statusTestRepo, "o", "r", statusTestWindow, powWindowEnd())

			if calls != 1 || len(items) != 0 || report.Status != tc.status {
				t.Fatalf("calls=%d items=%v report=%+v, want one verification, no evidence, status %s", calls, items, report, tc.status)
			}
		})
	}
}

func TestFetchMergedPullsRetainsEvidenceOnDetailFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  int
		body  string
		calls int
	}{
		{"http", 500, "synthetic-token https://private.invalid/path", 3},
		{"decode", 200, `{"merged_by":`, 3},
		{"forbidden", 403, "synthetic-token https://private.invalid/path", 2},
		{"rate limited", 429, "synthetic-token https://private.invalid/path", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			f := mergeDetailFixture(t, []int{11, 12, 13}, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if strings.HasSuffix(r.URL.Path, "/12") {
					w.WriteHeader(tc.code)
					if _, err := fmt.Fprint(w, tc.body); err != nil {
						t.Errorf("write failure: %v", err)
					}
					return
				}
				number, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/repos/o/r/pulls/"))
				if err != nil {
					t.Errorf("invalid detail number: %v", err)
				}
				if err := json.NewEncoder(w).Encode(powMergeDetail(number, "2026-10-22T10:00:00Z", statusTestUser)); err != nil {
					t.Errorf("write detail: %v", err)
				}
			})

			items, report := fetchMergedPulls(f.run(t), statusTestRepo, "o", "r", statusTestWindow, powWindowEnd())

			if calls != tc.calls || len(items) != tc.calls-1 || report.Count != len(items) || report.Status != StatusIncomplete {
				t.Fatalf("calls=%d items=%v report=%+v, want retained successful evidence and incomplete coverage", calls, items, report)
			}
			if strings.Contains(report.Detail, "synthetic-token") || strings.Contains(report.Detail, "https://") {
				t.Fatalf("unsafe detail %q", report.Detail)
			}
			if tc.code != 200 && !strings.Contains(report.Detail, fmt.Sprintf("HTTP %d", tc.code)) {
				t.Fatalf("missing HTTP status in %q", report.Detail)
			}
			if tc.calls == 2 && !strings.Contains(report.Detail, "1 unique merge candidate(s) skipped") {
				t.Fatalf("missing skipped attribution count in %q", report.Detail)
			}
		})
	}
}

func TestFetchMergedPullsBoundsUniqueDetailRequests(t *testing.T) {
	numbers := []int{0, -1, 1, 1}
	for number := 2; number <= 110; number++ {
		numbers = append(numbers, number)
	}
	calls := 0
	requests := map[string]int{}
	f := mergeDetailFixture(t, numbers, func(w http.ResponseWriter, r *http.Request) {
		calls++
		requests[r.URL.Path]++
		if err := json.NewEncoder(w).Encode(powMergeDetail(calls, "2026-10-22T10:00:00Z", statusTestUser)); err != nil {
			t.Errorf("write detail: %v", err)
		}
	})

	items, report := fetchMergedPulls(f.run(t), statusTestRepo, "o", "r", statusTestWindow, powWindowEnd())

	if calls != 100 || len(items) != 100 || report.Count != 100 || report.Status != StatusIncomplete {
		t.Fatalf("calls=%d items=%d report=%+v, want 100 unique verifications and incomplete coverage", calls, len(items), report)
	}
	for path, count := range requests {
		if count != 1 {
			t.Errorf("detail %s requested %d times, want once", path, count)
		}
	}
	if !strings.Contains(report.Detail, "10 unique merge candidate(s) skipped") || !strings.Contains(report.Detail, "100-detail-request ceiling") {
		t.Fatalf("missing skipped count or ceiling in %q", report.Detail)
	}
}

func TestFetchMergedPullsRetainsHydratedEvidenceWhenSearchIncomplete(t *testing.T) {
	f := newPowMergeFixture(t, nil, powMergeSearchDoc(1, true,
		powMergeDetail(12, "2026-10-22T10:00:00Z", statusTestUser)))

	items, report := fetchMergedPulls(f.run(t), statusTestRepo, "o", "r", statusTestWindow, powWindowEnd())

	if len(items) != 1 || report.Count != 1 || report.Status != StatusIncomplete || !strings.Contains(report.Detail, "incomplete_results") {
		t.Fatalf("items=%v report=%+v, want verified merge with incomplete search coverage", items, report)
	}
}
