package run

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// S8L1-7: Collect merges evidence into the record (CollectedEvidence upsert +
// legacy references + collection summary), and a re-collection upserts.
func TestCollect_MergesEvidence(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	reg := evidence.NewRegistry()
	plug := &fakePlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")}}
	reg.Register(plug)
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: true}}
	r := newRunner(t, Deps{Store: store, Registry: reg}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: sources}})

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res.Evidence) != 1 || res.MergedCount != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if plug.liveCalls != 1 {
		t.Fatalf("want one live collect, got %d", plug.liveCalls)
	}

	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if len(rec.CollectedEvidence) != 1 || rec.CollectedEvidence[0].ID != "e1" {
		t.Fatalf("want collected evidence, got %+v", rec.CollectedEvidence)
	}
	if rec.Collection == nil || !rec.Collection.Complete {
		t.Fatalf("want a complete collection summary, got %+v", rec.Collection)
	}
	if len(rec.Evidence) != 1 || rec.Evidence[0].ID != "e1" {
		t.Fatalf("want a legacy evidence reference, got %+v", rec.Evidence)
	}
	if rec.Evidence[0].Source != string(evidence.SourceLocalGit) {
		t.Errorf("want the source kind on the legacy ref, got %q", rec.Evidence[0].Source)
	}

	// A second collection upserts rather than duplicating.
	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	rec2, _ := store.GetDay(ctx, date)
	if len(rec2.CollectedEvidence) != 1 {
		t.Fatalf("a re-collection must upsert collected evidence, got %d", len(rec2.CollectedEvidence))
	}
	if len(rec2.Evidence) != 1 {
		t.Fatalf("legacy refs must upsert, got %d", len(rec2.Evidence))
	}
}

// F2: a NO-SOURCE collection must not clobber a prior INCOMPLETE collection summary
// (the readiness gate must still see the incomplete collection).
func TestCollect_NoSourcesPreservesIncompleteCollection(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{}) // NO sources

	date := "2026-10-06"
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.Collection = &evidence.CollectionSummary{
			Date: date,
			Sources: []evidence.SourceResult{
				{SourceID: "cal", Type: evidence.SourceCalendar, Status: evidence.StatusIncomplete, ErrorCode: "SOURCE_ERROR"},
			},
			Complete: false,
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if rec.Collection == nil || rec.Collection.Complete || len(rec.Collection.Sources) != 1 {
		t.Fatalf("a no-source collection must preserve the prior incomplete summary, got %+v", rec.Collection)
	}
	rd := review.Readiness(*rec)
	blocked := false
	for _, reason := range rd.Reasons {
		if strings.Contains(reason, "incomplete_collection") {
			blocked = true
		}
	}
	if !rd.Blocked || !blocked {
		t.Fatalf("readiness must still report incomplete_collection, got %v", rd.Reasons)
	}
}

// --- Finding A: no configured/enabled evidence sources ----------------------

// TestCollect_NoSourcesConfiguredFlag proves the explicit condition is true when
// ZERO sources are enabled, and the day still produces an empty draft.
func TestCollect_NoSourcesConfiguredFlag(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{}) // no sources

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !res.NoSourcesConfigured {
		t.Fatal("zero enabled sources must set NoSourcesConfigured")
	}
	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if n := len(prop.Work) + len(prop.Meetings) + len(prop.Manual); n != 0 {
		t.Fatalf("an empty day must produce an empty draft, got %d block(s)", n)
	}
}

// TestCollect_AllSourcesDisabledReportsNoSources proves an all-disabled source
// list also satisfies the no-sources predicate (len(sources) is not sufficient).
func TestCollect_AllSourcesDisabledReportsNoSources(t *testing.T) {
	store := newStore(t)
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: false}}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{Evidence: evidence.Config{Sources: sources}})

	res, err := r.Collect(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !res.NoSourcesConfigured {
		t.Fatal("an all-disabled source list must set NoSourcesConfigured")
	}
}

// F3 (CONFIG-COLLECT-1): an all-disabled source list must not clobber a prior
// INCOMPLETE collection summary (the readiness gate must still see it).
func TestCollect_AllDisabledPreservesPriorIncompleteSummary(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: false}}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{Evidence: evidence.Config{Sources: sources}})

	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.Collection = &evidence.CollectionSummary{
			Date: date,
			Sources: []evidence.SourceResult{
				{SourceID: "cal", Type: evidence.SourceCalendar, Status: evidence.StatusIncomplete, ErrorCode: "SOURCE_ERROR"},
			},
			Complete: false,
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if rec.Collection == nil || rec.Collection.Complete || len(rec.Collection.Sources) != 1 || rec.Collection.Sources[0].SourceID != "cal" {
		t.Fatalf("an all-disabled collection must preserve the prior incomplete summary, got %+v", rec.Collection)
	}
}

