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
	GitConfigRepos      = "repos"
	GitConfigAuthor     = "author"
	GitConfigProjectMap = "project_map"
	GitConfigFixture    = "fixture"
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
	Local  LocalGitSource
}

// NewGitPlugin returns a git plugin over the injected sources. The GitHub source
// type is narrow (FetchRemoteDayActivity) so a *git.LocalGitCollector cannot be
// passed as the GitHub source.
func NewGitPlugin(github GitHubPRSource, local LocalGitSource) *GitPlugin {
	return &GitPlugin{GitHub: github, Local: local}
}

// Type implements evidence.Plugin.
func (p *GitPlugin) Type() evidence.SourceKind { return TypeGit }

// RequiredConfig implements evidence.Plugin.
func (p *GitPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{{Key: GitConfigRepos, Secret: false, Required: true}}
}

// DeclaredEvidenceKinds implements evidence.Plugin.
func (p *GitPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{evidence.SourceRemotePR, evidence.SourceLocalGit}
}

// CheckContract implements evidence.Plugin. It verifies at least one repository
// is configured and that any project map is valid JSON.
func (p *GitPlugin) CheckContract(_ context.Context, cfg evidence.SourceConfig) []evidence.ContractCheck {
	if len(splitList(cfg.Config[GitConfigRepos])) == 0 {
		return []evidence.ContractCheck{{
			Name:   "git_repos",
			Passed: false,
			Code:   "GIT_REPOS_REQUIRED",
			Detail: "at least one repository is required",
		}}
	}
	if _, err := parseProjectMap(cfg.Config[GitConfigProjectMap]); err != nil {
		return []evidence.ContractCheck{{
			Name:   "git_project_map",
			Passed: false,
			Code:   "GIT_PROJECT_MAP_INVALID",
			Detail: "project_map must be a JSON object of repo -> project",
		}}
	}
	return []evidence.ContractCheck{{Name: "git_repos", Passed: true}}
}

// Collect implements evidence.Plugin. It deduplicates commits and PRs: a commit
// claimed by a PR is folded into the PR record (counts once, both refs retained);
// an unclaimed commit is a standalone local_git record. It never infers a PR
// number from a branch name.
func (p *GitPlugin) Collect(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (evidence.CollectResult, error) {
	activities, err := p.gather(ctx, cfg, req)
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

	return evidence.CollectResult{
		Evidence: evs,
		Result:   evidence.SourceResult{Status: evidence.StatusComplete, EvidenceCount: len(evs)},
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
// fixed fixture; live mode uses the injected sources and never touches a source
// twice.
func (p *GitPlugin) gather(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) ([]gitRepoActivity, error) {
	if req.IsContract() {
		return loadGitFixture(cfg.Config[GitConfigFixture])
	}

	date, err := parseDate(req.Date)
	if err != nil {
		return nil, errors.New("git: invalid target date")
	}
	author := strings.TrimSpace(cfg.Config[GitConfigAuthor])

	var out []gitRepoActivity
	for _, repo := range splitList(cfg.Config[GitConfigRepos]) {
		repoKey := evidence.CanonicalRepoKey(repo, repo)
		if repoKey == "" {
			return nil, fmt.Errorf("git: repository %q has no valid identity", repo)
		}
		act := gitRepoActivity{RepoKey: repoKey}
		if p.GitHub != nil {
			commits, prs, fetchErr := p.GitHub.FetchRemoteDayActivity(ctx, repo, date)
			if fetchErr != nil {
				return nil, errors.New("git: remote fetch failed")
			}
			act.Commits = append(act.Commits, commits...)
			act.PRs = append(act.PRs, prs...)
		}
		if p.Local != nil {
			commits, fetchErr := p.Local.FetchDayCommits(ctx, author, date)
			if fetchErr != nil {
				return nil, errors.New("git: local fetch failed")
			}
			act.Commits = append(act.Commits, commits...)
			act.Branch = p.Local.CurrentBranch(ctx)

			groups := p.Local.DiscoverPRReferences(commits)
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
		out = append(out, act)
	}
	return out, nil
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

func parseDate(date string) (time.Time, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return time.Time{}, errors.New("empty date")
	}
	return time.Parse("2006-01-02", date)
}
