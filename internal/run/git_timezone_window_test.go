package run

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
)

// recordingLocal records the day instant the git plugin hands to its local
// sub-source and returns no commits.
type recordingLocal struct {
	date  time.Time
	calls int
}

func (r *recordingLocal) CurrentBranch(context.Context) string { return "main" }
func (r *recordingLocal) RepoName(context.Context) string      { return "acme/app" }
func (r *recordingLocal) FetchDayCommits(_ context.Context, _ string, date time.Time) ([]git.GitHubCommit, error) {
	r.date = date
	r.calls++
	return nil, nil
}
func (r *recordingLocal) DiscoverPRReferences([]git.GitHubCommit) map[int][]git.GitHubCommit {
	return nil
}

// recordingRemote records the day instant the git plugin hands to its remote
// sub-source and returns no activity.
type recordingRemote struct {
	date  time.Time
	calls int
}

func (r *recordingRemote) FetchRemoteDayActivity(_ context.Context, _ string, date time.Time) ([]git.GitHubCommit, []git.GitHubPR, error) {
	r.date = date
	r.calls++
	return nil, nil, nil
}

// GIT-DATE-WINDOW-1: the RUNNER picks the target date in the configured timezone
// and must hand the git plugin the same zone, so both the local and remote day
// windows are anchored to the configured timezone's midnight (NOT UTC). The
// recorder must receive 2026-10-06T00:00:00 +07:00 carrying the runner's own
// Asia/Ho_Chi_Minh location. Repeated under differing host TZ values, the
// received instants must be identical (host-TZ independence).
func TestCollect_GitWindowAnchoredToConfiguredTimezone(t *testing.T) {
	// 2026-10-05T20:00Z is 2026-10-06T03:00 +07, so the runner's Date() is
	// 2026-10-06 in Asia/Ho_Chi_Minh.
	now := time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)

	for _, hostTZ := range []string{"UTC", "America/New_York", "Pacific/Kiritimati"} {
		t.Run("host_TZ="+hostTZ, func(t *testing.T) {
			t.Setenv("TZ", hostTZ)

			store := newStore(t)
			local := &recordingLocal{}
			gh := &recordingRemote{}
			reg := evidence.NewRegistry()
			reg.Register(plugins.NewGitPlugin(gh, local))
			sources := []evidence.SourceConfig{{
				ID: "git", Type: plugins.TypeGit, Enabled: true,
				Config: map[string]string{plugins.GitConfigRepos: "acme/app"},
			}}
			r := newRunner(t, Deps{Store: store, Registry: reg, Now: func() time.Time { return now }}, Config{
				Evidence: evidence.Config{Timezone: "Asia/Ho_Chi_Minh", Sources: sources},
			})

			hcm := r.loc
			if hcm == nil || hcm.String() != "Asia/Ho_Chi_Minh" {
				t.Fatalf("runner location = %v, want Asia/Ho_Chi_Minh", hcm)
			}

			date, err := r.Date()
			if err != nil {
				t.Fatalf("Date: %v", err)
			}
			if date != "2026-10-06" {
				t.Fatalf("runner date = %q, want 2026-10-06", date)
			}
			if _, err := r.Collect(ctx, date); err != nil {
				t.Fatalf("Collect: %v", err)
			}

			want := time.Date(2026, 10, 6, 0, 0, 0, 0, hcm)
			if local.calls != 1 || gh.calls != 1 {
				t.Fatalf("expected one local and one remote call, got %d/%d", local.calls, gh.calls)
			}
			if !local.date.Equal(want) || local.date.Location() != hcm {
				t.Errorf("local window instant = %v (loc %v), want %v", local.date, local.date.Location(), want)
			}
			if !gh.date.Equal(want) || gh.date.Location() != hcm {
				t.Errorf("remote window instant = %v (loc %v), want %v", gh.date, gh.date.Location(), want)
			}
		})
	}
}
