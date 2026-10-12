package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
)

// TypeGit is the registry type of the git plugin.
const TypeGit evidence.SourceKind = "git"

// Git config keys.
const (
	// GitConfigRepos is a comma-separated list of REMOTE owner/repo slugs. It is
	// required only when mode includes remote.
	GitConfigRepos = "repos"
	// GitConfigAuthor optionally filters local commits by author.
	GitConfigAuthor = "author"
	// GitConfigProjectMap is a JSON object of repo -> project.
	GitConfigProjectMap = "project_map"
	// GitConfigFixture is the fixed contract-mode input path.
	GitConfigFixture = "fixture"
	// GitConfigMode selects the collected sub-sources: "local", "remote", or
	// "both" (the default when absent).
	GitConfigMode = "mode"
	// GitConfigLocalRepos is a comma-separated list of explicit LOCAL CHECKOUT
	// PATHS. It defaults to "." when absent (preserving the historical CWD
	// behavior). A filesystem path is never used as repository identity.
	GitConfigLocalRepos = "local_repos"
)

// Git modes.
const (
	GitModeLocal  = "local"
	GitModeRemote = "remote"
	GitModeBoth   = "both"
)

// Stable git contract-check codes and secret-free collection warnings. Callers
// branch on these, never on message text. Warnings never contain a path or
// secret.
const (
	CodeGitReposRequired     = "GIT_REPOS_REQUIRED"
	CodeGitProjectMapInvalid = "GIT_PROJECT_MAP_INVALID"
	CodeGitModeInvalid       = "GIT_MODE_INVALID"
	// WarningGitRemoteFetchFailed marks a remote sub-source failure. Local
	// evidence from the same collection is retained and the source is partial.
	WarningGitRemoteFetchFailed = "GIT_REMOTE_FETCH_FAILED"
	// WarningGitLocalFetchFailed marks a local sub-source fetch failure.
	WarningGitLocalFetchFailed = "GIT_LOCAL_FETCH_FAILED"
	// WarningGitLocalIdentityUnresolved marks a local checkout whose repository
	// identity could not be resolved. The checkout path is deliberately omitted.
	WarningGitLocalIdentityUnresolved = "GIT_LOCAL_IDENTITY_UNRESOLVED"
)

// GitHubPRSource fetches remote commits and pull requests for one repo/day. The
// method name FetchRemoteDayActivity is intentionally DISTINCT from
// git.LocalGitCollector.FetchDayActivity, so a local collector can NEVER satisfy
// this interface. A mis-wire of a branch-inferring local source as the GitHub
// source is therefore rejected at compile time. Wrap *git.GitHubClient with
// GitHubClientAdapter.
type GitHubPRSource interface {
	FetchRemoteDayActivity(ctx context.Context, repo string, date time.Time) ([]git.GitHubCommit, []git.GitHubPR, error)
}

// GitHubClientAdapter adapts *git.GitHubClient to GitHubPRSource.
type GitHubClientAdapter struct {
	Client *git.GitHubClient
}

// FetchRemoteDayActivity implements GitHubPRSource.
func (a GitHubClientAdapter) FetchRemoteDayActivity(ctx context.Context, repo string, date time.Time) ([]git.GitHubCommit, []git.GitHubPR, error) {
	if a.Client == nil {
		return nil, nil, nil
	}
	return a.Client.FetchDayActivity(ctx, repo, date)
}

// LocalGitSource fetches local commits for one repo/day and discovers PR
// references from commit messages. *git.LocalGitCollector is adapted via
// LocalGitCollectorAdapter. PR numbers come only from commit-message references,
// never from a branch name.
type LocalGitSource interface {
	CurrentBranch(ctx context.Context) string
	// RepoName returns the local checkout's repository identity: "owner/repo"
	// from remote.origin.url when present, else the checkout basename. It is a
	// non-secret provenance value; the checkout path itself is never used as
	// identity.
	RepoName(ctx context.Context) string
	FetchDayCommits(ctx context.Context, author string, date time.Time) ([]git.GitHubCommit, error)
	DiscoverPRReferences(commits []git.GitHubCommit) map[int][]git.GitHubCommit
}

// LocalGitCollectorAdapter adapts *git.LocalGitCollector to LocalGitSource.
type LocalGitCollectorAdapter struct {
	Collector *git.LocalGitCollector
}

// CurrentBranch implements LocalGitSource.
func (a LocalGitCollectorAdapter) CurrentBranch(ctx context.Context) string {
	if a.Collector == nil {
		return ""
	}
	return a.Collector.CurrentBranch(ctx)
}