// TestCollect_EnabledSourceNoEvidenceIsNotNoSources proves a configured+enabled
// source that legitimately returns no evidence is NOT reported as no-sources and
// collection is complete.
func TestCollect_EnabledSourceNoEvidenceIsNotNoSources(t *testing.T) {
	store := newStore(t)
	reg := evidence.NewRegistry()
	reg.Register(&fakePlugin{kind: evidence.SourceLocalGit}) // enabled, returns nothing
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: true}}
	r := newRunner(t, Deps{Store: store, Registry: reg}, Config{Evidence: evidence.Config{Sources: sources}})

	res, err := r.Collect(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.NoSourcesConfigured {
		t.Fatal("an enabled source must not satisfy the no-sources predicate")
	}
	if !res.Summary.Complete {
		t.Fatalf("a configured source returning no evidence is complete, got %+v", res.Summary)
	}
}

// TestCollect_IncompleteSourceIsNotComplete is the regression guard: an
// incomplete (failing) collection must never be marked complete.
func TestCollect_IncompleteSourceIsNotComplete(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	reg := evidence.NewRegistry()
	reg.Register(&fakePlugin{kind: evidence.SourceLocalGit, err: errors.New("boom")})
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: true}}
	r := newRunner(t, Deps{Store: store, Registry: reg}, Config{Evidence: evidence.Config{Sources: sources}})

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.Summary.Complete {
		t.Fatal("a failing source must not produce a complete collection")
	}
	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if rec.Collection == nil || rec.Collection.Complete {
		t.Fatalf("the persisted summary must remain incomplete, got %+v", rec.Collection)
	}
}

// --- Finding B: local-only git + retain local evidence on remote failure -----

// errGitHub fails every remote call.
type errGitHub struct{ calls int }

func (f *errGitHub) FetchRemoteDayActivity(context.Context, string, time.Time) ([]git.GitHubCommit, []git.GitHubPR, error) {
	f.calls++
	return nil, nil, errors.New("remote down")
}

// fixedLocal returns fixed commits and a repo identity.
type fixedLocal struct {
	repoName string
	commits  []git.GitHubCommit
}

func (f *fixedLocal) CurrentBranch(context.Context) string { return "main" }
func (f *fixedLocal) RepoName(context.Context) string      { return f.repoName }
func (f *fixedLocal) FetchDayCommits(context.Context, string, time.Time) ([]git.GitHubCommit, error) {
	return f.commits, nil
}
func (f *fixedLocal) DiscoverPRReferences([]git.GitHubCommit) map[int][]git.GitHubCommit {
	return nil
}

// TestCollect_RemoteFailureBlocksUntilAcknowledged proves a remote-failed
// collection retains local evidence, is not complete, blocks readiness with
// incomplete_collection, and unblocks once the source is acknowledged.
func TestCollect_RemoteFailureBlocksUntilAcknowledged(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	gh := &errGitHub{}
	local := &fixedLocal{
		repoName: "acme/app",
		commits:  []git.GitHubCommit{{SHA: "sha1", Message: "local work", Timestamp: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}},
	}
	reg := evidence.NewRegistry()
	reg.Register(plugins.NewGitPlugin(gh, local))
	sources := []evidence.SourceConfig{{
		ID: "git", Type: plugins.TypeGit, Enabled: true,
		Config: map[string]string{"repos": "acme/app"},
	}}
	r := newRunner(t, Deps{Store: store, Registry: reg}, Config{Evidence: evidence.Config{Sources: sources}})

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.Summary.Complete {
		t.Fatal("a remote failure must not be complete")
	}
	if len(res.Evidence) == 0 {
		t.Fatal("local evidence must be retained when the remote fails")
	}
	warned := false
	for _, sr := range res.Summary.Sources {
		for _, w := range sr.Warnings {
			if w == plugins.WarningGitRemoteFetchFailed {
				warned = true
			}
		}
	}
	if !warned {
		t.Fatalf("the remote-failure warning must be present, got %+v", res.Summary)
	}

	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	rd := review.Readiness(*rec)
	if !rd.Blocked || !hasReason(rd.Reasons, "incomplete_collection") {
		t.Fatalf("readiness must block with incomplete_collection, got %v", rd.Reasons)
	}

	if err := r.Acknowledge(ctx, date, []string{"git"}); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	rec2, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay 2: %v", err)
	}
	if rd2 := review.Readiness(*rec2); rd2.Blocked {
		t.Fatalf("after acknowledgment readiness must clear, got %v", rd2.Reasons)
	}
}
