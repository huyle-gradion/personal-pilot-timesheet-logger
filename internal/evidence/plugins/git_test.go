package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
)

// fakeGitHub returns fixed activity for any repo.
type fakeGitHub struct {
	commits []git.GitHubCommit
	prs     []git.GitHubPR
}

func (f *fakeGitHub) FetchRemoteDayActivity(context.Context, string, time.Time) ([]git.GitHubCommit, []git.GitHubPR, error) {
	return f.commits, f.prs, nil
}

// fakeLocal returns fixed local activity and a current branch.
type fakeLocal struct {
	repoName string
	branch   string
	commits  []git.GitHubCommit
	refs     map[int][]git.GitHubCommit
}

func (f *fakeLocal) CurrentBranch(context.Context) string { return f.branch }
func (f *fakeLocal) RepoName(context.Context) string      { return f.repoName }
func (f *fakeLocal) FetchDayCommits(context.Context, string, time.Time) ([]git.GitHubCommit, error) {
	return f.commits, nil
}
func (f *fakeLocal) DiscoverPRReferences([]git.GitHubCommit) map[int][]git.GitHubCommit {
	return f.refs
}

// countingGitHub counts remote calls and returns no activity.
type countingGitHub struct{ calls int }

func (f *countingGitHub) FetchRemoteDayActivity(context.Context, string, time.Time) ([]git.GitHubCommit, []git.GitHubPR, error) {
	f.calls++
	return nil, nil, nil
}

// errGitHub fails every remote call.
type errGitHub struct{ calls int }

func (f *errGitHub) FetchRemoteDayActivity(context.Context, string, time.Time) ([]git.GitHubCommit, []git.GitHubPR, error) {
	f.calls++
	return nil, nil, errors.New("remote down")
}

func gitConfig(repos string) evidence.SourceConfig {
	return evidence.SourceConfig{
		ID: "git", Type: TypeGit, Enabled: true,
		Config: map[string]string{GitConfigRepos: repos},
	}
}

// TestGitPlugin_CommitFoldedIntoPRCountsOnce proves a commit claimed by a PR is
// folded into the PR record (counts once, both refs retained), while an unclaimed
// commit is a standalone local_git record.
func TestGitPlugin_CommitFoldedIntoPRCountsOnce(t *testing.T) {
	gh := &fakeGitHub{
		commits: []git.GitHubCommit{
			{SHA: "sha1", Message: "first", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)},
			{SHA: "sha2", Message: "second", Timestamp: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)},
			{SHA: "sha3", Message: "standalone", Timestamp: time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)},
		},
		prs: []git.GitHubPR{{Number: 7, Title: "Add feature", Branch: "feature/x", Commits: []string{"sha1", "sha2"}}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, nil))

	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{gitConfig("acme/app")}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if !summary.Complete {
		t.Fatalf("expected complete, got %+v", summary)
	}
	if len(ev) != 2 {
		t.Fatalf("expected 2 evidence items (1 PR + 1 standalone), got %d", len(ev))
	}

	var prEvidence *evidence.Evidence
	var standalone []evidence.Evidence
	for i := range ev {
		switch ev[i].Kind {
		case evidence.SourceRemotePR:
			prEvidence = &ev[i]
		case evidence.SourceLocalGit:
			standalone = append(standalone, ev[i])
		}
	}
	if prEvidence == nil {
		t.Fatal("expected PR evidence")
	}
	wantPRID, _ := evidence.RemotePRID("acme/app", 7)
	if prEvidence.ID != wantPRID {
		t.Errorf("PR ID = %q, want %q", prEvidence.ID, wantPRID)
	}
	if prEvidence.Duration.Known {
		t.Error("PR evidence must carry an unknown duration")
	}
	if prEvidence.TopicHint == nil || prEvidence.TopicHint.PRNumber != 7 || prEvidence.TopicHint.Repository != "acme/app" {
		t.Errorf("unexpected PR TopicHint: %+v", prEvidence.TopicHint)
	}

	var remoteRefs, localRefs int
	for _, ref := range prEvidence.SourceRefs {
		switch ref.Kind {
		case evidence.SourceRemotePR:
			remoteRefs++
		case evidence.SourceLocalGit:
			localRefs++
		}
	}
	if remoteRefs != 1 || localRefs != 2 {
		t.Errorf("expected 1 remote_pr ref + 2 local_git refs, got %d/%d", remoteRefs, localRefs)
	}

	// Each folded commit's SourceRef carries that commit's own time.
	atBySHA := map[string]time.Time{}
	for _, ref := range prEvidence.SourceRefs {
		if ref.Kind == evidence.SourceLocalGit {
			atBySHA[ref.CommitSHA] = ref.At
		}
	}
	if got := atBySHA["sha1"]; !got.Equal(time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("folded sha1 At = %v, want 09:00", got)
	}
	if got := atBySHA["sha2"]; !got.Equal(time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("folded sha2 At = %v, want 10:00", got)
	}

	if len(standalone) != 1 || standalone[0].SourceRefs[0].CommitSHA != "sha3" {
		t.Fatalf("expected the unclaimed commit sha3 as the only standalone, got %+v", standalone)
	}
	if got := standalone[0].SourceRefs[0].At; !got.Equal(time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("standalone sha3 At = %v, want 16:00", got)
	}
	if standalone[0].Duration.Known {
		t.Error("standalone commit must carry an unknown duration")
	}
}