// RepoName implements LocalGitSource.
func (a LocalGitCollectorAdapter) RepoName(ctx context.Context) string {
	if a.Collector == nil {
		return ""
	}
	return a.Collector.RepoName(ctx)
}

// FetchDayCommits implements LocalGitSource.
func (a LocalGitCollectorAdapter) FetchDayCommits(ctx context.Context, author string, date time.Time) ([]git.GitHubCommit, error) {
	if a.Collector == nil {
		return nil, nil
	}
	return a.Collector.FetchDayCommits(ctx, author, date)
}

// DiscoverPRReferences implements LocalGitSource.
func (a LocalGitCollectorAdapter) DiscoverPRReferences(commits []git.GitHubCommit) map[int][]git.GitHubCommit {
	if a.Collector == nil {
		return nil
	}
	return a.Collector.DiscoverPRReferences(commits)
}

// GitPlugin collects local commits and remote PR activity with deduplication.
type GitPlugin struct {
	GitHub GitHubPRSource
	// Local is the local source used for the configured path ".". It is the
	// historical injection seam and is used when Local non-nil.
	Local LocalGitSource
	// LocalFactory builds a per-path local source for explicit local_repos paths.
	// A nil factory falls back to git.NewLocalGitCollector(path).
	LocalFactory func(path string) LocalGitSource
}

// NewGitPlugin returns a git plugin over the injected sources. The GitHub source
// type is narrow (FetchRemoteDayActivity) so a *git.LocalGitCollector cannot be
// passed as the GitHub source.
func NewGitPlugin(github GitHubPRSource, local LocalGitSource) *GitPlugin {
	return &GitPlugin{GitHub: github, Local: local}
}

// hasLocalSource reports whether any local capability was injected. When neither
// Local nor LocalFactory is set, the plugin has no local source to collect from
// and does not fabricate one, so a synthetic remote-only plugin instance cannot
// spawn a git subprocess in the process working directory.
func (p *GitPlugin) hasLocalSource() bool {
	return p.Local != nil || p.LocalFactory != nil
}

// localFor resolves the local source for one configured checkout path. A "." path
// uses the injected Local (the historical seam); otherwise the factory, else the
// default git.NewLocalGitCollector(path).
func (p *GitPlugin) localFor(path string) LocalGitSource {
	if path == "" {
		path = "."
	}
	if path == "." && p.Local != nil {
		return p.Local
	}
	if p.LocalFactory != nil {
		return p.LocalFactory(path)
	}
	return LocalGitCollectorAdapter{Collector: git.NewLocalGitCollector(path)}
}

// Type implements evidence.Plugin.
func (p *GitPlugin) Type() evidence.SourceKind { return TypeGit }

// RequiredConfig implements evidence.Plugin. The repos key is declared but not
// unconditionally required: it is required only when mode includes remote, and
// that conditional requirement is enforced by CheckContract.
func (p *GitPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{{Key: GitConfigRepos, Secret: false, Required: false}}
}

// DeclaredEvidenceKinds implements evidence.Plugin.
func (p *GitPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{evidence.SourceRemotePR, evidence.SourceLocalGit}
}

// CheckContract implements evidence.Plugin. It enforces mode-appropriate config
// with stable codes: an invalid mode fails; mode remote/both requires at least
// one repos slug; mode local/both requires at least one local path (the default
// "." satisfies it); any project map must be valid JSON.
func (p *GitPlugin) CheckContract(_ context.Context, cfg evidence.SourceConfig) []evidence.ContractCheck {
	mode, ok := gitModeOf(cfg)
	if !ok {
		return []evidence.ContractCheck{{
			Name:   "git_mode",
			Passed: false,
			Code:   CodeGitModeInvalid,
			Detail: "mode must be one of local, remote, both",
		}}
	}

	var checks []evidence.ContractCheck

	if modeIncludesRemote(mode) {
		if len(splitList(cfg.Config[GitConfigRepos])) == 0 {
			checks = append(checks, evidence.ContractCheck{
				Name:   "git_repos",
				Passed: false,
				Code:   CodeGitReposRequired,
				Detail: "at least one repository is required when mode includes remote",
			})
		} else {
			checks = append(checks, evidence.ContractCheck{Name: "git_repos", Passed: true})
		}
	}

	if modeIncludesLocal(mode) {
		// An absent/empty local_repos defaults to ".", so "at least one local
		// path" is always satisfied: there is no reachable failure for this
		// requirement. The check is recorded as passing for observability.
		checks = append(checks, evidence.ContractCheck{Name: "git_local_repos", Passed: true})
	}

	if _, err := parseProjectMap(cfg.Config[GitConfigProjectMap]); err != nil {
		checks = append(checks, evidence.ContractCheck{
			Name:   "git_project_map",
			Passed: false,
			Code:   CodeGitProjectMapInvalid,
			Detail: "project_map must be a JSON object of repo -> project",
		})
	} else {
		checks = append(checks, evidence.ContractCheck{Name: "git_project_map", Passed: true})
	}

	return checks
}

