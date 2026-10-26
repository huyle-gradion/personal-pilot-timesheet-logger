package timesheet

// github.go implements the configured-user GitHub activity collector for an
// opened day. It combines seven REST endpoint families and never reduces
// coverage to a single event feed.
//
// Verified API inventory (official GitHub REST API documentation at
// docs.github.com, apiVersion 2022-11-28, checked 2026-10-22):
//
//   1. Commits — GET /repos/{owner}/{repo}/commits with author (username or
//      email match), since/until ISO 8601 timestamps, per_page max 100, page
//      pagination (REST commits, "List commits",
//      https://docs.github.com/en/rest/commits/commits#list-commits).
//      Every returned commit is verified against the provider-named actor in
//      the response itself: a commit whose GitHub user document names a
//      different login is dropped even when a query filter returned it. A
//      commit with no user document is omitted because a commit-author email
//      is a filter value, not proof of the configured user's ownership; the
//      omission is reported as a visible coverage limitation (F6).
//   2. Authored pull requests — GET /search/issues with the qualifiers
//      type:pr, author:{user}, repo:{owner}/{repo}, created:{date}..{date}
//      (REST search, "Search issues and pull requests",
//      https://docs.github.com/en/rest/search/search#search-issues-and-pull-requests);
//      the search API returns at most 1,000 results, may return
//      incomplete_results:true on timeout, and is rate-limited to 30
//      requests/minute authenticated. Because `created:` is date-granular,
//      the collector over-fetches the UTC date range of the day window and
//      attributes each result by its exact created_at instant and its
//      provider-named user.login.
//   3. Merged pull requests — GET /search/issues with the qualifiers type:pr,
//      repo:{owner}/{repo}, merged:{date}..{date}; `merged:` is the documented
//      merge-date qualifier and there is no `merged-by:` qualifier, so the
//      merger is verified client-side. Each result is kept only when
//      merged_by.login names the configured user and the exact merged_at
//      instant falls in the day's [start, end) window; over-fetched results
//      merged by another actor or outside the window are not attributed.
//      The search bound handling (1,000 results, incomplete_results, page
//      ceiling) matches the authored-PR family.
//   4. Submitted PR reviews — candidate pull requests come from
//      GET /search/issues with type:pr, reviewed-by:{user}, repo:{repo}; no
//      date qualifier is used because GitHub's `updated` qualifier is when the
//      resource was last updated, not when a review was submitted, and a PR
//      reviewed in the window and updated later would be excluded. The
//      candidate set is therefore a superset independent of later resource
//      updates. Each candidate is verified with
//      GET /repos/{owner}/{repo}/pulls/{pull_number}/reviews ("List reviews
//      for a pull request",
//      https://docs.github.com/en/rest/pulls/reviews#list-reviews-for-a-pull-request),
//      keeping reviews whose reviewer user.login is the configured user and
//      whose submitted_at falls in the window, with the provider-named
//      review state (APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED).
//      A search that lists a candidate without the verification call is
//      never attributed. Search totals above the documented 1,000-result
//      bound, the search incomplete_results flag, the page ceiling, more
//      candidate pull requests than the collector's maxReviewPRCandidates
//      bound, or a per-review pagination beyond the ceiling make the family
//      incomplete, never silently exhausted. The search families run under
//      the dedicated maxSearchResultPages ceiling (maxSearchResultPages in
//      model.go), whose pairing with these families is documented there: the
//      search endpoint's pagination semantics differ from the REST families.
//   5. PR review comments — GET /repos/{owner}/{repo}/pulls/comments
//      (repository-wide review comments) with page pagination; attribution
//      requires the comment's user.login to be the configured user (REST
//      pulls, "List review comments in a repository"),
//      https://docs.github.com/en/rest/pulls/comments#list-review-comments-in-a-repository.
//   6. Issue activity/comments — GET /repos/{owner}/{repo}/issues/comments
//      with since, page pagination; attributable comments filtered by
//      user.login (REST issues, "List issue comments for a repository"),
//      https://docs.github.com/en/rest/issues/comments#list-issue-comments-for-a-repository.
//      That document carries no pull_request field, so each comment target is
//      resolved once per run with the documented issue document
//      (GET /repos/{owner}/{repo}/issues/{n}, whose pull_request key is the
//      discriminator). The lookups are bounded per repository per attempt;
//      an unverified target stays an issue comment and the family reports
//      incomplete with the count, never a guess.
//   7. Discussions and other attributable repository interactions —
//      GET /repos/{owner}/{repo}/events with per_page max 100 and page
//      pagination (REST activity events doc,
//      https://docs.github.com/en/rest/activity/events#list-repository-events).
//      GitHub documents the shared event timeline cap: the timeline includes
//      up to 300 events and only events created within the past 30 days;
//      the collector therefore treats the feed as bounded coverage, never
//      completeness. Its FamilyReport steady state is the dedicated
//      `windowed` status (StatusWindowed): a fully-walked feed is bounded by
//      the provider's design, not by the collector, and never reads as a
//      collection limitation in the aggregate. A 422 on the page past the
//      retained window is the documented end of that window: it reports
//      `incomplete` (bounded provider coverage, retained count kept, a
//      sanitized status+body diagnostic recorded), never `failed` — `failed`
//      stays reserved for 403/429/transport/permission failures.
//
// Canonical identity: the same provider action seen through two endpoint
// families carries one event identity and its provenance is merged — issue
// comment gh:{repo}:issue-comment:{id} (issues/comments and events),
// PR review comment gh:{repo}:pr-review-comment:{id} (pulls/comments and
// events), submitted review gh:{repo}:review:{id} (pulls/{n}/reviews and
// events), merge gh:{repo}:pr-merge:{number} (search/issues and events).
// Distinct provider actions keep distinct identities and are never
// merged. Topics group by retained provider resource identity (pull request,
// issue, discussion numbers taken from provider payload/resource fields),
// never from rendered display strings.
//
// Bounds surfaced when hit, named per family and repository: the collector's
// page ceilings (maxGithubFamilyPages for REST families, maxSearchResultPages
// for the three search families), the events feed's 30-day/300-event retention,
// the submitted-review candidate ceiling, the issue-comment PR-discriminator
// lookup ceiling, search incomplete_results and the 1,000-result limit, HTTP
// 403/429 rate limiting, permission failures, topic item caps, and undecodable
// responses. A nonempty response is never treated as complete coverage.
//
// Attribution: items are kept only when the provider names the configured
// user as the acting identity at the event itself (commit GitHub user
// document, the search `author:` qualifier plus user.login, merged_by.login
// for a merge, comment user.login, event actor.login, verified review
// user.login). A commit-author email is never treated as verified ownership;
// an item whose ownership cannot be established is omitted with a reported
// coverage limitation. A resource's latest update by another actor is never
// claimed as the user's activity.
// Requests carry the configured GITHUB_TOKEN as a Bearer credential; the token
// value never appears in errors, diagnostics, state, or reports, and request
// URLs are never echoed.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type collectGithubResult struct {
	Status   string
	Detail   string
	Families []FamilyReport
	Topics   []TopicCandidate
}

// githubScope bundles the non-secret per-run collection inputs.
type githubScope struct {
	user    string
	repos   []string
	apiBase *url.URL
	token   string
}