// TestGitPlugin_SameBranchTwoReposDistinct proves identical branch names in
// different repositories never merge.
func TestGitPlugin_SameBranchTwoReposDistinct(t *testing.T) {
	gh := &fakeGitHub{
		commits: []git.GitHubCommit{{SHA: "shaX", Message: "work", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, nil))

	ev, _, err := reg.CollectDay(context.Background(),
		[]evidence.SourceConfig{gitConfig("acme/app,other/app")},
		evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if len(ev) != 2 {
		t.Fatalf("expected 2 standalone commits, got %d", len(ev))
	}
	if ev[0].ID == ev[1].ID {
		t.Errorf("same commit SHA in two repos produced the same ID %q", ev[0].ID)
	}
	if ev[0].TopicHint.Repository == ev[1].TopicHint.Repository {
		t.Errorf("expected distinct repo hints, got %q", ev[0].TopicHint.Repository)
	}
}

// TestGitPlugin_NoPRNumberInferenceFromBranch proves a branch name never becomes
// a PR number.
func TestGitPlugin_NoPRNumberInferenceFromBranch(t *testing.T) {
	local := &fakeLocal{
		branch:  "feature/123-do-thing",
		commits: []git.GitHubCommit{{SHA: "shaB", Message: "no pr here", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(nil, local))

	ev, _, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{gitConfig("acme/app")}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("expected 1 standalone commit, got %d", len(ev))
	}
	if ev[0].TopicHint == nil {
		t.Fatal("expected a topic hint")
	}
	if ev[0].TopicHint.PRNumber != 0 {
		t.Errorf("PR number was inferred from a branch name: %d", ev[0].TopicHint.PRNumber)
	}
	if ev[0].TopicHint.Branch != "feature/123-do-thing" {
		t.Errorf("expected branch hint, got %q", ev[0].TopicHint.Branch)
	}
}

// TestGitPlugin_LocalPRReferenceFromCommitMessage proves a PR reference in a
// commit message creates PR evidence, while the branch number is ignored.
func TestGitPlugin_LocalPRReferenceFromCommitMessage(t *testing.T) {
	commit := git.GitHubCommit{SHA: "shaM", Message: "fixes #42", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}
	local := &fakeLocal{
		branch:  "feature/999",
		commits: []git.GitHubCommit{commit},
		refs:    map[int][]git.GitHubCommit{42: {commit}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(nil, local))

	ev, _, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{gitConfig("acme/app")}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("expected 1 PR evidence item (commit folded), got %d", len(ev))
	}
	if ev[0].Kind != evidence.SourceRemotePR {
		t.Fatalf("expected remote_pr evidence, got %q", ev[0].Kind)
	}
	if ev[0].TopicHint == nil || ev[0].TopicHint.PRNumber != 42 {
		t.Errorf("expected PR 42 from commit message, got %+v", ev[0].TopicHint)
	}
}

// TestGitPlugin_ProjectHintsFromMapOnly proves project hints come from the
// configured repo->project map, never from commit/PR titles.
func TestGitPlugin_ProjectHintsFromMapOnly(t *testing.T) {
	gh := &fakeGitHub{
		commits: []git.GitHubCommit{{SHA: "shaP", Message: "Some Project Name In Title", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, nil))

	cfg := gitConfig("acme/app")
	cfg.Config[GitConfigProjectMap] = `{"acme/app":"RealProject"}`

	ev, _, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(ev))
	}
	if len(ev[0].ProjectHints) != 1 || ev[0].ProjectHints[0] != "RealProject" {
		t.Errorf("expected ProjectHints [RealProject], got %v", ev[0].ProjectHints)
	}
}

// TestGitPlugin_ContractDeterminism proves the fixed git fixture yields a
// complete determinism contract check.
func TestGitPlugin_ContractDeterminism(t *testing.T) {
	fixtureDoc := `{
		"repos": [
			{
				"repo": "acme/app",
				"branch": "main",
				"commits": [
					{"sha": "c1", "message": "one", "timestamp": "2026-10-06T09:00:00Z"},
					{"sha": "c2", "message": "two", "timestamp": "2026-10-06T10:00:00Z"}
				],
				"prs": [{"number": 3, "title": "PR three", "branch": "feature/three", "commits": ["c1"]}]
			}
		]
	}`
	dir := t.TempDir()
	fixture := filepath.Join(dir, "git.json")
	if err := os.WriteFile(fixture, []byte(fixtureDoc), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(nil, nil))
	cfg := gitConfig("acme/app")
	cfg.Config[GitConfigFixture] = fixture

	results, err := reg.VerifyContracts(context.Background(), []evidence.SourceConfig{cfg})
	if err != nil {
		t.Fatalf("VerifyContracts: %v", err)
	}
	if len(results) != 1 || results[0].Status != evidence.StatusComplete {
		t.Fatalf("expected one complete contract result, got %+v", results)
	}
}

// TestGitPlugin_SecretFree proves a sentinel secret in config never appears in
// serialized evidence or results.
func TestGitPlugin_SecretFree(t *testing.T) {
	const sentinel = "GIT-SENTINEL-7b2c"
	gh := &fakeGitHub{commits: []git.GitHubCommit{{SHA: "shaS", Message: "work", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}}}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, nil))

	cfg := gitConfig("acme/app")
	cfg.Config[GitConfigAuthor] = "keychain:" + sentinel

	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	artifact, _ := json.Marshal(struct {
		Evidence []evidence.Evidence        `json:"evidence"`
		Summary  evidence.CollectionSummary `json:"summary"`
	}{Evidence: ev, Summary: summary})
	if strings.Contains(string(artifact), sentinel) {
		t.Errorf("secret leaked into serialized output: %s", artifact)
	}
}