// Collect implements evidence.Plugin. It deduplicates commits and PRs: a commit
// claimed by a PR is folded into the PR record (counts once, both refs retained);
// an unclaimed commit is a standalone local_git record. It never infers a PR
// number from a branch name.
//
// A remote sub-source failure does NOT abort collection: local evidence is
// retained and the source is reported as StatusPartial with a stable warning, so
// a partial collection is never presented as complete and its evidence is never
// dropped. Only an unreadable project map or an invalid target date fails the
// source outright.
func (p *GitPlugin) Collect(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (evidence.CollectResult, error) {
	activities, warnings, partial, err := p.gather(ctx, cfg, req)
	if err != nil {
		return evidence.CollectResult{}, err
	}
	projectMap, err := parseProjectMap(cfg.Config[GitConfigProjectMap])
	if err != nil {
		return evidence.CollectResult{}, errors.New("git: invalid project map")
	}

	evs, err := buildGitEvidence(activities, projectMap, time.Now().UTC())
	if err != nil {
		return evidence.CollectResult{}, err
	}

	status := evidence.StatusComplete
	if partial {
		status = evidence.StatusPartial
	}
	return evidence.CollectResult{
		Evidence: evs,
		Result: evidence.SourceResult{
			Status:        status,
			EvidenceCount: len(evs),
			Warnings:      warnings,
		},
	}, nil
}

// gitRepoActivity is the raw activity gathered for one repository.
type gitRepoActivity struct {
	RepoKey string
	Branch  string
	Commits []git.GitHubCommit
	PRs     []git.GitHubPR
}

// gather collects raw activity per configured repository. Contract mode uses a
// fixed fixture; live mode honors the configured mode:
//
//   - mode includes remote: fetch GitHub activity. A fetch error is recorded as a
//     warning and collection continues with local (never aborts).
//   - mode includes local: collect from each configured local checkout path.
//   - mode == local: GitHub is never called at all.
//
// Activities are grouped by canonical repo key, so a remote slug and a local
// checkout that resolve to the same identity are folded together. The returned
// partial flag is true when any configured sub-source failed.
func (p *GitPlugin) gather(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) ([]gitRepoActivity, []string, bool, error) {
	if req.IsContract() {
		acts, err := loadGitFixture(cfg.Config[GitConfigFixture])
		return acts, nil, false, err
	}

	mode, ok := gitModeOf(cfg)
	if !ok {
		return nil, nil, false, errors.New("git: invalid mode")
	}
	date, err := parseDate(req.Date, req.Location)
	if err != nil {
		return nil, nil, false, errors.New("git: invalid target date")
	}
	author := strings.TrimSpace(cfg.Config[GitConfigAuthor])

	var order []string
	byKey := map[string]*gitRepoActivity{}
	activity := func(key string) *gitRepoActivity {
		if a, ok := byKey[key]; ok {
			return a
		}
		a := &gitRepoActivity{RepoKey: key}
		byKey[key] = a
		order = append(order, key)
		return a
	}

	var warnings []string
	seenWarning := map[string]bool{}
	addWarning := func(code string) {
		if seenWarning[code] {
			return
		}
		seenWarning[code] = true
		warnings = append(warnings, code)
	}
	partial := false

	if modeIncludesRemote(mode) {
		for _, repo := range splitList(cfg.Config[GitConfigRepos]) {
			repoKey := evidence.CanonicalRepoKey(repo, repo)
			if repoKey == "" {
				return nil, nil, false, fmt.Errorf("git: repository %q has no valid identity", repo)
			}
			act := activity(repoKey)
			if p.GitHub == nil {
				continue
			}
			commits, prs, fetchErr := p.GitHub.FetchRemoteDayActivity(ctx, repo, date)
			if fetchErr != nil {
				// A remote failure never aborts collection: local evidence is
				// retained and the failure is reported via a stable warning
				// (deduplicated across failing repos).
				addWarning(WarningGitRemoteFetchFailed)
				partial = true
				continue
			}
			act.Commits = append(act.Commits, commits...)
			act.PRs = append(act.PRs, prs...)
		}
	}

	if modeIncludesLocal(mode) && p.hasLocalSource() {
		locals := splitList(cfg.Config[GitConfigLocalRepos])
		if len(locals) == 0 {
			locals = []string{"."}
		}
		for _, path := range locals {
			local := p.localFor(path)
			name := ""
			if local != nil {
				name = local.RepoName(ctx)
			}
			// Derive identity from the checkout itself; the path is never used.
			// With no resolvable identity the failures are visible, secret-free,
			// and the path is never leaked.
			repoKey := evidence.CanonicalRepoKey(name, cfg.ID)
			if repoKey == "" {
				addWarning(WarningGitLocalIdentityUnresolved)
				partial = true
				continue
			}
			act := activity(repoKey)
			commits, fetchErr := local.FetchDayCommits(ctx, author, date)
			if fetchErr != nil {
				addWarning(WarningGitLocalFetchFailed)
				partial = true
				continue
			}
			act.Commits = append(act.Commits, commits...)
			act.Branch = local.CurrentBranch(ctx)

			groups := local.DiscoverPRReferences(commits)
			nums := make([]int, 0, len(groups))
			for n := range groups {
				nums = append(nums, n)
			}
			sort.Ints(nums)
			for _, n := range nums {
				pr := git.GitHubPR{Number: n}
				for _, c := range groups[n] {
					pr.Commits = append(pr.Commits, c.SHA)
					if pr.Title == "" {
						pr.Title = c.Message
					}
				}
				act.PRs = append(act.PRs, pr)
			}
		}
	}

	out := make([]gitRepoActivity, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	return out, warnings, partial, nil
}

// gitModeOf parses the configured mode. An absent mode defaults to "both"
// (backward compatible). An unrecognized mode is invalid.
func gitModeOf(cfg evidence.SourceConfig) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(cfg.Config[GitConfigMode])) {
	case "":
		return GitModeBoth, true
	case GitModeLocal:
		return GitModeLocal, true
	case GitModeRemote:
		return GitModeRemote, true
	case GitModeBoth:
		return GitModeBoth, true
	default:
		return "", false
	}
}