// collectGithub gathers attributable activity for the repository set and one
// recorded day window.
func collectGithub(scope *githubScope, windowStart, windowEnd time.Time) collectGithubResult {
	if strings.TrimSpace(scope.token) == "" {
		return collectGithubResult{
			Status: StatusMissingCredentials,
			Detail: "GITHUB_TOKEN is not set or is empty",
		}
	}
	run := &githubRun{scope: scope, client: &http.Client{Timeout: collectHTTPTimeout}}

	var allItems []EvidenceItem
	var families []FamilyReport
	for _, repo := range scope.repos {
		items, fams := collectRepo(run, repo, windowStart, windowEnd)
		allItems = append(allItems, items...)
		families = append(families, fams...)
	}
	// The aggregate count names only genuinely limited or failed families.
	// "complete" and "empty" are fully-walked coverage, and "windowed" is the
	// repo_events timeline's documented steady state — bounded by the
	// provider's design (only the past 30 days, up to 300 events), not a
	// collection limitation — so a fully collected day reads complete and the
	// detail never counts a windowed family against the day.
	bounded := 0
	for _, fam := range families {
		if fam.Status == StatusIncomplete || fam.Status == StatusFailed {
			bounded++
		}
	}
	topics := buildGithubTopics(allItems)
	overflowKept, overflowLost := 0, 0
	for _, t := range topics {
		overflowKept += len(t.OverflowItems)
		overflowLost += t.DroppedItems
	}
	status, detail := StatusComplete, ""
	if bounded > 0 {
		status = StatusIncomplete
		detail = fmt.Sprintf("%d of %d family queries reported limited or failed coverage", bounded, len(families))
	} else {
		detail = fmt.Sprintf("%d attributable item(s) across %d queried repositories", len(allItems), len(scope.repos))
	}
	if overflowKept > 0 || overflowLost > 0 {
		// The topic item cap bounds the AI-visible draft, not the evidence:
		// items beyond it are retained durably as topic overflow up to the
		// fixed ceilings, and anything beyond those ceilings is a real loss
		// counted here and on the affected topics, never a silent no-op (F6).
		// The source stays incomplete either way: the draft still shows only
		// the first maxTopicItems items per topic.
		status = StatusIncomplete
		if overflowLost > 0 {
			detail = niceJoin(detail, fmt.Sprintf(
				"%d attributable item(s) beyond the %d-item topic cap: %d retained durably as topic overflow, %d beyond the overflow caps are not retained; the affected topic(s) record the counts",
				overflowKept+overflowLost, maxTopicItems, overflowKept, overflowLost))
		} else {
			detail = niceJoin(detail, fmt.Sprintf(
				"%d attributable item(s) beyond the %d-item topic cap are retained durably as topic overflow; the draft still shows the first %d items per topic",
				overflowKept, maxTopicItems, maxTopicItems))
		}
	}
	return collectGithubResult{
		Status:   status,
		Detail:   detail,
		Families: families,
		Topics:   topics,
	}
}

type githubRun struct {
	scope  *githubScope
	client *http.Client
	// issueTargets caches per-run successful issue-document resolutions keyed
	// by "{repo}#{number}", so the issue-comment family can tell a pull request
	// conversation comment from an issue comment with one lookup per distinct
	// number for the whole run. Only conclusive answers are cached; a failed
	// lookup is retried for later comments under the same lookup ceiling.
	issueTargets map[string]issueTarget
}

// issueTarget is one cached issue-document resolution: whether the number is a
// pull request. Only successful, decoded lookups are cached.
type issueTarget struct {
	isPR bool
}

// issueLookupBudget is one issue_comments family attempt's lookup accounting
// for one repository. lookups is bounded by maxIssueTargetLookups;
// failedComments and boundedComments count the retained comments left
// unclassified, so the family detail states honest counts rather than guesses.
type issueLookupBudget struct {
	lookups         int
	failedComments  int
	boundedComments int
}

// issueTargetFor answers whether {owner}/{name}#{number} is a pull request by
// reading the documented issue document, whose response carries a
// "pull_request" key only for a pull request. A cache hit returns without a
// request. At the collector's maxIssueTargetLookups ceiling (per repository
// per attempt) it returns known=false, bounded=true immediately, so a large
// comment family never turns into an unbounded request fan-out. A non-200
// response, transport error, or decode failure returns known=false,
// bounded=false; it never fails the family. The caller keeps such items
// classified as issue comments and reports the counts as incomplete.
func (g *githubRun) issueTargetFor(repo, owner, name string, number int, budget *issueLookupBudget) (isPR, known, bounded bool) {
	key := fmt.Sprintf("%s#%d", repo, number)
	if target, ok := g.issueTargets[key]; ok {
		return target.isPR, true, false
	}
	if budget.lookups >= maxIssueTargetLookups {
		return false, false, true
	}
	budget.lookups++
	target := issueTarget{}
	known = false
	status, body, err := g.getJSON(nil, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, name, number))
	if err == nil && status == http.StatusOK {
		var doc struct {
			PullRequest *json.RawMessage `json:"pull_request"`
		}
		if json.Unmarshal(body, &doc) == nil {
			target = issueTarget{isPR: doc.PullRequest != nil}
			known = true
		}
	}
	if known {
		if g.issueTargets == nil {
			g.issueTargets = map[string]issueTarget{}
		}
		g.issueTargets[key] = target
	}
	return target.isPR, known, false
}

// getJSON performs one authenticated GET and returns the HTTP status and, for
// 200, the size-capped body. Errors are sanitized: request URLs are never
// echoed and credential values never appear anywhere.
func (g *githubRun) getJSON(q url.Values, path string) (int, []byte, error) {
	endpoint := *g.scope.apiBase
	endpoint.Path += strings.TrimPrefix(strings.TrimSuffix(path, "/"), "/")
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("request could not be constructed")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+g.scope.token)
	resp, err := g.client.Do(req)
	if err != nil {
		// Sanitized: transport error detail can embed the URL.
		return 0, nil, fmt.Errorf("the provider request could not be sent")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxGithubBodyBytes))
		if err != nil {
			return resp.StatusCode, nil, fmt.Errorf("the provider response could not be read")
		}
		return resp.StatusCode, body, nil
	}
	// A bounded copy of the non-200 body is returned so the stop-reason
	// classification can render a sanitized diagnostic (HTTP status plus a
	// bounded body snippet). The snippet is sanitized and URL-stripped where
	// it is rendered (sanitizeHTTPDiagnostic); request URLs are never echoed
	// and credential values never appear anywhere.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return resp.StatusCode, nil, nil
	}
	return resp.StatusCode, body, nil
}

// --- provider document shapes ------------------------------------------------

