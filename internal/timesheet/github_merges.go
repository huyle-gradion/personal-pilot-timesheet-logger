package timesheet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxMergePRCandidates = 100

// pullMergeDoc is the authoritative GET /repos/{owner}/{repo}/pulls/{number}
// document. Search issue candidates do not provide merged_by.
type pullMergeDoc struct {
	Title    string `json:"title"`
	HTMLURL  string `json:"html_url"`
	MergedAt string `json:"merged_at"`
	MergedBy *actor `json:"merged_by"`
}

func fetchMergedPulls(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	query := url.Values{}
	query.Set("q", fmt.Sprintf("type:pr repo:%s merged:%s..%s", repo,
		windowStart.UTC().Format("2006-01-02"), windowEnd.Add(-time.Second).UTC().Format("2006-01-02")))
	query.Set("sort", "created")
	query.Set("order", "desc")
	var candidates []int
	seen := map[int]bool{}
	incomplete, total, unverified, skipped := false, 0, 0, 0
	decode := func(body []byte) (int, error) {
		var doc searchDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			return 0, fmt.Errorf("decode merge candidates: %w", err)
		}
		if doc.TotalCount > total {
			total = doc.TotalCount
		}
		incomplete = incomplete || doc.IncompleteResults
		for _, item := range doc.Items {
			if item.Number <= 0 {
				unverified++
				continue
			}
			if seen[item.Number] {
				continue
			}
			seen[item.Number] = true
			if len(candidates) == maxMergePRCandidates {
				skipped++
				continue
			}
			candidates = append(candidates, item.Number)
		}
		return len(doc.Items), nil
	}
	walk := g.familyPageLoop(query, "/search/issues", maxSearchResultPages, decode)
	var items []EvidenceItem
	failures, failureDetail := 0, ""
	blocked := walk.httpStatus == http.StatusForbidden || walk.httpStatus == http.StatusTooManyRequests
	for i, number := range candidates {
		if blocked {
			skipped += len(candidates) - i
			break
		}
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, name, number)
		response, err := g.getJSON(nil, path)
		if err != nil || response.status != http.StatusOK {
			failures++
			if failureDetail == "" {
				failureDetail = "the PR detail request could not be completed"
				if err == nil {
					failureDetail = fmt.Sprintf("the PR detail provider responded with HTTP %d", response.status)
				}
			}
			blocked = response.status == http.StatusForbidden || response.status == http.StatusTooManyRequests
			continue
		}
		var doc pullMergeDoc
		if err := json.Unmarshal(response.body, &doc); err != nil {
			failures++
			if failureDetail == "" {
				failureDetail = "the PR detail response was not the expected document schema"
			}
			continue
		}
		if doc.MergedBy == nil || strings.TrimSpace(doc.MergedBy.Login) == "" {
			unverified++
			continue
		}
		if doc.MergedBy.Login != g.scope.user {
			continue
		}
		at, err := time.Parse(time.RFC3339, doc.MergedAt)
		if err != nil {
			unverified++
			continue
		}
		if at.Before(windowStart) || !at.Before(windowEnd) {
			continue
		}
		items = append(items, EvidenceItem{
			Ref: fmt.Sprintf("gh:%s:pr-merge:%d", repo, number), Kind: "merged_pull_request",
			Actor: doc.MergedBy.Login, Time: at.UTC().Format(time.RFC3339), Repo: repo,
			Resource: fmt.Sprintf("pr:%s:%d", repo, number), URL: doc.HTMLURL,
			Title: sanitizerText(doc.Title), Provenance: fmt.Sprintf("search/issues,repos/pulls/%d", number),
		})
	}
	n := len(items)
	status, detail := familyStatus(walk, n, "merged-pull-request search")
	var bounds []string
	if incomplete {
		bounds = append(bounds, "the search provider reported incomplete_results within its documented time/runtime bound")
	}
	if total > 1000 {
		bounds = append(bounds, "the search result total exceeds the documented 1,000-result bound")
	}
	if skipped > 0 {
		bounds = append(bounds, fmt.Sprintf("%d unique merge candidate(s) skipped under the %d-detail-request ceiling or after provider denial/rate limiting; attribution remains unverified", skipped, maxMergePRCandidates))
	}
	if failures > 0 {
		bounds = append(bounds, fmt.Sprintf("%d PR detail request(s) failed; %s; attribution remains unverified", failures, failureDetail))
	}
	if unverified > 0 {
		bounds = append(bounds, fmt.Sprintf("%d merged pull request search result(s) had no positive PR number or verifiable detail merged_by login or merged_at instant; omitted rather than guessed", unverified))
	}
	if len(bounds) > 0 {
		if status != StatusFailed {
			status = StatusIncomplete
		}
		detail = niceJoin(detail, strings.Join(bounds, "; "))
	}
	return items, FamilyReport{Family: "merged_pull_requests", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
}