func modeIncludesRemote(mode string) bool { return mode == GitModeRemote || mode == GitModeBoth }
func modeIncludesLocal(mode string) bool  { return mode == GitModeLocal || mode == GitModeBoth }

// GitModeRequiresRemote reports whether a git source config requires a remote
// (GitHub) credential. It is false ONLY for an explicit "local" mode. An absent
// mode defaults to "both" and requires a credential; an unrecognized mode is
// treated conservatively as requiring one (CheckContract fails it regardless).
func GitModeRequiresRemote(cfg evidence.SourceConfig) bool {
	mode, ok := gitModeOf(cfg)
	if !ok {
		return true
	}
	return modeIncludesRemote(mode)
}

// buildGitEvidence folds commits into PRs and emits standalone commits, with a
// stable ordering so identical input yields identical IDs.
func buildGitEvidence(activities []gitRepoActivity, projectMap map[string]string, observed time.Time) ([]evidence.Evidence, error) {
	var out []evidence.Evidence

	for _, act := range activities {
		commitBySHA := map[string]git.GitHubCommit{}
		var shas []string
		for _, c := range act.Commits {
			if c.SHA == "" {
				continue
			}
			if _, ok := commitBySHA[c.SHA]; !ok {
				commitBySHA[c.SHA] = c
				shas = append(shas, c.SHA)
			}
		}

		prByNum := map[int]git.GitHubPR{}
		var prNums []int
		for _, pr := range act.PRs {
			if pr.Number <= 0 {
				continue
			}
			if existing, ok := prByNum[pr.Number]; ok {
				existing.Commits = appendUnique(existing.Commits, pr.Commits...)
				if existing.Branch == "" {
					existing.Branch = pr.Branch
				}
				if existing.Title == "" {
					existing.Title = pr.Title
				}
				prByNum[pr.Number] = existing
			} else {
				prByNum[pr.Number] = pr
				prNums = append(prNums, pr.Number)
			}
		}
		sort.Ints(prNums)

		claimed := map[string]bool{}

		for _, num := range prNums {
			pr := prByNum[num]
			id, err := evidence.RemotePRID(act.RepoKey, num)
			if err != nil {
				return nil, err
			}
			refs := []evidence.SourceRef{{
				Kind:       evidence.SourceRemotePR,
				Repository: act.RepoKey,
				PRNumber:   num,
				Branch:     pr.Branch,
			}}
			var start, end time.Time
			for _, sha := range pr.Commits {
				c, ok := commitBySHA[sha]
				if !ok {
					continue
				}
				claimed[sha] = true
				refs = append(refs, evidence.SourceRef{
					Kind:       evidence.SourceLocalGit,
					Repository: act.RepoKey,
					CommitSHA:  sha,
					Branch:     act.Branch,
					At:         c.Timestamp.UTC(),
				})
				if !c.Timestamp.IsZero() {
					if start.IsZero() || c.Timestamp.Before(start) {
						start = c.Timestamp
					}
					if end.IsZero() || c.Timestamp.After(end) {
						end = c.Timestamp
					}
				}
			}

			out = append(out, evidence.Evidence{
				ID:           id,
				Kind:         evidence.SourceRemotePR,
				Title:        pr.Title,
				Start:        start.UTC(),
				End:          end.UTC(),
				Duration:     evidence.UnknownDuration(),
				SourceRefs:   refs,
				TopicHint:    &evidence.TopicHint{Repository: act.RepoKey, PRNumber: num, Branch: pr.Branch},
				ProjectHints: projectHints(projectMap, act.RepoKey),
				ObservedAt:   observed,
			})
		}

		sort.Strings(shas)
		for _, sha := range shas {
			if claimed[sha] {
				continue
			}
			c := commitBySHA[sha]
			id, err := evidence.LocalGitID(act.RepoKey, sha)
			if err != nil {
				return nil, err
			}
			out = append(out, evidence.Evidence{
				ID:           id,
				Kind:         evidence.SourceLocalGit,
				Title:        firstLine(c.Message),
				Start:        c.Timestamp.UTC(),
				Duration:     evidence.UnknownDuration(),
				SourceRefs:   []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: act.RepoKey, CommitSHA: sha, Branch: act.Branch, At: c.Timestamp.UTC()}},
				TopicHint:    &evidence.TopicHint{Repository: act.RepoKey, PRNumber: 0, Branch: act.Branch},
				ProjectHints: projectHints(projectMap, act.RepoKey),
				ObservedAt:   observed,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// gitFixture is the fixed contract-mode input shape.
type gitFixture struct {
	Repos []gitFixtureRepo `json:"repos"`
}

type gitFixtureRepo struct {
	Repo    string             `json:"repo"`
	Branch  string             `json:"branch"`
	Commits []gitFixtureCommit `json:"commits"`
	PRs     []gitFixturePR     `json:"prs"`
}

type gitFixtureCommit struct {
	SHA       string `json:"sha"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type gitFixturePR struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Branch  string   `json:"branch"`
	Commits []string `json:"commits"`
}

// loadGitFixture reads a fixed git fixture for contract mode.
func loadGitFixture(path string) ([]gitRepoActivity, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("git: contract mode requires a fixture path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("git: failed to read fixture")
	}
	var fx gitFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return nil, errors.New("git: failed to parse fixture")
	}
	var out []gitRepoActivity
	for _, r := range fx.Repos {
		repoKey := evidence.CanonicalRepoKey(r.Repo, r.Repo)
		if repoKey == "" {
			return nil, errors.New("git: fixture repository has no valid identity")
		}
		act := gitRepoActivity{RepoKey: repoKey, Branch: r.Branch}
		for _, c := range r.Commits {
			ts, _ := time.Parse(time.RFC3339, c.Timestamp)
			act.Commits = append(act.Commits, git.GitHubCommit{SHA: c.SHA, Message: c.Message, Timestamp: ts})
		}
		for _, pr := range r.PRs {
			act.PRs = append(act.PRs, git.GitHubPR{Number: pr.Number, Title: pr.Title, Branch: pr.Branch, Commits: pr.Commits})
		}
		out = append(out, act)
	}
	return out, nil
}

// parseProjectMap parses a JSON repo -> project map, normalizing repo keys
// through CanonicalRepoKey.
func parseProjectMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		key := evidence.CanonicalRepoKey(k, k)
		if key == "" {
			key = k
		}
		out[key] = v
	}
	return out, nil
}

func projectHints(m map[string]string, repoKey string) []string {
	if m == nil {
		return nil
	}
	if p, ok := m[repoKey]; ok && p != "" {
		return []string{p}
	}
	return nil
}

func splitList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func appendUnique(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, d := range dst {
		seen[d] = true
	}
	for _, it := range items {
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		dst = append(dst, it)
	}
	return dst
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// parseDate parses a YYYY-MM-DD target date in the request's zone. A nil loc
// means UTC, preserving the historical UTC parse for callers that supply no
// zone. It never uses the host's time.Local.
func parseDate(date string, loc *time.Location) (time.Time, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return time.Time{}, errors.New("empty date")
	}
	if loc == nil {
		loc = time.UTC
	}
	return time.ParseInLocation("2006-01-02", date, loc)
}