// TestGitHubPRSource_RejectsLocalCollector proves a local collector (whose
// FetchDayActivity infers PRs from branch names) can never satisfy the GitHub
// source interface, so it cannot be mis-wired as the remote source.
func TestGitHubPRSource_RejectsLocalCollector(t *testing.T) {
	if _, ok := any(&git.LocalGitCollector{}).(GitHubPRSource); ok {
		t.Fatal("LocalGitCollector must not satisfy GitHubPRSource (branch-based PR inference)")
	}
	if _, ok := any(&git.GitHubClient{}).(GitHubPRSource); ok {
		t.Fatal("a raw GitHubClient must go through GitHubClientAdapter, not satisfy GitHubPRSource directly")
	}
	if _, ok := any(GitHubClientAdapter{}).(GitHubPRSource); !ok {
		t.Fatal("GitHubClientAdapter must satisfy GitHubPRSource")
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// runGitCmdEnv runs a git command with extra environment variables appended to
// the current process environment. It is used to pin a commit's author and
// committer dates so a test never depends on the host clock or zone.
func runGitCmdEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestLocalGitCollectorAdapter_UsesMessageReferencesOnly drives the real adapter
// over a temp git repo whose branch name carries a number (feature/999) while the
// commit message references #42, proving PR discovery uses commit-message
// references only, never the branch name.
//
// The commit's author and committer dates are pinned to a fixed instant inside
// the 00:00-06:59 +07 window (2026-10-06T05:00:00+07:00 == 2026-10-05T22:00:00Z),
// whose LOCAL (Asia/Ho_Chi_Minh) day differs from its UTC day. This makes the
// test deterministic at every wall-clock time and host timezone: no time.Now and
// no TZ mutation, only explicit *time.Location values.
func TestLocalGitCollectorAdapter_UsesMessageReferencesOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}

	// Fixed commit instant: local day 2026-10-06 (+07), UTC day 2026-10-05.
	const commitISO = "2026-10-06T05:00:00+07:00"

	dir := t.TempDir()
	runGitCmd(t, dir, "init", "-q")
	runGitCmd(t, dir, "config", "user.email", "test@example.com")
	runGitCmd(t, dir, "config", "user.name", "Test User")
	runGitCmd(t, dir, "checkout", "-q", "-b", "feature/999")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitCmd(t, dir, "add", ".")
	runGitCmdEnv(t, dir,
		[]string{"GIT_AUTHOR_DATE=" + commitISO, "GIT_COMMITTER_DATE=" + commitISO},
		"commit", "-q", "-m", "fixes #42")

	adapter := LocalGitCollectorAdapter{Collector: git.NewLocalGitCollector(dir)}
	ctx := context.Background()

	if branch := adapter.CurrentBranch(ctx); branch != "feature/999" {
		t.Fatalf("expected branch feature/999, got %q", branch)
	}

	// The commit is found for its LOCAL (Asia/Ho_Chi_Minh) day even though that
	// day differs from its UTC day.
	commits, err := adapter.FetchDayCommits(ctx, "", time.Date(2026, 10, 6, 12, 0, 0, 0, hcm))
	if err != nil {
		t.Fatalf("FetchDayCommits (local day): %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit in the local-day window, got %d", len(commits))
	}
	refs := adapter.DiscoverPRReferences(commits)
	if _, ok := refs[42]; !ok {
		t.Errorf("expected PR 42 from the commit message, got %v", refs)
	}
	if _, ok := refs[999]; ok {
		t.Errorf("branch name 999 must not create a PR reference, got %v", refs)
	}

	// Deterministic fold on the nil-Location back-compat path. This request
	// deliberately omits Location, so parseDate applies its documented
	// nil=>UTC default and the window is the commit's UTC day (2026-10-05). The
	// runner now supplies the configured zone via CollectRequest.Location
	// (GIT-DATE-WINDOW-1), so this is no longer a production defect: it exercises
	// only the preserved nil=>UTC default, independent of the host zone and clock.
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(nil, adapter))
	ev, _, err := reg.CollectDay(ctx, []evidence.SourceConfig{gitConfig("acme/app")}, evidence.CollectRequest{Date: "2026-10-05"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if len(ev) != 1 || ev[0].Kind != evidence.SourceRemotePR {
		t.Fatalf("expected 1 PR evidence (commit folded), got %+v", ev)
	}
	if ev[0].TopicHint == nil || ev[0].TopicHint.PRNumber != 42 {
		t.Errorf("expected PR 42 evidence, got %+v", ev[0].TopicHint)
	}
}

// TestLocalGitCollectorAdapter_LocalDayBoundaryWindow covers the 00:00-06:59
// +07 window where a commit's LOCAL (Asia/Ho_Chi_Minh) day differs from its UTC
// day. Three commits are pinned to fixed instants; a single explicit
// Asia/Ho_Chi_Minh FetchDayCommits window for local day 2026-10-06 must find all
// three. The adapter windows by date.Location() (here an explicit
// Asia/Ho_Chi_Minh supplied by the caller), independent of the plugin's nil=>UTC
// date parse.
func TestLocalGitCollectorAdapter_LocalDayBoundaryWindow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}

	// Each fixed instant has LOCAL day 2026-10-06 (+07); the UTC day is noted per
	// commit. The 07:00 case is the first instant whose UTC day also matches.
	cases := []struct {
		name string
		iso  string // local day 2026-10-06 (+07)
		utc  string // resulting UTC day
	}{
		{"lower-bound 00:00 +07", "2026-10-06T00:00:00+07:00", "2026-10-05"},    // == 2026-10-05T17:00:00Z
		{"upper-bound 06:59 +07", "2026-10-06T06:59:00+07:00", "2026-10-05"},    // == 2026-10-05T23:59:00Z
		{"first matching 07:00 +07", "2026-10-06T07:00:00+07:00", "2026-10-06"}, // == 2026-10-06T00:00:00Z
	}

	dir := t.TempDir()
	runGitCmd(t, dir, "init", "-q")
	runGitCmd(t, dir, "config", "user.email", "test@example.com")
	runGitCmd(t, dir, "config", "user.name", "Test User")
	for _, c := range cases {
		runGitCmdEnv(t, dir,
			[]string{"GIT_AUTHOR_DATE=" + c.iso, "GIT_COMMITTER_DATE=" + c.iso},
			"commit", "-q", "--allow-empty", "-m", "boundary: "+c.name+" (UTC day "+c.utc+")")
	}

	adapter := LocalGitCollectorAdapter{Collector: git.NewLocalGitCollector(dir)}
	commits, err := adapter.FetchDayCommits(context.Background(), "", time.Date(2026, 10, 6, 12, 0, 0, 0, hcm))
	if err != nil {
		t.Fatalf("FetchDayCommits (local day 2026-10-06 +07): %v", err)
	}
	if len(commits) != len(cases) {
		t.Fatalf("expected %d commits in the local-day window, got %d: %+v", len(cases), len(commits), commits)
	}
	// Every commit's own instant still reports its fixed offset instant.
	for _, c := range cases {
		want, perr := time.Parse(time.RFC3339, c.iso)
		if perr != nil {
			t.Fatalf("parse %q: %v", c.iso, perr)
		}
		found := false
		for _, got := range commits {
			if got.Timestamp.Equal(want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("commit at %s (UTC day %s) not found in the local-day window", c.iso, c.utc)
		}
	}
}