type actor struct {
	Login string `json:"login"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type commitDoc struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  *struct {
		Author *struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Date  string `json:"date"`
		} `json:"author"`
		Message string `json:"message"`
	} `json:"commit"`
	Author *actor `json:"author"`
}

type searchDoc struct {
	TotalCount        int          `json:"total_count"`
	IncompleteResults bool         `json:"incomplete_results"`
	Items             []searchItem `json:"items"`
}

type searchItem struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	MergedAt  string `json:"merged_at"`
	MergedBy  *actor `json:"merged_by"`
	HTMLURL   string `json:"html_url"`
	User      *actor `json:"user"`
}

type commentDoc struct {
	ID             int    `json:"id"`
	CreatedAt      string `json:"created_at"`
	HTMLURL        string `json:"html_url"`
	IssueURL       string `json:"issue_url"`
	PullRequestURL string `json:"pull_request_url"`
	Body           string `json:"body"`
	User           *actor `json:"user"`
}

// reviewDoc is the documented shape of one submitted pull request review
// from GET /repos/{owner}/{repo}/pulls/{pull_number}/reviews.
type reviewDoc struct {
	ID          int    `json:"id"`
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
	HTMLURL     string `json:"html_url"`
	User        *actor `json:"user"`
}

type ghEventDoc struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Actor     *actor            `json:"actor"`
	CreatedAt string            `json:"created_at"`
	Payload   ghEventPayloadDoc `json:"payload"`
}

type ghEventPayloadDoc struct {
	Action     string `json:"action"`
	Discussion *struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"discussion"`
	Comment *struct {
		ID      int    `json:"id"`
		HTMLURL string `json:"html_url"`
	} `json:"comment"`
	PullRequest *struct {
		Number   int    `json:"number"`
		Title    string `json:"title"`
		Merged   bool   `json:"merged"`
		MergedAt string `json:"merged_at"`
		MergedBy *actor `json:"merged_by"`
		HTMLURL  string `json:"html_url"`
	} `json:"pull_request"`
	Issue *struct {
		Number int `json:"number"`
	} `json:"issue"`
	Review *struct {
		ID      int    `json:"id"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
	} `json:"review"`
}

// wthin collectors' output assembly

var issueNumberTailRE = regexp.MustCompile(`/issues/(\d+)$`)
var pullNumberTailRE = regexp.MustCompile(`/pulls/(\d+)$`)

// familyWalk records how one paged family walk ended. Each page has
// per_page=100; stop is finished, empty, http, decode, transport, or capped.
// A finished page (fewer than 100 entries) is the only provable end. For an
// http stop, httpStatus carries the provider status and httpBody a bounded
// copy of the provider error body (rendered only through
// sanitizeHTTPDiagnostic, never echoed raw). maxPages records the ceiling the
// walk ran under, so the capped detail names the real bound.
type familyWalk struct {
	pages      int
	stop       string
	httpStatus int
	httpBody   []byte
	err        error
	maxPages   int
}

// familyPageLoop walks one paged family under the given page ceiling.
func (g *githubRun) familyPageLoop(query url.Values, path string, maxPages int, decode func(body []byte) (int, error)) familyWalk {
	walk := familyWalk{maxPages: maxPages}
	for page := 1; page <= maxPages; page++ {
		q := cloneValues(query)
		q.Set("per_page", "100")
		q.Set("page", strconv.Itoa(page))
		status, body, err := g.getJSON(q, path)
		if err != nil {
			walk.stop, walk.err = "transport", err
			return walk
		}
		if status != http.StatusOK {
			walk.stop = "http"
			walk.httpStatus = status
			walk.httpBody = body
			walk.err = fmt.Errorf("provider responded with HTTP %d", status)
			return walk
		}
		n, derr := decode(body)
		if derr != nil {
			walk.stop = "decode"
			walk.err = fmt.Errorf("the provider response was not the expected document schema")
			return walk
		}
		if n == 0 {
			walk.stop = "empty"
			return walk
		}
		if n < 100 {
			walk.stop = "finished"
			return walk
		}
		walk.pages++
	}
	walk.stop = "capped"
	return walk
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		for _, x := range vs {
			out.Add(k, x)
		}
	}
	return out
}

// --- the families ------------------------------------------------------------

func collectRepo(g *githubRun, repo string, windowStart, windowEnd time.Time) ([]EvidenceItem, []FamilyReport) {
	var items []EvidenceItem
	reports := make([]FamilyReport, 0, 7)
	owner, name, _ := strings.Cut(repo, "/")

	commits, rep := fetchCommits(g, repo, owner, name, windowStart, windowEnd)
	items = append(items, commits...)
	reports = append(reports, rep)

	pulls, rep := fetchAuthoredPulls(g, repo, owner, name, windowStart, windowEnd)
	items = append(items, pulls...)
	reports = append(reports, rep)

	merges, rep := fetchMergedPulls(g, repo, owner, name, windowStart, windowEnd)
	items = append(items, merges...)
	reports = append(reports, rep)

	reviews, rep := fetchSubmittedReviews(g, repo, owner, name, windowStart, windowEnd)
	items = append(items, reviews...)
	reports = append(reports, rep)

	reviewComments, rep := fetchReviewComments(g, repo, owner, name, windowStart, windowEnd)
	items = append(items, reviewComments...)
	reports = append(reports, rep)

	issueComments, rep := fetchIssueComments(g, repo, owner, name, windowStart, windowEnd)
	items = append(items, issueComments...)
	reports = append(reports, rep)

	feed, rep := fetchRepoEvents(g, repo, owner, name, windowStart, windowEnd)
	items = append(items, feed...)
	reports = append(reports, rep)

	return items, reports
}

// --- collectors --------------------------------------------------------------

// --- collectors --------------------------------------------------------------

func fetchCommits(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	since := windowStart.UTC().Format(time.RFC3339)
	until := windowEnd.UTC().Format(time.RFC3339)
	query := url.Values{
		"author": {g.scope.user},
		"since":  {since},
		"until":  {until},
	}
	path := "/repos/" + owner + "/" + name + "/commits"
	var items []EvidenceItem
	unverified := 0
	decode := func(body []byte) (int, error) {
		var docs []commitDoc
		if err := json.Unmarshal(body, &docs); err != nil {
			return 0, err
		}
		for _, doc := range docs {
			if doc.Commit == nil || doc.Commit.Author == nil || doc.Commit.Author.Date == "" {
				continue
			}
			at, err := time.Parse(time.RFC3339, doc.Commit.Author.Date)
			if err != nil || !at.Before(windowEnd) || !at.After(windowStart.Add(-time.Second)) {
				continue
			}
			// Verify the provider-named actor (F6): only the response's GitHub
			// user document establishes that the configured user performed the
			// commit. A commit-author email is a server-side filter value, not
			// proof of ownership, so an email alone is never retained; the
			// omission is reported as a visible coverage limitation instead.
			if doc.Author == nil || strings.TrimSpace(doc.Author.Login) == "" {
				unverified++
				continue
			}
			if doc.Author.Login != g.scope.user {
				continue
			}
			items = append(items, EvidenceItem{
				Ref:        "gh:commit:" + doc.SHA,
				Kind:       "commit",
				Actor:      doc.Author.Login,
				Time:       at.UTC().Format(time.RFC3339),
				Repo:       repo,
				URL:        doc.HTMLURL,
				Title:      sanitizerText(firstLine(doc.Commit.Message)),
				Provenance: "repos/commits",
			})
		}
		return len(docs), nil
	}
	walk := g.familyPageLoop(query, path, maxGithubFamilyPages, decode)
	n := len(items)
	status, detail := familyStatus(walk, n, "commit")
	if unverified > 0 && status != StatusFailed {
		status = StatusIncomplete
		detail = niceJoin(detail, fmt.Sprintf(
			"%d commit(s) in the day's window carried no GitHub user document; their authorship cannot be verified as the configured user, so they were omitted rather than attributed by commit-author email", unverified))
	}
	return items, FamilyReport{Family: "commits", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
}

func fetchAuthoredPulls(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	dayFrom := windowStart.UTC().Format("2006-01-02")
	dayTo := windowEnd.Add(-time.Second).UTC().Format("2006-01-02")
	query := url.Values{}
	query.Set("q", fmt.Sprintf("type:pr author:%s repo:%s created:%s..%s", g.scope.user, repo, dayFrom, dayTo))
	query.Set("sort", "created")
	query.Set("order", "desc")
	path := "/search/issues"
	var items []EvidenceItem
	incomplete := false
	total := 0
	decode := func(body []byte) (int, error) {
		var doc searchDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			return 0, err
		}
		total = doc.TotalCount
		if doc.IncompleteResults {
			incomplete = true
		}
		for _, item := range doc.Items {
			at, err := time.Parse(time.RFC3339, item.CreatedAt)
			if err != nil || !at.Before(windowEnd) || !at.After(windowStart.Add(-time.Second)) {
				continue
			}
			if item.User == nil || item.User.Login != g.scope.user {
				continue
			}
			items = append(items, EvidenceItem{
				Ref:        fmt.Sprintf("gh:%s:pr:%d", repo, item.Number),
				Kind:       "authored_pull_request",
				Actor:      item.User.Login,
				Time:       at.UTC().Format(time.RFC3339),
				Repo:       repo,
				Resource:   fmt.Sprintf("pr:%s:%d", repo, item.Number),
				URL:        item.HTMLURL,
				Title:      sanitizerText(item.Title),
				Provenance: "search/issues",
			})
		}
		return len(doc.Items), nil
	}
	// The search endpoint's pagination semantics differ from the REST
	// families (documented 1,000-result bound, incomplete_results flag), so
	// this family runs under the dedicated maxSearchResultPages ceiling; the
	// pairing is documented at the constant in model.go.
	walk := g.familyPageLoop(query, path, maxSearchResultPages, decode)
	n := len(items)
	status, detail := familyStatus(walk, n, "authored-pull-request search")
	if incomplete {
		status = StatusIncomplete
		detail = niceJoin(detail, "the search provider reported incomplete_results within its documented time/runtime bound")
	}
	if total > 1000 {
		status = StatusIncomplete
		detail = niceJoin(detail, "the search result total exceeds the documented 1,000-result bound")
	}
	if status == StatusComplete && n == 0 {
		status = "empty"
	}
	return items, FamilyReport{Family: "authored_pull_requests", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
}

// fetchMergedPulls is family 3: pull requests merged by the configured user.
// `merged:` is the documented search qualifier for the merge date; there is no
// merged-by: qualifier, so the merger is verified client-side from each
// result's merged_by.login and the exact merged_at instant is checked against
// the day's [start, end) window. A nil merged_by or an empty/unparsable
// merged_at cannot establish the merger or the instant and is counted as
// unverified in the detail, never attributed. A result whose merged_by names
// another actor, or whose merged_at falls outside the window, is over-fetched
// noise from the date-granular qualifier and is not attributed. Search bounds
// (1,000 results, incomplete_results, page ceiling) match the authored-PR
// family.
func fetchMergedPulls(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	dayFrom := windowStart.UTC().Format("2006-01-02")
	dayTo := windowEnd.Add(-time.Second).UTC().Format("2006-01-02")
	query := url.Values{}
	query.Set("q", fmt.Sprintf("type:pr repo:%s merged:%s..%s", repo, dayFrom, dayTo))
	query.Set("sort", "created")
	query.Set("order", "desc")
	path := "/search/issues"
	var items []EvidenceItem
	incomplete := false
	total := 0
	unverified := 0
	decode := func(body []byte) (int, error) {
		var doc searchDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			return 0, err
		}
		total = doc.TotalCount
		if doc.IncompleteResults {
			incomplete = true
		}
		for _, item := range doc.Items {
			// `merged:` is date-granular in UTC, so the query over-fetches the
			// day's date range; attribution requires the provider-named
			// merger and the exact merged_at instant inside the window.
			// Another actor's merge is over-fetched noise, not a coverage
			// limitation, so it is dropped without counting it unverified.
			if item.MergedBy == nil || strings.TrimSpace(item.MergedBy.Login) == "" {
				unverified++
				continue
			}
			if item.MergedBy.Login != g.scope.user {
				continue
			}
			at, err := time.Parse(time.RFC3339, item.MergedAt)
			if err != nil {
				unverified++
				continue
			}
			// merged_at is full-precision RFC3339, unlike the comment
			// families' since-truncated query bounds, so the window is exact
			// and half-open: no one-second slop at the start.
			if at.Before(windowStart) || !at.Before(windowEnd) {
				continue
			}
			items = append(items, EvidenceItem{
				Ref:        fmt.Sprintf("gh:%s:pr-merge:%d", repo, item.Number),
				Kind:       "merged_pull_request",
				Actor:      item.MergedBy.Login,
				Time:       at.UTC().Format(time.RFC3339),
				Repo:       repo,
				Resource:   fmt.Sprintf("pr:%s:%d", repo, item.Number),
				URL:        item.HTMLURL,
				Title:      sanitizerText(item.Title),
				Provenance: "search/issues",
			})
		}
		return len(doc.Items), nil
	}
	// Same search-endpoint pairing as fetchAuthoredPulls: the merged-PR
	// search runs under maxSearchResultPages, documented at the constant.
	walk := g.familyPageLoop(query, path, maxSearchResultPages, decode)
	n := len(items)
	status, detail := familyStatus(walk, n, "merged-pull-request search")
	if incomplete {
		status = StatusIncomplete
		detail = niceJoin(detail, "the search provider reported incomplete_results within its documented time/runtime bound")
	}
	if total > 1000 {
		status = StatusIncomplete
		detail = niceJoin(detail, "the search result total exceeds the documented 1,000-result bound")
	}
	if unverified > 0 && status != StatusFailed {
		status = StatusIncomplete
		detail = niceJoin(detail, fmt.Sprintf(
			"%d merged pull request search result(s) carried no verifiable merged_by login or merged_at instant; the merger could not be attributed, so they were omitted rather than guessed", unverified))
	}
	if status == StatusComplete && n == 0 {
		status = "empty"
	}
	return items, FamilyReport{Family: "merged_pull_requests", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
}

// fetchSubmittedReviews is family 4: submitted pull request reviews by the
// configured user (F6). Candidate pull requests are a deliberate superset:
// every PR in the repository the user has reviewed, independent of when the
// resource was last updated, because GitHub's search `updated` qualifier means
// "last updated", not "review submitted" — a PR reviewed inside the window and
// touched later would otherwise be excluded. Each candidate's reviews are
// verified against the documented reviews endpoint and filtered by the exact
// `submitted_at` instant before attribution, so a later resource update never
// supplies or suppresses a selected-day review. Search bounds, the candidate
// ceiling and the page ceiling make the family incomplete, never silently
// exhausted.
func fetchSubmittedReviews(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	query := url.Values{}
	query.Set("q", fmt.Sprintf("type:pr reviewed-by:%s repo:%s", g.scope.user, repo))
	query.Set("sort", "updated")
	query.Set("order", "desc")

	var items []EvidenceItem
	var bounds []string
	var candidates []searchItem
	candTruncated := false
	sawSearchIncomplete := false
	searchCap := false
	searchTotal := 0
	decode := func(body []byte) (int, error) {
		var doc searchDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			return 0, err
		}
		searchTotal = doc.TotalCount
		if doc.IncompleteResults {
			sawSearchIncomplete = true
		}
		for _, item := range doc.Items {
			if len(candidates) >= maxReviewPRCandidates {
				candTruncated = true
				break
			}
			if item.Number == 0 {
				continue
			}
			candidates = append(candidates, item)
		}
		return len(doc.Items), nil
	}
	// Same search-endpoint pairing as fetchAuthoredPulls: the candidate
	// search runs under maxSearchResultPages, documented at the constant.
	searchWalk := g.familyPageLoop(query, "/search/issues", maxSearchResultPages, decode)
	if searchWalk.err != nil {
		return nil, FamilyReport{Family: "submitted_pr_reviews", QueriedRepo: repo, Count: 0, Status: StatusFailed, Detail: truncateRunes(searchWalk.err.Error(), maxDetailRunes)}
	}
	if searchWalk.stop == "capped" {
		searchCap = true
	}
	if candTruncated {
		bounds = append(bounds, fmt.Sprintf(
			"the candidate search retained only the first %d pull request(s); the remaining candidate(s) were not verified for submitted reviews", maxReviewPRCandidates))
	}
	if searchCap {
		bounds = append(bounds, fmt.Sprintf(
			"the candidate search reached the collector's %d-page pagination ceiling; more candidate pages exist", maxSearchResultPages))
	}
	if sawSearchIncomplete {
		bounds = append(bounds, "the candidate search reported incomplete_results within its documented time/runtime bound")
	}
	if searchTotal > 1000 {
		bounds = append(bounds, "the candidate search total exceeds the documented 1,000-result bound")
	}

	reviewCapped := false
	for _, cand := range candidates {
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, name, cand.Number)
		decode := func(body []byte) (int, error) {
			var docs []reviewDoc
			if err := json.Unmarshal(body, &docs); err != nil {
				return 0, err
			}
			for _, doc := range docs {
				if doc.User == nil || doc.User.Login != g.scope.user {
					continue
				}
				at, err := time.Parse(time.RFC3339, doc.SubmittedAt)
				if err != nil || !at.Before(windowEnd) || !at.After(windowStart.Add(-time.Second)) {
					continue
				}
				state := strings.TrimSpace(doc.State)
				if state == "" || doc.ID == 0 {
					continue
				}
				items = append(items, EvidenceItem{
					Ref:        fmt.Sprintf("gh:%s:review:%d", repo, doc.ID),
					Kind:       "submitted_pr_review",
					Actor:      doc.User.Login,
					Time:       at.UTC().Format(time.RFC3339),
					Repo:       repo,
					Resource:   fmt.Sprintf("pr:%s:%d", repo, cand.Number),
					URL:        doc.HTMLURL,
					Title:      sanitizerText(fmt.Sprintf("pull request #%d review (%s)", cand.Number, strings.ToLower(state))),
					Provenance: fmt.Sprintf("repos/pulls/%d/reviews", cand.Number),
				})
			}
			return len(docs), nil
		}
		walk := g.familyPageLoop(url.Values{}, path, maxGithubFamilyPages, decode)
		if walk.err != nil {
			// Reviews verified before this candidate's request failed are real
			// attributable evidence; retain them and keep the honest failed
			// status. The detail must not imply the remaining candidates were
			// verified (F7).
			detail := niceJoin(walk.err.Error(), fmt.Sprintf(
				"%d submitted review(s) retained before the failure; remaining review coverage is unverified", len(items)))
			return items, FamilyReport{Family: "submitted_pr_reviews", QueriedRepo: repo, Count: len(items), Status: StatusFailed, Detail: truncateRunes(detail, maxDetailRunes)}
		}
		if walk.stop == "capped" {
			reviewCapped = true
		}
	}
	if reviewCapped {
		bounds = append(bounds, fmt.Sprintf(
			"at least one candidate pull request's review list reached the collector's %d-page ceiling; coverage for submitted reviews is bounded", maxGithubFamilyPages))
	}

	n := len(items)
	if len(bounds) > 0 {
		return items, FamilyReport{Family: "submitted_pr_reviews", QueriedRepo: repo, Count: n, Status: StatusIncomplete, Detail: strings.Join(bounds, "; ")}
	}
	status := StatusComplete
	detail := fmt.Sprintf("%d submitted review(s) retained; %d candidate pull request(s) from the update-independent reviewed-by superset verified with the reviews endpoint and filtered by submitted_at; pagination exhausted", n, len(candidates))
	if n == 0 {
		status = "empty"
	}
	return items, FamilyReport{Family: "submitted_pr_reviews", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
}

func fetchReviewComments(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	query := url.Values{"sort": {"created"}, "direction": {"asc"}}
	path := "/repos/" + owner + "/" + name + "/pulls/comments"
	resource := func(doc *commentDoc) string {
		return pullResourceFromURL(doc.PullRequestURL, repo)
	}
	items, walk := fetchCommentsPageLoop(g, query, path, repo, windowStart, windowEnd, "pr_review_comment", "pr-review-comment", "pulls/comments", "review-comment", resource, nil)
	n := len(items)
	status, detail := familyStatus(walk, n, "review-comment")
	if status == StatusComplete && n == 0 {
		status = "empty"
	}
	return items, FamilyReport{Family: "pr_review_comments", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
}

func fetchIssueComments(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	query := url.Values{
		"sort":      {"created"},
		"direction": {"asc"},
		"since":     {windowStart.UTC().Format(time.RFC3339)},
	}
	path := "/repos/" + owner + "/" + name + "/issues/comments"
	resource := func(doc *commentDoc) string {
		return issueResourceFromURL(doc.IssueURL, repo)
	}
	// The issues/comments document has no pull_request field, so a PR
	// conversation comment is indistinguishable from an issue comment in that
	// response. The documented issue document is the authority: resolve the
	// number from the comment's issue_url and ask issueTargetFor. Only a known
	// PR answer reclassifies; an unknown answer leaves the item an issue
	// comment and is reported as incomplete with the honest count — never
	// guessed.
	budget := &issueLookupBudget{}
	unparsable := 0
	refine := func(item *EvidenceItem, doc *commentDoc) {
		m := issueNumberTailRE.FindStringSubmatch(doc.IssueURL)
		if m == nil {
			unparsable++
			return
		}
		number, err := strconv.Atoi(m[1])
		if err != nil || number == 0 {
			unparsable++
			return
		}
		isPR, known, bounded := g.issueTargetFor(repo, owner, name, number, budget)
		if !known {
			if bounded {
				budget.boundedComments++
			} else {
				budget.failedComments++
			}
			return
		}
		if isPR {
			item.Kind = "pr_conversation_comment"
			item.Resource = fmt.Sprintf("pr:%s:%d", repo, number)
		}
	}
	items, walk := fetchCommentsPageLoop(g, query, path, repo, windowStart, windowEnd, "issue_comment", "issue-comment", "issues/comments", "issue-comment", resource, refine)
	n := len(items)
	status, detail := familyStatus(walk, n, "issue-comment")
	if status != StatusFailed {
		if budget.failedComments > 0 {
			status = StatusIncomplete
			detail = niceJoin(detail, fmt.Sprintf(
				"%d comment(s) could not be verified as pull requests (the documented issue lookup failed or was not HTTP 200); they stay classified as issue comments, never guessed", budget.failedComments))
		}
		if budget.boundedComments > 0 {
			status = StatusIncomplete
			detail = niceJoin(detail, fmt.Sprintf(
				"%d comment(s) exceeded the collector's %d-lookup ceiling per repository per attempt and were not verified as pull requests; they stay classified as issue comments, never guessed", budget.boundedComments, maxIssueTargetLookups))
		}
		if unparsable > 0 {
			status = StatusIncomplete
			detail = niceJoin(detail, fmt.Sprintf(
				"%d comment(s) carried no parsable issue number in issue_url and could not be checked for the documented pull_request discriminator; they stay classified as issue comments, never guessed", unparsable))
		}
	}
	if status == StatusComplete && n == 0 {
		status = "empty"
	}
	return items, FamilyReport{Family: "issue_comments", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
}

// fetchCommentsPageLoop walks one paged comment family. kind is the
// EvidenceItem kind; refKind is the canonical identity token shared with the
// same provider action seen through the events feed, so REST and event
// records for one comment deduplicate and merge provenance. refine, when
// non-nil, may adjust the constructed item without changing its canonical
// identity (the issue-comment family uses it to classify a PR conversation
// comment from the documented issue document).
func fetchCommentsPageLoop(g *githubRun, query url.Values, path, repo string, windowStart, windowEnd time.Time, kind, refKind, provenancePrefix, label string, resourceFn func(*commentDoc) string, refine func(*EvidenceItem, *commentDoc)) ([]EvidenceItem, familyWalk) {
	var items []EvidenceItem
	decode := func(body []byte) (int, error) {
		var docs []commentDoc
		if err := json.Unmarshal(body, &docs); err != nil {
			return 0, err
		}
		for _, doc := range docs {
			if doc.User == nil || doc.User.Login != g.scope.user {
				continue
			}
			at, err := time.Parse(time.RFC3339, doc.CreatedAt)
			if err != nil || !at.Before(windowEnd) || !at.After(windowStart.Add(-time.Second)) {
				continue
			}
			title := label
			if strings.EqualFold(label, "review-comment") && doc.PullRequestURL != "" {
				title = "review comment on " + pullRefFromURL(doc.PullRequestURL, repo)
			} else if strings.EqualFold(label, "issue-comment") && doc.IssueURL != "" {
				title = "comment on " + issueRefFromURL(doc.IssueURL, repo)
			}
			item := EvidenceItem{
				Ref:        fmt.Sprintf("gh:%s:%s:%d", repo, refKind, doc.ID),
				Kind:       kind,
				Actor:      doc.User.Login,
				Time:       at.UTC().Format(time.RFC3339),
				Repo:       repo,
				Resource:   resourceFn(&doc),
				URL:        doc.HTMLURL,
				Title:      title,
				Provenance: provenancePrefix,
			}
			if refine != nil {
				refine(&item, &doc)
			}
			items = append(items, item)
		}
		return len(docs), nil
	}
	walk := g.familyPageLoop(query, path, maxGithubFamilyPages, decode)
	return items, walk
}

// fetchRepoEvents is family 7: discussions and other attributable repository
// interactions. GitHub bounds the events timeline by design (only the past 30
// days, up to 300 events per timeline), so the family's steady state is the
// dedicated `windowed` status — honest bounded recent-window coverage, never
// `empty` and never a forced `incomplete`. A 422 on the page past the
// retained window is the documented end of that window, classified as
// `incomplete` (bounded provider coverage with the retained count and a
// sanitized diagnostic), never `failed`; `failed` stays reserved for
// 403/429/transport/permission failures. Reaching the collector's own page
// ceiling stays `incomplete`, because that bound is the collector's, not the
// provider's.
func fetchRepoEvents(g *githubRun, repo, owner, name string, windowStart, windowEnd time.Time) ([]EvidenceItem, FamilyReport) {
	query := url.Values{}
	path := "/repos/" + owner + "/" + name + "/events"
	var items []EvidenceItem
	var sawOlder bool
	decode := func(body []byte) (int, error) {
		var docs []ghEventDoc
		if err := json.Unmarshal(body, &docs); err != nil {
			return 0, err
		}
		for _, doc := range docs {
			at, err := time.Parse(time.RFC3339, doc.CreatedAt)
			if err != nil {
				continue
			}
			if at.Before(windowStart) {
				sawOlder = true
				continue
			}
			if !at.Before(windowEnd) {
				continue
			}
			if doc.Actor == nil || doc.Actor.Login != g.scope.user {
				continue
			}
			ev := evidenceFromEvent(doc, repo, at, g.scope.user, windowStart, windowEnd)
			if ev != nil {
				items = append(items, *ev)
			}
		}
		return len(docs), nil
	}
	walk := g.familyPageLoop(query, path, maxGithubFamilyPages, decode)
	n := len(items)
	if walk.err != nil {
		// sawOlder is only proof of a finished page below the window; it never
		// overrides a real failure on a later page.
		if walk.stop == "http" && walk.httpStatus == http.StatusUnprocessableEntity {
			detail := niceJoin(sanitizeHTTPDiagnostic(walk.httpStatus, walk.httpBody), fmt.Sprintf(
				"%d attributable event(s) retained before the provider's retained-window cap; coverage for repository events is bounded provider coverage (only the past 30 days, up to 300 events per timeline), not a collection failure", n))
			return items, FamilyReport{Family: "repo_events", QueriedRepo: repo, Count: n, Status: StatusIncomplete, Detail: detail}
		}
		status, detail := familyStatus(walk, n, "repository events")
		return items, FamilyReport{Family: "repo_events", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
	}
	if sawOlder {
		walk.stop = "finished"
	}
	if walk.stop == "capped" {
		status, detail := familyStatus(walk, n, "repository events")
		return items, FamilyReport{Family: "repo_events", QueriedRepo: repo, Count: n, Status: status, Detail: detail}
	}
	// Steady state: the walk finished or emptied inside the retained window.
	// The timeline is bounded by the provider's documented design regardless
	// of how many items were retained, so the honest status is `windowed`.
	_, detail := familyStatus(walk, n, "repository events")
	detail = niceJoin(detail, "the events timeline is bounded by the provider (only the past 30 days, up to 300 events per timeline); the collector reports it as windowed coverage — a bounded recent window by the provider's design, not completeness and not a collection limitation")
	return items, FamilyReport{Family: "repo_events", QueriedRepo: repo, Count: n, Status: StatusWindowed, Detail: detail}
}

// evidenceFromEvent maps one attributable event; unrelated actors are dropped
// before any record is created. Events carry the same canonical identity and
// resource identity their REST family counterparts use, so one provider
// action seen twice merges provenance and distinct actions stay distinct.
// user is the configured user; it is needed because a pull-request merge is
// attributable only when the payload names that user as the merger. The
// window is re-checked for a merge's merged_at: the caller filters the event
// by its created_at, and the item must never be dated outside the day.
func evidenceFromEvent(doc ghEventDoc, repo string, at time.Time, user string, windowStart, windowEnd time.Time) *EvidenceItem {
	if doc.Actor == nil {
		return nil
	}
	actor := doc.Actor.Login
	base := EvidenceItem{
		Actor:      actor,
		Time:       at.UTC().Format(time.RFC3339),
		Repo:       repo,
		Provenance: "repos/events",
	}
	// A merge is a merge for this day only when the provider names the
	// configured user as the merger and the full-precision merged_at instant
	// is inside [windowStart, windowEnd). Otherwise the event falls through to
	// the generic-event branch below; it is never attributed with an
	// out-of-window time.
	var mergeAt time.Time
	windowedMerge := false
	if strings.EqualFold(doc.Type, "PullRequestEvent") && doc.Payload.Action == "closed" &&
		doc.Payload.PullRequest != nil && doc.Payload.PullRequest.Merged &&
		doc.Payload.PullRequest.MergedBy != nil && doc.Payload.PullRequest.MergedBy.Login == user &&
		doc.Payload.PullRequest.Number != 0 {
		if parsed, err := time.Parse(time.RFC3339, doc.Payload.PullRequest.MergedAt); err == nil &&
			!parsed.Before(windowStart) && parsed.Before(windowEnd) {
			mergeAt, windowedMerge = parsed, true
		}
	}
	switch {
	case windowedMerge:
		// Same canonical identity as the merged_pull_requests REST family so
		// the two endpoint families merge provenance instead of double
		// counting one merge. A close without merged, a merge by another
		// actor, or an out-of-window merged_at keeps the generic-event
		// behavior below.
		base.Ref = fmt.Sprintf("gh:%s:pr-merge:%d", repo, doc.Payload.PullRequest.Number)
		base.Kind = "merged_pull_request"
		base.Resource = fmt.Sprintf("pr:%s:%d", repo, doc.Payload.PullRequest.Number)
		base.URL = doc.Payload.PullRequest.HTMLURL
		base.Title = sanitizerText(doc.Payload.PullRequest.Title)
		base.Time = mergeAt.UTC().Format(time.RFC3339)
		return &base
	case strings.EqualFold(doc.Type, "DiscussionEvent") && doc.Payload.Discussion != nil && doc.Payload.Discussion.Number != 0:
		base.Ref = fmt.Sprintf("gh:%s:discussion:%d", repo, doc.Payload.Discussion.Number)
		base.Kind = "discussion"
		base.Resource = fmt.Sprintf("discussion:%s:%d", repo, doc.Payload.Discussion.Number)
		base.Title = sanitizerText(doc.Payload.Discussion.Title)
		return &base
	case strings.EqualFold(doc.Type, "DiscussionCommentEvent") && doc.Payload.Discussion != nil &&
		doc.Payload.Discussion.Number != 0 && doc.Payload.Comment != nil:
		base.Ref = fmt.Sprintf("gh:%s:discussion-comment:%d", repo, doc.Payload.Comment.ID)
		base.Kind = "discussion_comment"
		base.Resource = fmt.Sprintf("discussion:%s:%d", repo, doc.Payload.Discussion.Number)
		base.URL = doc.Payload.Comment.HTMLURL
		base.Title = sanitizerText(fmt.Sprintf("discussion comment on %s#%d", repo, doc.Payload.Discussion.Number))
		return &base
	case strings.EqualFold(doc.Type, "PullRequestReviewEvent") && doc.Payload.PullRequest != nil && doc.Payload.Review != nil:
		base.Ref = fmt.Sprintf("gh:%s:review:%d", repo, doc.Payload.Review.ID)
		base.Kind = "submitted_pr_review"
		base.Resource = fmt.Sprintf("pr:%s:%d", repo, doc.Payload.PullRequest.Number)
		base.URL = doc.Payload.Review.HTMLURL
		base.Title = sanitizerText(fmt.Sprintf("review on %s#%d (%s)", repo, doc.Payload.PullRequest.Number, doc.Payload.PullRequest.Title))
		return &base
	case strings.EqualFold(doc.Type, "PullRequestReviewCommentEvent") && doc.Payload.PullRequest != nil && doc.Payload.Comment != nil:
		base.Ref = fmt.Sprintf("gh:%s:pr-review-comment:%d", repo, doc.Payload.Comment.ID)
		base.Kind = "pr_review_comment"
		base.Resource = fmt.Sprintf("pr:%s:%d", repo, doc.Payload.PullRequest.Number)
		base.URL = doc.Payload.Comment.HTMLURL
		base.Title = sanitizerText(fmt.Sprintf("review comment on %s#%d (%s)", repo, doc.Payload.PullRequest.Number, doc.Payload.PullRequest.Title))
		return &base
	case strings.EqualFold(doc.Type, "IssueCommentEvent") && doc.Payload.Issue != nil && doc.Payload.Comment != nil:
		base.Ref = fmt.Sprintf("gh:%s:issue-comment:%d", repo, doc.Payload.Comment.ID)
		base.Kind = "issue_comment"
		base.Resource = fmt.Sprintf("issue:%s:%d", repo, doc.Payload.Issue.Number)
		base.URL = doc.Payload.Comment.HTMLURL
		base.Title = sanitizerText(fmt.Sprintf("issue comment on %s#%d", repo, doc.Payload.Issue.Number))
		return &base
	default:
		// Other attributable interactions (fork, star, release, gollum,
		// commit comments, and so on) stay visible as bounded feed content.
		if doc.ID == "" {
			return nil
		}
		base.Ref = "gh:" + repo + ":event:" + doc.ID
		base.Kind = "event"
		base.Title = providerEventTypeName(doc.Type)
		return &base
	}
}

// --- family status and text helpers -------------------------------------------

// providerURLRE matches http(s) URLs so provider error bodies (which embed
// documentation links) can be stripped from diagnostics.
var providerURLRE = regexp.MustCompile(`https?://\S+`)

// sanitizeHTTPDiagnostic renders a bounded, sanitized provider-error
// diagnostic for a family Detail: the HTTP status plus a bounded snippet of
// the provider body. Provider bodies embed documentation links, so URLs are
// stripped and control characters are replaced; credential-shaped text outside
// a URL is NOT redacted here, so a caller must not treat the detail as
// credential-free.
func sanitizeHTTPDiagnostic(status int, body []byte) string {
	snippet := strings.TrimSpace(providerURLRE.ReplaceAllString(sanitizerText(string(body)), ""))
	snippet = strings.TrimSpace(strings.Trim(snippet, "\",:' "))
	if snippet == "" {
		return fmt.Sprintf("the provider responded with HTTP %d; no readable provider error body", status)
	}
	return fmt.Sprintf("the provider responded with HTTP %d; provider error body: %s", status, snippet)
}

// familyStatus maps a pagination walk result to honest family coverage.
// "finished"/"empty" and the events feed's own windowed reporting are the
// only paths to a non-incomplete status; anything else preserves the visible
// bound. The capped detail names the ceiling the walk actually ran under
// (walk.maxPages), so search families bound by maxSearchResultPages report
// their own ceiling, not the shared REST one.
func familyStatus(walk familyWalk, n int, label string) (string, string) {
	if walk.err != nil {
		return StatusFailed, truncateRunes(walk.err.Error(), maxDetailRunes)
	}
	switch walk.stop {
	case "finished", "empty":
		if n == 0 {
			return "empty", fmt.Sprintf("no attributable %s items in the day's window (pagination exhausted after %d page(s))", label, walk.pages+1)
		}
		return StatusComplete, fmt.Sprintf("%d item(s) retained; pagination exhausted after %d page(s)", n, walk.pages+1)
	case "capped":
		return StatusIncomplete, fmt.Sprintf(
			"the collector's %d-page pagination ceiling was reached with more pages available; coverage for %s is bounded, not exhaustive",
			walk.maxPages, label)
	default:
		return StatusIncomplete, fmt.Sprintf("%s coverage is incomplete (%s)", label, walk.stop)
	}
}

func niceJoin(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// sanitizerText strips control characters and truncates provider text. It
// never inspects credentials (none are in provider payloads).
func sanitizerText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return truncateRunes(strings.TrimSpace(s), maxTitleRunes)
}

func pullRefFromURL(u, repo string) string {
	if m := pullNumberTailRE.FindStringSubmatch(u); m != nil {
		return fmt.Sprintf("%s#%s", repo, m[1])
	}
	return "pull request in " + repo
}

func issueRefFromURL(u, repo string) string {
	if m := issueNumberTailRE.FindStringSubmatch(u); m != nil {
		return fmt.Sprintf("%s#%s", repo, m[1])
	}
	return "issue in " + repo
}

// pullResourceFromURL and issueResourceFromURL derive the provider resource
// identity for grouping from the provider link's own resource path — never
// from a rendered title.
func pullResourceFromURL(u, repo string) string {
	if m := pullNumberTailRE.FindStringSubmatch(u); m != nil {
		return fmt.Sprintf("pr:%s:%s", repo, m[1])
	}
	return ""
}

func issueResourceFromURL(u, repo string) string {
	if m := issueNumberTailRE.FindStringSubmatch(u); m != nil {
		return fmt.Sprintf("issue:%s:%s", repo, m[1])
	}
	return ""
}

func providerEventTypeName(t string) string {
	t = strings.TrimSpace(t)
	switch t {
	case "":
		return "provider event"
	}
	return strings.ToLower(strings.TrimSuffix(t, "Event"))
}

// --- topic grouping -------------------------------------------------------------

// buildGithubTopics groups attributable items into Topic Candidates while
// de-duplicating the same provider event seen through two endpoint families.
// Duplicate provenance is merged into the retained item's provenance field
// instead of deleting a supporting record; minutes are never derived.
// When a topic has more provider items than the fixed per-topic item cap,
// the fact is recorded honestly on the topic (truncated), the items beyond
// the cap are retained durably as OverflowItems up to the fixed per-topic
// (maxTopicOverflowItems) and per-attempt (maxTotalOverflowItems) ceilings,
// and anything beyond those ceilings is counted in DroppedItems — never
// silently discarded.
//
// Retention allocation rule (deterministic): each topic fills its retained
// Items first, then its OverflowItems, both in de-duplicated item arrival
// order; the attempt-wide overflow pool is filled in the same order, so once
// maxTotalOverflowItems is reached, overflow items arriving later (in later
// topics) are the ones counted lost. FirstAt/LastAt stay computed from the
// retained Items only: overflow retention never changes a topic's span,
// keeping the draft's topic-to-block mapping stable.
func buildGithubTopics(items []EvidenceItem) []TopicCandidate {
	// De-duplicate same-event hits (same canonical ref) before grouping;
	// document the merged provenance on the retained item.
	byRef := map[string]int{}
	var deduped []EvidenceItem
	for _, item := range items {
		if idx, ok := byRef[item.Ref]; ok {
			if !strings.Contains(","+deduped[idx].Provenance+",", ","+item.Provenance+",") {
				deduped[idx].Provenance = deduped[idx].Provenance + "," + item.Provenance
			}
			continue
		}
		byRef[item.Ref] = len(deduped)
		deduped = append(deduped, item)
	}

	byResource := map[string]*TopicCandidate{}
	type entry struct {
		key string
		t   *TopicCandidate
	}
	var order []entry

	totalOverflow := 0
	for _, item := range deduped {
		key, title := githubTopicKey(item)
		t, ok := byResource[key]
		if !ok {
			t = &TopicCandidate{Ref: "tc:" + key, Title: title, Provider: "github"}
			byResource[key] = t
			order = append(order, entry{key: key, t: t})
		}
		switch {
		case len(t.Items) < maxTopicItems:
			t.Items = append(t.Items, item)
		case len(t.OverflowItems) < maxTopicOverflowItems && totalOverflow < maxTotalOverflowItems:
			t.OverflowItems = append(t.OverflowItems, item)
			totalOverflow++
		default:
			t.DroppedItems++
		}
	}

	var out []TopicCandidate
	for _, e := range order {
		t := e.t
		if t.DroppedItems > 0 || len(t.OverflowItems) > 0 {
			t.Truncated = true
		}
		sort.SliceStable(t.Items, func(x, y int) bool {
			if t.Items[x].Time != t.Items[y].Time {
				return t.Items[x].Time < t.Items[y].Time
			}
			return t.Items[x].Ref < t.Items[y].Ref
		})
		sort.SliceStable(t.OverflowItems, func(x, y int) bool {
			if t.OverflowItems[x].Time != t.OverflowItems[y].Time {
				return t.OverflowItems[x].Time < t.OverflowItems[y].Time
			}
			return t.OverflowItems[x].Ref < t.OverflowItems[y].Ref
		})
		if len(t.Items) > 0 {
			t.FirstAt = t.Items[0].Time
			t.LastAt = t.Items[len(t.Items)-1].Time
		}
		out = append(out, *t)
	}
	return out
}

// githubTopicKey picks a deterministic grouping identity from the retained
// provider resource identity (pull request, issue, or discussion numbers
// carried in provider payloads and provider links), never from rendered
// display strings. Commits stay a per-repository group because
// commit-to-pull-request linkage is not verified fact.
func githubTopicKey(item EvidenceItem) (string, string) {
	repo := item.Repo
	if repo == "" {
		repo = "unknown-repository"
	}
	if kind, groupRepo, number, ok := resourceGroupID(item.Resource); ok {
		switch kind {
		case "pr":
			return "gh:" + groupRepo + "#pr:" + number, fmt.Sprintf("Pull request #%s (%s)", number, groupRepo)
		case "issue":
			return "gh:" + groupRepo + "#issue:" + number, fmt.Sprintf("Issue #%s activity (%s)", number, groupRepo)
		case "discussion":
			return "gh:" + groupRepo + "#discussion:" + number, fmt.Sprintf("Discussion #%s (%s)", number, groupRepo)
		}
	}
	if item.Kind == "commit" {
		return "gh:" + repo + ":commits", "Commits (" + repo + ")"
	}
	return "gh:" + repo + ":other-activity", "Other attributable activity (" + repo + ")"
}

// resourceGroupID splits a retained resource identity of the form
// "pr:{repo}:{number}" (also issue, discussion) into its parts.
func resourceGroupID(resource string) (kind, groupRepo, number string, ok bool) {
	parts := strings.SplitN(resource, ":", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	switch parts[0] {
	case "pr", "issue", "discussion":
		if parts[1] == "" || parts[2] == "" {
			return "", "", "", false
		}
		return parts[0], parts[1], parts[2], true
	}
	return "", "", "", false
}
