package timesheet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestRepoEventsStopsAtFullTerminalLinkPage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || r.URL.Path != "/repos/o/r/events" || r.URL.Query().Get("per_page") != "100" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if page > 3 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		if page < 3 {
			w.Header().Set("Link", fmt.Sprintf(`<https://api.github.com/repos/o/r/events?page=%d>; rel="next"`, page+1))
		} else {
			w.Header().Set("Link", `<https://api.github.com/repos/o/r/events?page=2>; rel="prev", <https://api.github.com/repos/o/r/events?page=1>; rel="first"`)
		}
		docs := make([]ghEventDoc, 100)
		for i := range docs {
			docs[i] = ghEventDoc{ID: strconv.Itoa((page-1)*100 + i), Type: "WatchEvent", Actor: &actor{Login: "alice"}, CreatedAt: "2026-10-27T12:00:00Z"}
		}
		if err := json.NewEncoder(w).Encode(docs); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	g := paginationTestRun(t, server)
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

	items, report := fetchRepoEvents(g, "o/r", "o", "r", start, start.AddDate(0, 0, 1))

	if requests != 3 || report.Status != StatusWindowed || report.Count != 300 || len(items) != 300 {
		t.Fatalf("requests=%d status=%s count=%d retained=%d; want 3, windowed, 300, 300", requests, report.Status, report.Count, len(items))
	}
}

func paginationTestRun(t *testing.T, server *httptest.Server) *githubRun {
	t.Helper()
	base, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	return &githubRun{scope: &githubScope{apiBase: base, token: "test-token", user: "alice"}, client: server.Client()}
}

func TestFamilyPaginationUsesLinkMetadataWithLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headers  []string
		first    int
		requests int
		count    int
	}{
		{"full terminal", []string{`<https://example.invalid/?page=1>; rel="prev first"`}, 100, 1, 100},
		{"short next", []string{`<https://example.invalid/?page=2>; rel="next"`}, 1, 2, 2},
		{"empty next", []string{`<https://example.invalid/?page=2>; rel="next"`}, 0, 2, 1},
		{"multiple headers", []string{`<https://example.invalid/?page=1>; rel="prev"`, `<https://example.invalid/?page=2>; rel="next last"`}, 1, 2, 2},
		{"no header full", nil, 100, 2, 101},
		{"no header short", nil, 1, 1, 1},
		{"malformed full", []string{`<https://example.invalid/?page=1>; rel="prev`}, 100, 2, 101},
		{"malformed short", []string{`<https://example.invalid/?page=1>; rel="prev`}, 1, 1, 1},
		{"unknown relation full", []string{`<https://example.invalid/>; rel="help"`}, 100, 2, 101},
		{"malformed after terminal", []string{`<https://example.invalid/>; rel="prev", broken`}, 100, 2, 101},
		{"invalid target", []string{`<https://%zz/>; rel="prev"`}, 100, 2, 101},
		{"missing relation", []string{`<https://example.invalid/>`}, 100, 2, 101},
		{"invalid relation token", []string{`<https://example.invalid/>; rel="prev @invalid"`}, 100, 2, 101},
		{"missing host", []string{`<https://>; rel="prev"`}, 100, 2, 101},
		{"garbage before parameters", []string{`<https://example.invalid/>garbage; rel="prev"`}, 100, 2, 101},
		{"anchored relation", []string{`<https://example.invalid/>; rel="prev"; anchor="https://other.invalid/unrelated"`}, 100, 2, 101},
		{"comma in target", []string{`<https://example.invalid/a,b>; rel="prev"; title="a,b"`}, 100, 1, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, count := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Query().Get("page") != strconv.Itoa(requests) || r.URL.Path != "/family" || r.URL.Query().Get("filter") != "kept" {
					t.Errorf("unexpected request %s", r.URL)
				}
				n := tc.first
				if requests == 1 {
					for _, header := range tc.headers {
						w.Header().Add("Link", header)
					}
				} else {
					n = 1
				}
				if err := json.NewEncoder(w).Encode(make([]int, n)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			g := paginationTestRun(t, server)

			walk := g.familyPageLoop(url.Values{"filter": {"kept"}}, "/family", 3, func(body []byte) (int, error) {
				var items []int
				err := json.Unmarshal(body, &items)
				count += len(items)
				return len(items), err
			})
			status, _ := familyStatus(walk, count, "test")

			if requests != tc.requests || count != tc.count || status != StatusComplete {
				t.Fatalf("requests=%d count=%d status=%s; want %d, %d, complete", requests, count, status, tc.requests, tc.count)
			}
		})
	}
}