// TestGitPlugin_LocalOnlyNeverCallsGitHub proves mode=local collects local
// evidence and never contacts the remote (GitHub is never called).
func TestGitPlugin_LocalOnlyNeverCallsGitHub(t *testing.T) {
	gh := &countingGitHub{}
	local := &fakeLocal{
		repoName: "acme/app",
		branch:   "main",
		commits:  []git.GitHubCommit{{SHA: "l1", Message: "local one", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, local))

	cfg := gitConfig("")
	cfg.Config[GitConfigMode] = GitModeLocal
	cfg.Config[GitConfigLocalRepos] = "."

	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if gh.calls != 0 {
		t.Fatalf("mode=local must never call GitHub, got %d call(s)", gh.calls)
	}
	if !summary.Complete {
		t.Fatalf("a local-only success must be complete, got %+v", summary)
	}
	if len(ev) != 1 || ev[0].Kind != evidence.SourceLocalGit {
		t.Fatalf("want 1 local_git evidence item, got %+v", ev)
	}
}

// TestGitPlugin_RemoteFailureRetainsLocalEvidence proves a remote failure does
// NOT abort collection: local evidence is retained and the source is partial with
// a stable, secret-free warning naming the remote failure.
func TestGitPlugin_RemoteFailureRetainsLocalEvidence(t *testing.T) {
	gh := &errGitHub{}
	local := &fakeLocal{
		repoName: "acme/app",
		branch:   "main",
		commits:  []git.GitHubCommit{{SHA: "l1", Message: "local one", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, local))

	cfg := gitConfig("acme/app") // mode defaults to both

	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if summary.Complete {
		t.Fatal("a remote failure must not be reported as complete")
	}
	if len(ev) != 1 || ev[0].Kind != evidence.SourceLocalGit {
		t.Fatalf("local evidence must be retained on remote failure, got %+v", ev)
	}
	if len(summary.Sources) != 1 {
		t.Fatalf("want one source result, got %+v", summary.Sources)
	}
	sr := summary.Sources[0]
	if sr.Status != evidence.StatusPartial {
		t.Fatalf("want StatusPartial (evidence retained), got %q", sr.Status)
	}
	if !containsString(sr.Warnings, WarningGitRemoteFetchFailed) {
		t.Fatalf("want the remote-failure warning, got %v", sr.Warnings)
	}
}

// TestGitPlugin_CheckContractModeRequirements proves mode-appropriate contract
// enforcement with stable codes.
func TestGitPlugin_CheckContractModeRequirements(t *testing.T) {
	p := NewGitPlugin(nil, nil)

	invalid := gitConfig("acme/app")
	invalid.Config[GitConfigMode] = "sideways"
	if !hasFailedCheck(p.CheckContract(context.Background(), invalid), CodeGitModeInvalid) {
		t.Fatalf("an invalid mode must fail with %s", CodeGitModeInvalid)
	}

	remoteNoRepos := gitConfig("")
	remoteNoRepos.Config[GitConfigMode] = GitModeRemote
	if !hasFailedCheck(p.CheckContract(context.Background(), remoteNoRepos), CodeGitReposRequired) {
		t.Fatalf("mode=remote without repos must fail with %s", CodeGitReposRequired)
	}

	localNoRepos := gitConfig("")
	localNoRepos.Config[GitConfigMode] = GitModeLocal
	for _, c := range p.CheckContract(context.Background(), localNoRepos) {
		if !c.Passed {
			t.Fatalf("mode=local without repos must pass (default local path), got %+v", c)
		}
	}
}

func hasFailedCheck(checks []evidence.ContractCheck, code string) bool {
	for _, c := range checks {
		if !c.Passed && c.Code == code {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// F5 (CONFIG-COLLECT-1): dedup/provenance holds when the local checkout's
// RepoName canonicalizes to the configured repos slug. The same commit SHA on
// both sides is emitted ONCE (folded into the PR record), and every SourceRef
// carries the canonical repository key.
func TestGitPlugin_DedupWhenLocalMatchesConfiguredSlug(t *testing.T) {
	sha := "sha-dedup"
	ts := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	gh := &fakeGitHub{
		commits: []git.GitHubCommit{{SHA: sha, Message: "remote copy", Timestamp: ts}},
		prs:     []git.GitHubPR{{Number: 9, Title: "PR nine", Branch: "feature/nine", Commits: []string{sha}}},
	}
	local := &fakeLocal{
		repoName: "acme/app", // canonicalizes to the configured slug
		branch:   "feature/nine",
		commits:  []git.GitHubCommit{{SHA: sha, Message: "local copy", Timestamp: ts}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, local))

	cfg := gitConfig("acme/app") // default mode = both
	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if !summary.Complete {
		t.Fatalf("want a complete collection, got %+v", summary)
	}
	if len(ev) != 1 {
		t.Fatalf("the same SHA on both sides must be emitted once, got %d: %+v", len(ev), ev)
	}
	if ev[0].Kind != evidence.SourceRemotePR {
		t.Fatalf("want the folded PR record, got %q", ev[0].Kind)
	}
	wantKey := evidence.CanonicalRepoKey("acme/app", "acme/app")
	for _, ref := range ev[0].SourceRefs {
		if ref.Repository != wantKey {
			t.Errorf("SourceRef.Repository = %q, want canonical %q", ref.Repository, wantKey)
		}
	}
	var remoteRefs, localRefs int
	for _, ref := range ev[0].SourceRefs {
		switch ref.Kind {
		case evidence.SourceRemotePR:
			remoteRefs++
		case evidence.SourceLocalGit:
			localRefs++
		}
	}
	if remoteRefs != 1 || localRefs != 1 {
		t.Fatalf("want 1 remote_pr + 1 folded local_git ref, got %d/%d", remoteRefs, localRefs)
	}
}

// F6 (CONFIG-COLLECT-1): identical remote-failure warnings are deduplicated, so a
// warning appears once even when several configured repos fail.
func TestGitPlugin_RemoteFailureWarningDeduplicated(t *testing.T) {
	gh := &errGitHub{}
	local := &fakeLocal{
		repoName: "acme/app",
		commits:  []git.GitHubCommit{{SHA: "l1", Message: "local", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}},
	}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, local))

	cfg := gitConfig("acme/app,acme/lib") // two remote repos, both fail
	_, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if len(summary.Sources) != 1 {
		t.Fatalf("want one source result, got %+v", summary.Sources)
	}
	count := 0
	for _, w := range summary.Sources[0].Warnings {
		if w == WarningGitRemoteFetchFailed {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one deduplicated remote-failure warning, got %d (%v)", count, summary.Sources[0].Warnings)
	}
}

// PREFLIGHT-RUNTIME-1: runtime validation of a git source passes WITHOUT a
// fixture, and mode-appropriate config is still enforced.
func TestGitPlugin_RuntimeValidationNoFixture(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(nil, nil))
	ctx := context.Background()

	// local-only passes without a fixture.
	local := gitConfig("")
	local.Config[GitConfigMode] = GitModeLocal
	res, err := reg.ValidateSources(ctx, []evidence.SourceConfig{local})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if res[0].Status != evidence.StatusComplete {
		t.Fatalf("local-only must validate complete without a fixture, got %+v", res[0])
	}

	// mode=remote without repos fails.
	remote := gitConfig("")
	remote.Config[GitConfigMode] = GitModeRemote
	res, err = reg.ValidateSources(ctx, []evidence.SourceConfig{remote})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if res[0].Status != evidence.StatusIncomplete || res[0].ErrorCode != CodeGitReposRequired {
		t.Fatalf("mode=remote without repos must fail %s, got %+v", CodeGitReposRequired, res[0])
	}

	// an invalid mode fails.
	invalid := gitConfig("acme/app")
	invalid.Config[GitConfigMode] = "sideways"
	res, err = reg.ValidateSources(ctx, []evidence.SourceConfig{invalid})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if res[0].ErrorCode != CodeGitModeInvalid {
		t.Fatalf("an invalid mode must fail %s, got %+v", CodeGitModeInvalid, res[0])
	}
}

// PREFLIGHT-RUNTIME-1: fixture-based verification is UNCHANGED. VerifyContracts
// still requires a fixture (fixtures are TEST inputs, not runtime config).
func TestGitPlugin_VerifyContractsRequiresFixture(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(nil, nil))

	cfg := gitConfig("acme/app") // no fixture
	results, err := reg.VerifyContracts(context.Background(), []evidence.SourceConfig{cfg})
	if err != nil {
		t.Fatalf("VerifyContracts: %v", err)
	}
	if len(results) != 1 || results[0].Status != evidence.StatusIncomplete {
		t.Fatalf("VerifyContracts must require a fixture, got %+v", results)
	}
}

// GIT-DATE-WINDOW-1: CollectRequest.Location anchors the parsed target date (and
// therefore both the local and remote day windows) to the configured zone; a nil
// Location preserves the historical UTC parse.

// recordingLocalSource records the day instant handed to the local sub-source.
type recordingLocalSource struct {
	date  time.Time
	calls int
}

func (r *recordingLocalSource) CurrentBranch(context.Context) string { return "main" }
func (r *recordingLocalSource) RepoName(context.Context) string      { return "acme/app" }
func (r *recordingLocalSource) FetchDayCommits(_ context.Context, _ string, date time.Time) ([]git.GitHubCommit, error) {
	r.date = date
	r.calls++
	return nil, nil
}
func (r *recordingLocalSource) DiscoverPRReferences([]git.GitHubCommit) map[int][]git.GitHubCommit {
	return nil
}

// recordingRemoteSource records the day instant handed to the remote sub-source.
type recordingRemoteSource struct {
	date  time.Time
	calls int
}

func (r *recordingRemoteSource) FetchRemoteDayActivity(_ context.Context, _ string, date time.Time) ([]git.GitHubCommit, []git.GitHubPR, error) {
	r.date = date
	r.calls++
	return nil, nil, nil
}

func TestGitPlugin_CollectRequestLocationAnchorsDate(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}

	// With Location: both sub-sources receive HCM midnight.
	local := &recordingLocalSource{}
	gh := &recordingRemoteSource{}
	reg := evidence.NewRegistry()
	reg.Register(NewGitPlugin(gh, local))
	if _, _, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{gitConfig("acme/app")},
		evidence.CollectRequest{Date: "2026-10-06", Location: hcm}); err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	want := time.Date(2026, 10, 6, 0, 0, 0, 0, hcm)
	if !local.date.Equal(want) || local.date.Location() != hcm {
		t.Errorf("local date = %v (loc %v), want %v (loc HCM)", local.date, local.date.Location(), want)
	}
	if !gh.date.Equal(want) || gh.date.Location() != hcm {
		t.Errorf("remote date = %v (loc %v), want %v (loc HCM)", gh.date, gh.date.Location(), want)
	}

	// With nil Location: the date is still parsed as UTC.
	local2 := &recordingLocalSource{}
	gh2 := &recordingRemoteSource{}
	reg2 := evidence.NewRegistry()
	reg2.Register(NewGitPlugin(gh2, local2))
	if _, _, err := reg2.CollectDay(context.Background(), []evidence.SourceConfig{gitConfig("acme/app")},
		evidence.CollectRequest{Date: "2026-10-06"}); err != nil {
		t.Fatalf("CollectDay (nil location): %v", err)
	}
	wantUTC := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if !local2.date.Equal(wantUTC) || local2.date.Location() != time.UTC {
		t.Errorf("nil-location local date = %v (loc %v), want %v (loc UTC)", local2.date, local2.date.Location(), wantUTC)
	}
	if !gh2.date.Equal(wantUTC) || gh2.date.Location() != time.UTC {
		t.Errorf("nil-location remote date = %v (loc %v), want %v (loc UTC)", gh2.date, gh2.date.Location(), wantUTC)
	}
}