func TestFamilyPaginationNeverFetchesLinkTarget(t *testing.T) {
	targetRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests++
		t.Error("untrusted Link target was fetched")
	}))
	defer target.Close()
	requests, decoded := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer test-token" || r.URL.Path != "/family" || r.URL.Query().Get("page") != strconv.Itoa(requests) {
			t.Errorf("unexpected original-origin request %s", r.URL)
		}
		if requests == 1 {
			w.Header().Set("Link", "<"+target.URL+`/steal?access_token=other>; rel="next"`)
		} else {
			w.Header().Set("Link", `<https://example.invalid/>; rel="prev"`)
		}
		if err := json.NewEncoder(w).Encode([]int{requests}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	g := paginationTestRun(t, server)

	walk := g.familyPageLoop(nil, "/family", 3, func(body []byte) (int, error) {
		var items []int
		err := json.Unmarshal(body, &items)
		decoded += len(items)
		return len(items), err
	})

	if requests != 2 || targetRequests != 0 || decoded != 2 || walk.err != nil {
		t.Fatalf("original=%d target=%d retained=%d error=%v; want 2, 0, 2, nil", requests, targetRequests, decoded, walk.err)
	}
}

func TestFamilyPaginationExplicitNextRemainsBounded(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Link", `<https://example.invalid/>; rel="next"`)
		if err := json.NewEncoder(w).Encode([]int{1}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	g := paginationTestRun(t, server)

	walk := g.familyPageLoop(nil, "/family", 2, func(body []byte) (int, error) {
		var items []int
		err := json.Unmarshal(body, &items)
		return len(items), err
	})
	status, _ := familyStatus(walk, 2, "test")

	if requests != 2 || status != StatusIncomplete {
		t.Fatalf("requests=%d status=%s; want 2, incomplete", requests, status)
	}
}

func TestRepoEventsOldTimestampsNeverOverrideCollectorCap(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		docs := make([]ghEventDoc, 100)
		for i := range docs {
			docs[i] = ghEventDoc{ID: strconv.Itoa(i), Type: "WatchEvent", Actor: &actor{Login: "alice"}, CreatedAt: "2026-10-26T12:00:00Z"}
		}
		if err := json.NewEncoder(w).Encode(docs); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	g := paginationTestRun(t, server)
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

	items, report := fetchRepoEvents(g, "o/r", "o", "r", start, start.AddDate(0, 0, 1))

	if requests != maxGithubFamilyPages || report.Status != StatusIncomplete || len(items) != 0 || report.Count != 0 {
		t.Fatalf("requests=%d status=%s retained=%d count=%d; want %d, incomplete, 0, 0", requests, report.Status, len(items), report.Count, maxGithubFamilyPages)
	}
}

func TestRepoEventsGenuine422AfterExplicitNextStaysIncomplete(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 2 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Link", `<https://example.invalid/?page=2>; rel="next"`)
		if err := json.NewEncoder(w).Encode([]ghEventDoc{{ID: "1", Type: "WatchEvent", Actor: &actor{Login: "alice"}, CreatedAt: "2026-10-27T12:00:00Z"}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	g := paginationTestRun(t, server)
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

	items, report := fetchRepoEvents(g, "o/r", "o", "r", start, start.AddDate(0, 0, 1))

	if requests != 2 || report.Status != StatusIncomplete || report.Count != 1 || len(items) != 1 {
		t.Fatalf("requests=%d status=%s count=%d retained=%d; want 2, incomplete, 1, 1", requests, report.Status, report.Count, len(items))
	}
}
