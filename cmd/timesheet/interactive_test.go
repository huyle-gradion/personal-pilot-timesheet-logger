package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/huh/v2"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/selection"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// --- fakes ------------------------------------------------------------------

// fakePrompter drives the REAL guided orchestration with scripted answers. An
// exhausted queue returns errAborted, so a test that under-scripts a scenario
// aborts instead of silently choosing.
type fakePrompter struct {
	choose   []int
	inputs   []string
	multi    [][]int
	confirms []bool

	ci, ii, mi, co int
	chooseTitles   []string
	confirmTitles  []string
	// chooseOptions records the option list offered at each Choose prompt, so a
	// test can assert WHICH options were presented (e.g. a mapped interval offers
	// the edit-instead-of-remove rule and no removal).
	chooseOptions [][]promptOption
	// confirmDefaults records the default offered at each Confirm prompt, so a
	// test can assert the guided final confirmation defaults to "no".
	confirmDefaults []bool
}

func (f *fakePrompter) Choose(_ context.Context, title string, options []promptOption, def int) (int, error) {
	f.chooseTitles = append(f.chooseTitles, title)
	f.chooseOptions = append(f.chooseOptions, options)
	if f.ci >= len(f.choose) {
		return 0, errAborted
	}
	v := f.choose[f.ci]
	f.ci++
	if v < 0 {
		return 0, errAborted
	}
	if v >= len(options) {
		return 0, fmt.Errorf("fakePrompter: choice %d out of range (%d options)", v, len(options))
	}
	return v, nil
}

func (f *fakePrompter) MultiChoose(_ context.Context, _ string, _ []promptOption) ([]int, error) {
	if f.mi >= len(f.multi) {
		return nil, nil
	}
	v := f.multi[f.mi]
	f.mi++
	return v, nil
}

func (f *fakePrompter) Input(_ context.Context, title, _, initial string) (string, error) {
	if f.ii >= len(f.inputs) {
		return "", errAborted
	}
	v := f.inputs[f.ii]
	f.ii++
	// Match BOTH real prompters: a blank answer keeps the initial value.
	if strings.TrimSpace(v) == "" {
		return initial, nil
	}
	return v, nil
}

func (f *fakePrompter) Confirm(_ context.Context, title string, def bool) (bool, error) {
	f.confirmTitles = append(f.confirmTitles, title)
	f.confirmDefaults = append(f.confirmDefaults, def)
	if f.co >= len(f.confirms) {
		return false, errAborted
	}
	v := f.confirms[f.co]
	f.co++
	return v, nil
}

// stubCatalog installs a hermetic live catalog for the guided classification
// step, reusing the classify test's fake fetcher.
func stubCatalog(t *testing.T, opts ...gradion.ClassificationOption) {
	t.Helper()
	withCatalogFetcher(t, &fakeCatalogFetcher{catalog: gradion.Catalog{
		Complete: true, ObservedAt: time.Now().UTC(), Options: opts,
	}})
}

func guidedTestConfig() cliConfig {
	cfg := cliConfig{}
	cfg.Evidence.Timezone = "Asia/Ho_Chi_Minh"
	cfg.Classifications = map[string]string{"Proj": "cls-1"}
	return cfg
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func newGuidedRunner(t *testing.T, storage string, remote run.Remote, cfg cliConfig) (*run.Runner, workflow.Store) {
	t.Helper()
	store, err := workflow.NewJSONFileStore(storage)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	r, err := run.New(run.Deps{
		Store:    store,
		Registry: evidence.NewRegistry(),
		Remote:   remote,
		Now:      func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) },
	}, cfg.Config)
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	return r, store
}

// --- mode precedence --------------------------------------------------------

func TestResolveMode_Precedence(t *testing.T) {
	cases := []struct {
		name    string
		req     guidedRequest
		want    interactionMode
		wantErr bool
	}{
		{"default is scripted", guidedRequest{}, modeScripted, false},
		{"decisions file forces scripted", guidedRequest{Interactive: true, Decisions: true, StdinTTY: true, StdoutTTY: true}, modeScripted, false},
		{"non-interactive forces scripted", guidedRequest{Interactive: true, NonInteractive: true, StdinTTY: true, StdoutTTY: true}, modeScripted, false},
		{"plain is allowed on a pipe", guidedRequest{Plain: true}, modePlain, false},
		{"plain wins over interactive", guidedRequest{Interactive: true, Plain: true, StdinTTY: true, StdoutTTY: true}, modePlain, false},
		{"interactive with both ttys is tui", guidedRequest{Interactive: true, StdinTTY: true, StdoutTTY: true}, modeTUI, false},
		{"interactive on a pipe refuses", guidedRequest{Interactive: true, StdinTTY: false, StdoutTTY: true}, modeScripted, true},
		{"interactive on a piped stdout refuses", guidedRequest{Interactive: true, StdinTTY: true, StdoutTTY: false}, modeScripted, true},
		{"interactive under CI refuses", guidedRequest{Interactive: true, StdinTTY: true, StdoutTTY: true, CI: true}, modeScripted, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveMode(tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error, got mode %v", got)
				}
				if !strings.Contains(err.Error(), "--plain") || !strings.Contains(err.Error(), "--decisions") {
					t.Fatalf("the refusal must point at --plain/--decisions, got %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want mode %v, got %v", tc.want, got)
			}
		})
	}
}

func TestGuided_InteractiveOnPipeRefuses(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, guidedTestConfig())
	var stdout, stderr bytes.Buffer
	code := runCLIWithIO(context.Background(),
		[]string{"run", "--interactive", "--config", cfgPath, "--storage-dir", filepath.Join(dir, "records"), "--date", testDate},
		strings.NewReader(""), &stdout, &stderr, false, false)
	if code != exitUsage {
		t.Fatalf("want a usage exit, got %d (%s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--plain") {
		t.Fatalf("the refusal must mention --plain, got:\n%s", stderr.String())
	}
}

func TestGuided_InteractiveUnderCIRefuses(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, guidedTestConfig())
	var stdout, stderr bytes.Buffer
	// Both descriptors are reported as terminals, but CI forbids a TUI.
	code := runCLIWithIO(context.Background(),
		[]string{"run", "--interactive", "--config", cfgPath, "--storage-dir", filepath.Join(dir, "records"), "--date", testDate},
		strings.NewReader(""), &stdout, &stderr, true, true)
	if code != exitUsage {
		t.Fatalf("want a usage exit under CI, got %d (%s)", code, stderr.String())
	}
}

// --- the real plain CLI path ------------------------------------------------

// plainScenarioStdin scripts the review of ONE git work block: edit it (index 2),
// set a real span and project, answer no to missing work, decline bulk actions
// (choice 1 = "No bulk actions"), pick catalog option 1, and confirm the review.
// The plain prompter numbers choices from 1.
const plainScenarioStdin = "3\n16:00\n17:00\nProj\ndid work\nn\n1\n1\ny\n"

func TestGuided_PlainRealPath_AppliesReviewAndPreviews(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "Gradion Intern Academy 2026", ActivityType: "internal project"})

	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, guidedTestConfig())
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})

	var stdout, stderr bytes.Buffer
	code := runCLIWithIO(context.Background(),
		[]string{"run", "--plain", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--no-editorial"},
		strings.NewReader(plainScenarioStdin), &stdout, &stderr, false, false)
	if code != exitOK {
		t.Fatalf("guided run: %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want 1 confirmed interval, got %+v", rec.Confirmed)
	}
	ci := rec.Confirmed[0]
	if ci.Project != "Proj" {
		t.Errorf("want project Proj, got %q", ci.Project)
	}
	if ci.Description != "did work" {
		t.Errorf("want description %q, got %q", "did work", ci.Description)
	}
	if ci.ClassificationID != "cls-1" || ci.ActivityType != "internal project" {
		t.Errorf("want the chosen classification + derived activity type, got %q / %q", ci.ClassificationID, ci.ActivityType)
	}
	if ci.ClassificationCode != "INTGRADI2610" || ci.ClassificationName != "Gradion Intern Academy 2026" {
		t.Errorf("want the catalog retrieval metadata captured, got %q / %q", ci.ClassificationCode, ci.ClassificationName)
	}
	if ci.StartTime != "16:00" || ci.EndTime != "17:00" {
		t.Errorf("want the edited span 16:00-17:00, got %s-%s", ci.StartTime, ci.EndTime)
	}
	if rec.Review == nil || !rec.Review.Reviewed {
		t.Fatalf("want the day reviewed, got %+v", rec.Review)
	}
	if len(rec.Approvals) != 0 {
		t.Fatalf("the guided run must create NO approval, got %+v", rec.Approvals)
	}

	out := stdout.String()
	for _, want := range []string{"date: " + testDate, "timezone: Asia/Ho_Chi_Minh", "plan_id: ", "operations: 1", "total: 1h 0m across 1 operation(s)", "next step: timesheet approve --plan "} {
		if !strings.Contains(out, want) {
			t.Errorf("guided stdout missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "timesheet submit") {
		t.Errorf("the guided run must NOT instruct a submission:\n%s", out)
	}
}

func TestGuided_PlainEOFAborts(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})

	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, guidedTestConfig())
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	before := loadRecord(t, storage, testDate)

	var stdout, stderr bytes.Buffer
	// A blank stdin aborts at the FIRST prompt.
	code := runCLIWithIO(context.Background(),
		[]string{"run", "--plain", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--no-editorial"},
		strings.NewReader(""), &stdout, &stderr, false, false)
	if code == exitOK {
		t.Fatalf("an EOF must abort, got ok\n%s", stdout.String())
	}
	after := loadRecord(t, storage, testDate)
	if len(after.Confirmed) != len(before.Confirmed) {
		t.Fatalf("an abort must not confirm work:\n before=%+v\n after=%+v", before.Confirmed, after.Confirmed)
	}
	if after.Review != nil && after.Review.Reviewed {
		t.Fatalf("an abort must not mark the day reviewed, got %+v", after.Review)
	}
	if len(after.Approvals) != 0 {
		t.Fatalf("an abort must not create an approval, got %+v", after.Approvals)
	}
}

// --- guided vs scripted parity ----------------------------------------------

func TestGuided_ParityWithScriptedReview(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "Gradion Intern Academy 2026", ActivityType: "internal project"})
	cfg := guidedTestConfig()

	// Guided.
	gd := t.TempDir()
	gStorage := filepath.Join(gd, "records")
	seedRecord(t, gStorage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	gr, gstore := newGuidedRunner(t, gStorage, nil, cfg)
	var out, errb bytes.Buffer
	p := &fakePrompter{
		choose:   []int{2, 0, 0},
		inputs:   []string{"16:00", "17:00", "Proj", "did work"},
		confirms: []bool{false, true},
	}
	if code := cmdRunGuided(context.Background(), gr, cfg, gstore, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	guidedRec := loadRecord(t, gStorage, testDate)

	// Scripted equivalent: the SAME block decisions applied through the core.
	sd := t.TempDir()
	sStorage := filepath.Join(sd, "records")
	seedRecord(t, sStorage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	sr, _ := newGuidedRunner(t, sStorage, nil, cfg)
	prop, err := sr.Propose(context.Background(), testDate)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(prop.Work) != 1 {
		t.Fatalf("want 1 work block, got %+v", prop.Work)
	}
	blockID := prop.Work[0].ID
	decisions := run.ReviewDecisions{
		Confirm:        []run.ConfirmDecision{{BlockID: blockID, Project: "Proj"}},
		Trim:           []run.TrimDecision{{BlockID: blockID, NewStart: "16:00", NewEnd: "17:00"}},
		Description:    []run.DescriptionDecision{{BlockID: blockID, Text: "did work"}},
		Classification: []run.ClassificationDecision{{BlockID: blockID, ClassificationID: "cls-1", ActivityType: "internal project"}},
	}
	if err := sr.ApplyReview(context.Background(), testDate, decisions); err != nil {
		t.Fatalf("scripted apply: %v", err)
	}
	scriptedRec := loadRecord(t, sStorage, testDate)

	if len(guidedRec.Confirmed) != len(scriptedRec.Confirmed) {
		t.Fatalf("confirmed counts differ: guided=%+v scripted=%+v", guidedRec.Confirmed, scriptedRec.Confirmed)
	}
	for i := range guidedRec.Confirmed {
		g, s := guidedRec.Confirmed[i], scriptedRec.Confirmed[i]
		if g.ID != s.ID || g.Project != s.Project || g.Description != s.Description || g.ClassificationID != s.ClassificationID || g.ActivityType != s.ActivityType {
			t.Fatalf("guided and scripted reviews differ:\n guided=%+v\n scripted=%+v", g, s)
		}
	}
}

// REVIEW-UNDO-1: an interactive "Remove from this day" choice produces the SAME
// record as a scripted unconfirm.
func TestGuided_UnconfirmParityWithScripted(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()

	// Guided: existing work c1 -> "Remove from this day" (index 2).
	gd := t.TempDir()
	gStorage := filepath.Join(gd, "records")
	seedRecord(t, gStorage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	gr, gstore := newGuidedRunner(t, gStorage, nil, cfg)
	var out, errb bytes.Buffer
	// no reset (false); remove c1 (2); no bulk (0); no missing work (false); confirm (true).
	p := &fakePrompter{choose: []int{2, 0}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), gr, cfg, gstore, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	guidedRec := loadRecord(t, gStorage, testDate)
	if len(guidedRec.Confirmed) != 0 {
		t.Fatalf("the interactive remove must remove the interval, got %+v", guidedRec.Confirmed)
	}

	// Scripted equivalent: the same removal applied through the core.
	sd := t.TempDir()
	sStorage := filepath.Join(sd, "records")
	seedRecord(t, sStorage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	sr, _ := newGuidedRunner(t, sStorage, nil, cfg)
	if err := sr.ApplyReview(context.Background(), testDate, run.ReviewDecisions{
		Unconfirm: []run.UnconfirmDecision{{BlockID: "c1", Reason: "user_retracted"}},
		MarkEmpty: true,
	}); err != nil {
		t.Fatalf("scripted apply: %v", err)
	}
	scriptedRec := loadRecord(t, sStorage, testDate)

	if len(guidedRec.Confirmed) != len(scriptedRec.Confirmed) {
		t.Fatalf("confirmed counts differ: guided=%+v scripted=%+v", guidedRec.Confirmed, scriptedRec.Confirmed)
	}
	if guidedRec.Review == nil || scriptedRec.Review == nil ||
		guidedRec.Review.Empty != scriptedRec.Review.Empty ||
		guidedRec.Review.Reviewed != scriptedRec.Review.Reviewed {
		t.Fatalf("review state differs:\n guided=%+v\n scripted=%+v", guidedRec.Review, scriptedRec.Review)
	}
}

// REVIEW-UNDO-1: a MAPPED (submitted) interval is presented as non-removable: the
// option teaches the edit-instead rule and stages nothing, so the interval stays.
func TestGuided_MappedIntervalNotRemovable(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.RemoteMappings = []workflow.RemoteMapping{{LocalID: "c1", RemoteID: "remote-1", Date: testDate}}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// no reset (false); the mapped interval's actionable choice is the teach-the-
	// rule option (index 2, stages nothing); no bulk; no missing work; confirm.
	p := &fakePrompter{choose: []int{2, 0}, confirms: []bool{false, false, true}}
	// The preview may BLOCK on the unmatched mapping (reconcile:*); the review is
	// still applied, which is what this test inspects.
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK && code != exitBlocked {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	foundSubmit, foundRemove := false, false
	for _, opts := range p.chooseOptions {
		for _, o := range opts {
			if strings.Contains(o.Label, "edit the remote entry instead") {
				foundSubmit = true
			}
			if strings.Contains(o.Label, "Remove from this day") {
				foundRemove = true
			}
		}
	}
	if !foundSubmit || foundRemove {
		t.Fatalf("a mapped interval must offer the edit-instead rule and no removal, got submit=%v remove=%v", foundSubmit, foundRemove)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || rec.Confirmed[0].ID != "c1" {
		t.Fatalf("a submitted interval must NOT be removed, got %+v", rec.Confirmed)
	}
}

// REVIEW-UNDO-1 (F2 guided): a fragment whose split-group SIBLING is mapped is
// presented as non-removable too, because the group is removed as a whole. The
// guided path must not offer a removal that apply would refuse.
func TestGuided_MappedSiblingMakesFragmentNonRemovable(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 0},
			{ID: "c2", Date: testDate, StartTime: "10:00", EndTime: "11:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 1},
		}
		rec.Review = &workflow.ReviewState{Reviewed: true}
		rec.RemoteMappings = []workflow.RemoteMapping{{LocalID: "c2", RemoteID: "remote-1", Date: testDate}}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// For BOTH fragments the only actionable choice (index 2) is the teach-the-rule
	// option, which stages nothing; no bulk; no missing work; confirm.
	p := &fakePrompter{choose: []int{2, 2, 0}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK && code != exitBlocked {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	foundSubmit, foundRemove := false, false
	for _, opts := range p.chooseOptions {
		for _, o := range opts {
			if strings.Contains(o.Label, "edit the remote entry instead") {
				foundSubmit = true
			}
			if strings.Contains(o.Label, "Remove from this day") {
				foundRemove = true
			}
		}
	}
	if !foundSubmit || foundRemove {
		t.Fatalf("a fragment with a mapped sibling must offer the edit-instead rule and no removal, got submit=%v remove=%v", foundSubmit, foundRemove)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 2 {
		t.Fatalf("no fragment of a group with a mapped sibling may be removed, got %+v", rec.Confirmed)
	}
}

// REVIEW-UNDO-1 (F3): removing BOTH fragments of one split group in a single
// guided pass stages exactly ONE removal (the group is removed as a whole), so
// apply never fails with unconfirm_unknown_interval. Because that removal empties
// the day, the day is recorded honestly as reviewed-empty.
func TestGuided_SplitGroupRemoveBothFragmentsStagesOnce(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 0},
			{ID: "c2", Date: testDate, StartTime: "10:00", EndTime: "11:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 1},
		}
		rec.Review = &workflow.ReviewState{Reviewed: true}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// no reset (false); remove c1 (2); c2 offers ONLY "Keep as is" (0) and
	// "Removed with the rest of this split group" (1, stages nothing); no bulk
	// (0); no missing work (false); confirm (true).
	p := &fakePrompter{choose: []int{2, 1, 0}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d (an unconfirm_unknown_interval means two ids of one group were staged)\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 0 {
		t.Fatalf("the whole split group must be removed, got %+v", rec.Confirmed)
	}
	if rec.Review == nil || !rec.Review.Reviewed || !rec.Review.Empty {
		t.Fatalf("a day emptied by removals must be recorded reviewed-empty, got %+v", rec.Review)
	}
}

// GAP-5-STAGING-GUARD order 1 (remove, then edit): once one fragment of a split
// group is staged for removal, the remaining fragments of THAT group may be kept
// or noted as "removed with the rest", but the EDIT option must NOT be offered.
// Offering it lets the guided run stage an edit on a fragment the group removal
// deletes, which apply rejects with "confirmed block not found".
func TestGuided_SplitGroupAlreadyStagedOffersNoEdit(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 0},
			{ID: "c2", Date: testDate, StartTime: "10:00", EndTime: "11:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 1},
		}
		rec.Review = &workflow.ReviewState{Reviewed: true}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// no reset (false); remove c1 (2); c2 -> "Removed with the rest" (1, no-op);
	// no bulk (0); no missing work (false); confirm (true).
	p := &fakePrompter{choose: []int{2, 1, 0}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d (a refused apply means an edit reached a removed fragment)\n%s\n%s", code, out.String(), errb.String())
	}
	if len(p.chooseOptions) < 2 {
		t.Fatalf("want a Choose for each fragment, got %d", len(p.chooseOptions))
	}
	second := p.chooseOptions[1]
	if len(second) != 2 {
		t.Fatalf("a fragment whose group is already staged must offer exactly Keep + group-removal, got %v", promptLabels(second))
	}
	if second[0].Label != "Keep as is" || second[1].Label != "Removed with the rest of this split group" {
		t.Fatalf("unexpected option labels: %v", promptLabels(second))
	}
	for _, o := range second {
		if strings.Contains(o.Label, "Edit") {
			t.Fatalf("no edit option may be offered for a fragment whose group is already staged: %v", promptLabels(second))
		}
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 0 {
		t.Fatalf("the whole split group must be removed, got %+v", rec.Confirmed)
	}
}

// GAP-5-STAGING-GUARD order 2 (edit, then remove): staging a removal for a
// fragment whose split group already carries a staged edit must be REFUSED with a
// stable reason naming the edited fragment, must stage nothing, and must keep the
// staged edit, so the resulting set is one apply accepts.
func TestGuided_SplitGroupEditedThenRemoveRefused(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 0},
			{ID: "c2", Date: testDate, StartTime: "10:00", EndTime: "11:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 1},
		}
		rec.Review = &workflow.ReviewState{Reviewed: true}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// no reset (false); edit c1 (1) changing its DESCRIPTION (no span overlap);
	// remove c2 (2, REFUSED); no bulk (0); no missing work (false); confirm (true).
	p := &fakePrompter{choose: []int{1, 2, 0}, inputs: []string{"", "", "edited c1"}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d (want the refused removal kept out of apply)\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "unconfirm_group_edited:c1") {
		t.Fatalf("the refused removal must print the stable reason naming the edited fragment c1:\n%s", out.String())
	}
	if strings.Contains(out.String()+"\n"+errb.String(), "confirmed block not found") {
		t.Fatalf("the invalid set must never reach apply:\n%s\n%s", out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 2 {
		t.Fatalf("the group must survive a refused removal, got %+v", rec.Confirmed)
	}
	// The staged edit is NOT silently discarded.
	found := false
	for _, ci := range rec.Confirmed {
		if ci.ID == "c1" && ci.Description == "edited c1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the staged edit on c1 must survive the refused removal, got %+v", rec.Confirmed)
	}
}

// GAP-5-STAGING-GUARD: the new guard is scoped to the SAME split group. A removal
// of an interval in a DIFFERENT group, or with NO group, is unaffected by an edit
// staged on an unrelated fragment.
func TestGuided_EditDoesNotBlockSeparateRemoval(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 0},
			{ID: "c2", Date: testDate, StartTime: "10:00", EndTime: "11:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g2", PartIndex: 0},
			{ID: "c3", Date: testDate, StartTime: "11:00", EndTime: "12:00", Project: "Proj", ClassificationID: "cls-1"},
		}
		rec.Review = &workflow.ReviewState{Reviewed: true}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// no reset (false); edit c1 (1); remove c2 in a DIFFERENT group (2); remove c3
	// with NO group (2); no bulk (0); no missing work (false); confirm (true).
	p := &fakePrompter{choose: []int{1, 2, 2, 0}, inputs: []string{"09:30", "10:30", "edited c1"}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d (a separate-group/no-group removal must be unaffected)\n%s\n%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "unconfirm_group_edited") {
		t.Fatalf("the guard must not fire for a different group or a group-less interval:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || rec.Confirmed[0].ID != "c1" {
		t.Fatalf("c2 and c3 must be removed and c1 kept, got %+v", rec.Confirmed)
	}
	if rec.Confirmed[0].StartTime != "09:30" {
		t.Fatalf("the staged edit on c1 must be applied, got %+v", rec.Confirmed[0])
	}
}

// GAP-5-STAGING-GUARD: an edit that changed NOTHING stages no decision, so it must
// NOT falsely block a legitimate removal of the whole split group.
func TestGuided_NoOpEditDoesNotBlockGroupRemoval(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 0},
			{ID: "c2", Date: testDate, StartTime: "10:00", EndTime: "11:00", Project: "Proj", ClassificationID: "cls-1", SplitGroupID: "g1", PartIndex: 1},
		}
		rec.Review = &workflow.ReviewState{Reviewed: true}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// no reset (false); c1 -> edit (1) but blanks keep every value (no-op); remove
	// c2 (2, allowed); no bulk (0); no missing work (false); confirm (true).
	p := &fakePrompter{choose: []int{1, 2, 0}, inputs: []string{"", "", ""}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "unconfirm_group_edited") {
		t.Fatalf("a no-op edit must not block a legitimate group removal:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 0 {
		t.Fatalf("the whole split group must be removed, got %+v", rec.Confirmed)
	}
	if rec.Review == nil || !rec.Review.Reviewed || !rec.Review.Empty {
		t.Fatalf("a day emptied by removals must be recorded reviewed-empty, got %+v", rec.Review)
	}
}

// promptLabels renders an option list's labels for test failures.
func promptLabels(opts []promptOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Label)
	}
	return out
}

// --- abort on a TUI error ---------------------------------------------------

// tuiErrorPrompter returns errAborted from the first prompt, simulating a huh
// form error / ctrl-c.
type tuiErrorPrompter struct{}

func (tuiErrorPrompter) Choose(context.Context, string, []promptOption, int) (int, error) {
	return 0, errAborted
}
func (tuiErrorPrompter) MultiChoose(context.Context, string, []promptOption) ([]int, error) {
	return nil, errAborted
}
func (tuiErrorPrompter) Input(context.Context, string, string, string) (string, error) {
	return "", errAborted
}
func (tuiErrorPrompter) Confirm(context.Context, string, bool) (bool, error) {
	return false, errAborted
}

func TestGuided_TUIErrorAborts(t *testing.T) {
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	cfg := guidedTestConfig()
	r, store := newGuidedRunner(t, storage, nil, cfg)
	var out, errb bytes.Buffer
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, tuiErrorPrompter{}, &out, &errb)
	if code == exitOK {
		t.Fatalf("a TUI error must abort, got ok")
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review != nil && rec.Review.Reviewed {
		t.Fatalf("a TUI error must not mark the day reviewed")
	}
}

// --- empty days -------------------------------------------------------------

func TestGuided_EmptyDayReviewedEmpty(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	vault := filepath.Join(dir, "vault")
	cfg := guidedTestConfig()
	cfg.Vault = vault
	cfg.DailyDir = "Daily"
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	p := &fakePrompter{choose: []int{0}, confirms: []bool{false, true}} // no bulk, no missing work, confirm review
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("empty guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review == nil || !rec.Review.Reviewed || !rec.Review.Empty {
		t.Fatalf("want a reviewed-empty day, got %+v", rec.Review)
	}
	if len(rec.Confirmed) != 0 {
		t.Fatalf("an empty day must invent no entries, got %+v", rec.Confirmed)
	}
}

func TestGuided_EmptyRefreshPreservesConfirmed(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate) // one confirmed interval, reviewed
	})
	before := loadRecord(t, storage, testDate)

	r, store := newGuidedRunner(t, storage, nil, cfg)
	var out, errb bytes.Buffer
	// Existing interval is "kept" (index 0); no reset; no bulk actions; no missing work; confirm review.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	after := loadRecord(t, storage, testDate)
	if len(after.Confirmed) != 1 || after.Confirmed[0].ID != before.Confirmed[0].ID || after.Confirmed[0].Description != before.Confirmed[0].Description {
		t.Fatalf("existing confirmed work must be preserved:\n before=%+v\n after=%+v", before.Confirmed, after.Confirmed)
	}
	if after.Review != nil && after.Review.Empty {
		t.Fatalf("a day with existing confirmed work must never be marked empty")
	}
}

// --- incomplete sources -----------------------------------------------------

func incompleteCollection() *evidence.CollectionSummary {
	return &evidence.CollectionSummary{
		Date:     testDate,
		Complete: false,
		Sources:  []evidence.SourceResult{{SourceID: "cal-work", Type: evidence.SourceCalendar, Status: evidence.StatusIncomplete, ErrorCode: "SOURCE_ERROR"}},
	}
}

func TestGuided_IncompleteSourceAckUnblocks(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Collection = incompleteCollection()
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// acknowledge incomplete (true), no reset (false), no missing work (false), no bulk, confirm review.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{true, false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review == nil || len(rec.Review.AcknowledgedSources) != 1 || rec.Review.AcknowledgedSources[0] != "cal-work" {
		t.Fatalf("want cal-work acknowledged, got %+v", rec.Review)
	}
	if strings.Contains(out.String(), "incomplete_collection") {
		t.Fatalf("an acknowledged incomplete source must not block the preview:\n%s", out.String())
	}
}

func TestGuided_IncompleteSourceNotAckedBlocks(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Collection = incompleteCollection()
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// do NOT acknowledge (false), no reset, no missing work, no bulk, confirm review.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{false, false, false, true}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code != exitBlocked {
		t.Fatalf("want a blocked preview, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "incomplete_collection") {
		t.Fatalf("want incomplete_collection in the block reasons:\n%s", out.String())
	}
}

// --- classification authority + suggestions ---------------------------------

func TestGuided_ClassificationSuggestionNeverApplied(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig() // config map suggests Proj -> cls-1
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// confirm the block (0), no missing work, no bulk (0), LEAVE classification unset (last option),
	// confirm the review.
	p := &fakePrompter{choose: []int{2, 0, 3}, inputs: []string{"16:00", "17:00", "Proj", "work"}, confirms: []bool{false, true}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	// The plan blocks on the unresolved classification, but the review IS applied.
	if code != exitBlocked {
		t.Fatalf("want a blocked preview for an unset classification, got %d\n%s", code, out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want 1 confirmed interval, got %+v", rec.Confirmed)
	}
	if rec.Confirmed[0].ClassificationID != "" {
		t.Fatalf("a suggestion must NEVER be auto-applied, got %q", rec.Confirmed[0].ClassificationID)
	}
	if !strings.Contains(out.String(), "suggestion (unapplied)") {
		t.Fatalf("want the suggestion shown as unapplied:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "unresolved_classification") {
		t.Fatalf("want the preview to block on unresolved_classification:\n%s", out.String())
	}
}

// P1: with NO recent selection, the guided step surfaces the owner's first-run
// suggestion (resolved against the fetched catalog) with its DERIVED activity
// type, clearly UNAPPLIED.
func TestGuided_FirstRunSuggestionUnappliedWhenNoRecent(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	cat := gradion.Catalog{
		Complete: true, Source: "live", ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{{
			ID: "cls-1", Code: "INTGRADI2610", Name: "Gradion Intern Academy 2026", ActivityType: "internal project",
		}},
	}
	var out bytes.Buffer
	g := &guided{cfg: cliConfig{}, stdout: &out, stderr: &bytes.Buffer{}, cat: &cat}
	g.printUnappliedSuggestions(nil)
	s := out.String()
	for _, want := range []string{"suggestion (unapplied)", "INTGRADI2610", "internal project"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the first-run suggestion must be surfaced unapplied (%q missing):\n%s", want, s)
		}
	}
	// Never applied or persisted.
	p, _ := selection.DefaultPath()
	prefs, err := selection.Load(p)
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	if _, ok := prefs.MostRecent(); ok {
		t.Fatalf("a suggestion must never be persisted: %+v", prefs)
	}
}

// P1: a guided run with no recent selection SHOWS the first-run suggestion but
// writes no ClassificationID without an explicit pick.
func TestGuided_FirstRunSuggestionShownButNotApplied(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{
		ID: "cls-1", Code: "INTGRADI2610", Name: "Gradion Intern Academy 2026", ActivityType: "internal project",
	})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	cfg.Classifications = nil // isolate the FIRST-RUN suggestion from the config map
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// edit+confirm the block (2), no missing work, no bulk (0), LEAVE classification
	// unset (3), confirm the review (true).
	p := &fakePrompter{choose: []int{2, 0, 3}, inputs: []string{"16:00", "17:00", "Proj", "work"}, confirms: []bool{false, true}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code != exitBlocked {
		t.Fatalf("want a blocked preview for an unset classification, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "suggestion (unapplied): first-run") ||
		!strings.Contains(out.String(), "INTGRADI2610") ||
		!strings.Contains(out.String(), "internal project") {
		t.Fatalf("the first-run suggestion must be shown as unapplied with its derived type:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || rec.Confirmed[0].ClassificationID != "" {
		t.Fatalf("a suggestion must NEVER be auto-applied, got %+v", rec.Confirmed)
	}
	// A printed suggestion must never create a decision or persist a recent.
	prefsPath, _ := selection.DefaultPath()
	prefs, err := selection.Load(prefsPath)
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	if _, ok := prefs.MostRecent(); ok {
		t.Fatalf("a printed suggestion must not persist a recent: %+v", prefs)
	}
}

// P2: the guided step surfaces up to maxRecentHints recent selections, each
// UNAPPLIED, when recents exist (precedence over the first-run suggestion).
func TestGuided_SurfacesUpToThreeRecentUnapplied(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	cat := gradion.Catalog{
		Complete: true, Source: "live", ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{{
			ID: "cls-1", Code: "INTGRADI2610", Name: "Gradion Intern Academy 2026", ActivityType: "internal project",
		}},
	}
	p, _ := selection.DefaultPath()
	prefs := selection.Prefs{}
	for i := 0; i < 4; i++ {
		prefs.Record(selection.RecentSelection{
			ClassificationID: fmt.Sprintf("uuid-%d", i),
			Code:             fmt.Sprintf("CODE-%d", i),
			Name:             fmt.Sprintf("Recent %d", i),
			ActivityType:     "internal project",
		})
	}
	if err := selection.Save(p, prefs); err != nil {
		t.Fatalf("save prefs: %v", err)
	}
	var out bytes.Buffer
	g := &guided{cfg: cliConfig{}, stdout: &out, stderr: &bytes.Buffer{}, cat: &cat}
	g.printUnappliedSuggestions(nil)
	s := out.String()
	for _, want := range []string{"Recent 3", "Recent 2", "Recent 1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("want recent %q surfaced unapplied:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Recent 0") {
		t.Fatalf("only %d recents may be surfaced:\n%s", maxRecentHints, s)
	}
	// A recent exists, so the first-run suggestion must NOT also be shown.
	if strings.Contains(s, "first-run") {
		t.Fatalf("a recent selection takes precedence over the first-run suggestion:\n%s", s)
	}
}

// P3: a search that returns an option BEYOND the unfiltered first page makes it
// selectable, and the choice still re-resolves through AuthoritativeClassificationOption
// (the guided search unions the retrieved option into the validation catalog). No
// absence is asserted.
func TestGuided_ClassificationSearchBeyondPageSelectable(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	fetch := &fakeCatalogFetcher{
		catalog: partialPage(),
		byQuery: map[string]gradion.Catalog{"intern": targetedIntern()},
	}
	withCatalogFetcher(t, fetch)

	var out bytes.Buffer
	// base page (12 shown) -> "Search the catalog" (12), search "intern", then the
	// single searched option (0).
	g := &guided{
		cfg: cliConfig{}, stdout: &out, stderr: &bytes.Buffer{},
		p: &fakePrompter{choose: []int{12, 0}, inputs: []string{"intern"}},
	}
	base := partialPage()
	g.cat = &base
	opt, ok, err := g.chooseClassification(context.Background(), classTarget{label: "x"}, base)
	if err != nil {
		t.Fatalf("chooseClassification: %v", err)
	}
	if !ok || opt.ID != "uuid-int" {
		t.Fatalf("the searched out-of-page option must be selectable, got ok=%v opt=%+v", ok, opt)
	}
	if strings.Contains(out.String(), "was not found") {
		t.Fatalf("a successful search must not assert absence:\n%s", out.String())
	}
	// The retrieved choice must re-resolve and capture the DERIVED activity type.
	dec := run.ReviewDecisions{Classification: []run.ClassificationDecision{{BlockID: "c1", ClassificationID: opt.ID}}}
	if err := g.validateClassifications(&dec); err != nil {
		t.Fatalf("the searched choice must re-resolve via AuthoritativeClassificationOption: %v", err)
	}
	if dec.Classification[0].ActivityType != "internal project" {
		t.Fatalf("the derived activity type must be captured, got %q", dec.Classification[0].ActivityType)
	}
}

// P3: a search with NO match prints a partial/no-match caveat that offers search and
// never asserts absence.
func TestGuided_ClassificationSearchNoMatchCaveat(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	fetch := &fakeCatalogFetcher{
		catalog: partialPage(),
		byQuery: map[string]gradion.Catalog{"zzz": {Complete: true, Source: "live", ObservedAt: time.Now().UTC()}},
	}
	withCatalogFetcher(t, fetch)

	var out bytes.Buffer
	// base page (12 shown) -> "Search the catalog" (12), search "zzz" (no match),
	// then "Leave unset" (14) on the re-prompt.
	g := &guided{
		cfg: cliConfig{}, stdout: &out, stderr: &bytes.Buffer{},
		p: &fakePrompter{choose: []int{12, 14}, inputs: []string{"zzz"}},
	}
	base := partialPage()
	g.cat = &base
	_, ok, err := g.chooseClassification(context.Background(), classTarget{label: "x"}, base)
	if err != nil {
		t.Fatalf("chooseClassification: %v", err)
	}
	if ok {
		t.Fatalf("a no-match search must not silently select anything")
	}
	s := out.String()
	for _, want := range []string{"PARTIAL", "20", "--search", "may lie beyond"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the no-match caveat must be partial-aware and offer search (%q missing):\n%s", want, s)
		}
	}
}

// --- journal narratives + manual linkage ------------------------------------

func journalEvidence() evidence.Evidence {
	start := time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)
	return evidence.Evidence{
		ID:          "j1",
		Kind:        evidence.SourceJournal,
		Title:       "Shipped the parser fix",
		Description: "Shipped the parser fix",
		SourceRefs: []evidence.SourceRef{{
			Kind: evidence.SourceJournal, SourceID: "journal-work",
			NotePath: "02 - Daily & Logs/2026-10-06.md", HeadingPath: "Work / Gradion", Section: "Work",
			LineStart: 12, LineEnd: 14, StatedDate: "2026-10-06",
		}},
		ObservedAt: start,
	}
}

func TestGuided_JournalNarrativeLinkedToManualInterval(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, journalEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// narrative: skip (0); missing work: yes; manual interval inputs; link the journal (multi [0]);
	// add another? no; classification: option 1; confirm review.
	p := &fakePrompter{
		choose:   []int{0, 0, 0}, // narrative skip, no bulk, classification option 0
		inputs:   []string{"09:00", "10:00", "Proj", "did journal work"},
		multi:    [][]int{{0}},
		confirms: []bool{true, false, true}, // missing=yes, add-another=no, final=yes
	}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}

	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want 1 confirmed manual interval, got %+v", rec.Confirmed)
	}
	ci := rec.Confirmed[0]
	if !ci.Manual {
		t.Fatalf("want a manual confirmed interval, got %+v", ci)
	}
	if len(ci.EvidenceIDs) != 2 {
		t.Fatalf("want the manual id + the linked journal id, got %v", ci.EvidenceIDs)
	}
	if !containsString(ci.EvidenceIDs, "j1") {
		t.Fatalf("want the journal id linked, got %v", ci.EvidenceIDs)
	}
	// Exactly one manual evidence item, and its own duration is authoritative
	// (60 minutes), not doubled by the linked journal item.
	manuals := 0
	for _, e := range rec.CollectedEvidence {
		if e.Kind == evidence.SourceManual {
			manuals++
			if !containsString(e.LinkedEvidenceIDs, "j1") {
				t.Fatalf("want the manual evidence to carry the link, got %+v", e.LinkedEvidenceIDs)
			}
		}
	}
	if manuals != 1 {
		t.Fatalf("want exactly 1 manual evidence item, got %d", manuals)
	}
	if ci.StartTime != "09:00" || ci.EndTime != "10:00" {
		t.Fatalf("want the user-timed span preserved, got %s-%s", ci.StartTime, ci.EndTime)
	}
	if !strings.Contains(out.String(), "duration=unknown") {
		t.Fatalf("the narrative must be shown with an explicit unknown duration:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "stated=2026-10-06") {
		t.Fatalf("the note's OWN stated date must be shown as quoted provenance:\n%s", out.String())
	}
}

func TestGuided_MissingWorkQuestionAskedOnce(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	p := &fakePrompter{choose: []int{2, 0, 0}, inputs: []string{"16:00", "17:00", "Proj", "work"}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	n := 0
	for _, title := range p.confirmTitles {
		if strings.Contains(title, "work missing") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the missing-work question must be asked exactly once, got %d (%v)", n, p.confirmTitles)
	}
}

// --- zero remote writes + no hidden credential retrieval --------------------

func TestGuided_ZeroRemoteWrites(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	remote := &submitFakeRemote{}
	r, store := newGuidedRunner(t, storage, remote, cfg)

	var out, errb bytes.Buffer
	p := &fakePrompter{choose: []int{2, 0, 0}, inputs: []string{"16:00", "17:00", "Proj", "work"}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if remote.writes != 0 {
		t.Fatalf("the guided run must make ZERO remote writes, got %d", remote.writes)
	}
}

// recordingResolver fails on every lookup and records the services asked for.
type recordingResolver struct{ calls []string }

func (r *recordingResolver) Lookup(_ context.Context, service, _ string) (string, error) {
	r.calls = append(r.calls, service)
	return "", &keychain.OperatingError{Outcome: keychain.OutcomeNotFound, Code: "KEYCHAIN_ERR_NOT_FOUND", Message: "absent"}
}

func TestGuided_NoHiddenCredentialRetrieval(t *testing.T) {
	rec := &recordingResolver{}
	restore := keychain.SetDefaultResolver(rec)
	t.Cleanup(restore)
	// A resolved git token lets preflight pass WITHOUT any Keychain lookup.
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("TIMESHEET_HOME", t.TempDir())

	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig() // no sources, no remote, no keychain refs
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	p := &fakePrompter{choose: []int{0}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("a no-source guided run must not touch the Keychain: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if len(rec.calls) != 0 {
		t.Fatalf("the guided run must not resolve any credential, got lookups %v", rec.calls)
	}
}

// --- collision join: counts once --------------------------------------------

func manualEvidenceAt(id, project string, start, end time.Time) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceManual, Title: "manual", Description: "manual work",
		Start: start, End: end, Duration: evidence.KnownDuration(int(end.Sub(start).Minutes())),
		SourceRefs:   []evidence.SourceRef{{Kind: evidence.SourceManual}},
		ProjectHints: []string{project}, ObservedAt: start,
	}
}

func meetingEvidenceAt(id string, start, end time.Time) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceCalendar, Title: "Meeting", Start: start, End: end,
		Status: evidence.EventStatusConfirmed, Invitation: evidence.InvitationAccepted,
		Duration: evidence.KnownDuration(int(end.Sub(start).Minutes())), ObservedAt: start,
	}
}

func TestGuided_CollisionJoinCountsOnce(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence,
			manualEvidenceAt("m1", "Proj", time.Date(2026, 10, 6, 9, 0, 0, 0, loc), time.Date(2026, 10, 6, 10, 0, 0, 0, loc)),
			meetingEvidenceAt("cal1", time.Date(2026, 10, 6, 9, 30, 0, 0, loc), time.Date(2026, 10, 6, 10, 30, 0, 0, loc)),
		)
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// manual block confirm (0), meeting join (4), no bulk (0), classification (0);
	// no missing work, confirm review.
	p := &fakePrompter{choose: []int{0, 4, 0, 0}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("a join must count ONCE, got %d intervals: %+v", len(rec.Confirmed), rec.Confirmed)
	}
	ci := rec.Confirmed[0]
	if ci.Kind != "joint" {
		t.Fatalf("want a joint interval, got %+v", ci)
	}
	if !containsString(ci.EvidenceIDs, "m1") || !containsString(ci.EvidenceIDs, "cal1") {
		t.Fatalf("the joint must carry both evidence ids, got %v", ci.EvidenceIDs)
	}
	if ci.StartTime != "09:00" || ci.EndTime != "10:30" {
		t.Fatalf("want the union span 09:00-10:30, got %s-%s", ci.StartTime, ci.EndTime)
	}
	if ci.ClassificationID != "cls-1" {
		t.Fatalf("the joint must inherit the classification, got %q", ci.ClassificationID)
	}
}

// --- cancel after choices preserves prior state -----------------------------

func TestGuided_CancelAfterChoicesPreservesPriorState(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Review.AcknowledgedSources = []string{"cal"}
		rec.Approvals = []workflow.Approval{{ApprovalID: "a1", PlanID: "p1", Date: testDate}}
	})
	before := loadRecord(t, storage, testDate)

	r, store := newGuidedRunner(t, storage, nil, cfg)
	var out, errb bytes.Buffer
	// keep existing (0); no reset; no bulk (0); no missing work; then CANCEL at the final confirmation.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{false, false, false}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code == exitOK {
		t.Fatalf("a cancelled final confirmation must not succeed")
	}
	after := loadRecord(t, storage, testDate)
	if len(after.Confirmed) != len(before.Confirmed) || after.Confirmed[0].Description != before.Confirmed[0].Description {
		t.Fatalf("prior confirmed work must be preserved:\n before=%+v\n after=%+v", before.Confirmed, after.Confirmed)
	}
	if len(after.Approvals) != 1 || after.Approvals[0].InvalidatedAt != nil || after.Approvals[0].Superseded {
		t.Fatalf("a prior approval must be preserved untouched, got %+v", after.Approvals)
	}
	if after.Review == nil || len(after.Review.AcknowledgedSources) != 1 || after.Review.AcknowledgedSources[0] != "cal" {
		t.Fatalf("prior review selections must be preserved, got %+v", after.Review)
	}
}

// --- plain cancellation never accepts a default -----------------------------

func TestPlainPrompter_CancelledContextAborts(t *testing.T) {
	var out bytes.Buffer
	p := newPlainPrompter(strings.NewReader("1\ny\n"), &out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Choose(ctx, "t", []promptOption{{Label: "a"}}, 0); !errors.Is(err, errAborted) {
		t.Fatalf("a cancelled Choose must abort, got %v", err)
	}
	if _, err := p.Confirm(ctx, "t", true); !errors.Is(err, errAborted) {
		t.Fatalf("a cancelled Confirm must abort (never the default), got %v", err)
	}
	if _, err := p.Input(ctx, "t", "", "default"); !errors.Is(err, errAborted) {
		t.Fatalf("a cancelled Input must abort (never the default), got %v", err)
	}
	if _, err := p.MultiChoose(ctx, "t", []promptOption{{Label: "a"}}); !errors.Is(err, errAborted) {
		t.Fatalf("a cancelled MultiChoose must abort, got %v", err)
	}
}

func TestPlainPrompter_EOFFromEmptyReaderAborts(t *testing.T) {
	var out bytes.Buffer
	p := newPlainPrompter(strings.NewReader(""), &out)
	if _, err := p.Choose(context.Background(), "t", []promptOption{{Label: "a"}}, 0); !errors.Is(err, errAborted) {
		t.Fatalf("an EOF must abort, got %v", err)
	}
}

// --- retained existing classification is explicit, not silent ---------------

func TestGuided_RetainedClassificationFollowup(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "keep their existing classification") || !strings.Contains(out.String(), "classify select") {
		t.Fatalf("a retained classification must be disclosed with an actionable followup:\n%s", out.String())
	}
}

// TestGuidedOfflineHarness is the named, hermetic guided-run harness. It drives
// the REAL orchestration offline (no credential, no remote, no live service) with
// a sanitized journal note plus local git evidence, and asserts the persisted
// record. Run it with:
//
//	go test ./cmd/timesheet -run TestGuidedOfflineHarness -v
func TestGuidedOfflineHarness(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", Name: "Intern Academy", ActivityType: "internal project"})

	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig() // Asia/Ho_Chi_Minh, no sources, no remote
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, journalEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// journal: skip (0); missing work: yes; manual 09:00-10:00 linked to the note;
	// add another: no; classification: option 0; confirm review.
	p := &fakePrompter{
		choose:   []int{0, 0, 0},
		inputs:   []string{"09:00", "10:00", "Proj", "reviewed the parser fix"},
		multi:    [][]int{{0}},
		confirms: []bool{true, false, true},
	}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("offline harness: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("harness: want 1 confirmed interval, got %+v", rec.Confirmed)
	}
	if rec.Confirmed[0].ClassificationID != "cls-1" {
		t.Fatalf("harness: want cls-1, got %q", rec.Confirmed[0].ClassificationID)
	}
	if len(rec.Approvals) != 0 {
		t.Fatalf("harness: the guided run must not approve")
	}
	t.Logf("offline guided harness OK: %d interval(s), total %s-%s, plan %s",
		len(rec.Confirmed), rec.Confirmed[0].StartTime, rec.Confirmed[0].EndTime, valueAfter(out.String(), "plan_id:"))
}

// --- existing confirmed work is editable via staged actions -----------------

func TestGuided_ExistingConfirmedEditable(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// edit the existing interval (1): keep times/project, set a new description.
	// The interval's project ("Proj") is already set, so the project is NOT
	// prompted; the inputs are start, end, description.
	p := &fakePrompter{choose: []int{1, 0}, inputs: []string{"", "", "edited description"}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || rec.Confirmed[0].ID != "c1" {
		t.Fatalf("want the same confirmed interval, got %+v", rec.Confirmed)
	}
	if rec.Confirmed[0].Description != "edited description" {
		t.Fatalf("an existing interval must be editable, got %q", rec.Confirmed[0].Description)
	}
}

// --- SKILL-1: state the defaults, ask only genuine unknowns -----------------
//
// An ACCEPTED invitation is the stated default for attendance, a project already
// set (from the source mapping) is kept as-is, and an empty exclusion set is the
// default. These tests pin the two interactive levers: attendance and project.

// unacceptedMeetingEvidenceAt is a meeting evidence whose invitation is NOT
// accepted (kept as a proposal input, so it is still proposed), so attendance is a
// genuine unknown and MUST be asked.
func unacceptedMeetingEvidenceAt(id string, start, end time.Time) evidence.Evidence {
	e := meetingEvidenceAt(id, start, end)
	e.Invitation = evidence.InvitationNeedsAction
	return e
}

// gitEvidenceWithProject is git evidence whose resolved project is set (as a
// configured mapping would supply), so the project is NOT a genuine unknown.
func gitEvidenceWithProject(project string) evidence.Evidence {
	e := gitEvidence()
	e.ProjectHints = []string{project}
	return e
}

// TestGuided_AcceptedInvitationDoesNotPromptAttendance: an accepted invitation
// (Preselected true) is the stated default, so attendance is treated as attended
// WITHOUT a prompt and the review stages attendance as attended.
func TestGuided_AcceptedInvitationDoesNotPromptAttendance(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		// meetingEvidenceAt sets Invitation=accepted -> Preselected true.
		m := meetingEvidenceAt("cal1", time.Date(2026, 10, 6, 9, 0, 0, 0, loc), time.Date(2026, 10, 6, 10, 0, 0, 0, loc))
		m.ProjectHints = []string{"Proj"} // a resolved project, so the preview is not blocked on it
		rec.CollectedEvidence = append(rec.CollectedEvidence, m)
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// confirm the meeting (0); no bulk (0); classification (0); no missing work;
	// confirm the review.
	p := &fakePrompter{choose: []int{0, 0, 0}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	for _, title := range p.confirmTitles {
		if strings.Contains(title, "Did you attend this meeting?") {
			t.Fatalf("an accepted invitation must NOT prompt for attendance, got %v", p.confirmTitles)
		}
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || !rec.Confirmed[0].Attendance {
		t.Fatalf("an accepted invitation must stage attendance as attended, got %+v", rec.Confirmed)
	}
	if !strings.Contains(out.String(), "invitation accepted") {
		t.Fatalf("the attendance default must be stated to the operator:\n%s", out.String())
	}
}

// TestGuided_UnacceptedInvitationStillPromptsAttendance: a non-accepted invitation
// is a genuine unknown, so the attendance prompt IS asked.
func TestGuided_UnacceptedInvitationStillPromptsAttendance(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		m := unacceptedMeetingEvidenceAt("cal1", time.Date(2026, 10, 6, 9, 0, 0, 0, loc), time.Date(2026, 10, 6, 10, 0, 0, 0, loc))
		m.ProjectHints = []string{"Proj"} // a resolved project, so the preview is not blocked on it
		rec.CollectedEvidence = append(rec.CollectedEvidence, m)
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// confirm the meeting (0) -> attendance asked (true); no bulk (0); classification
	// (0); no missing work; confirm the review.
	p := &fakePrompter{choose: []int{0, 0, 0}, confirms: []bool{true, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	n := 0
	for _, title := range p.confirmTitles {
		if strings.Contains(title, "Did you attend this meeting?") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("a non-accepted invitation must ask attendance exactly once, got %d (%v)", n, p.confirmTitles)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || !rec.Confirmed[0].Attendance {
		t.Fatalf("an attended meeting must stage attendance as attended, got %+v", rec.Confirmed)
	}
}

// TestGuided_UnacceptedMeetingEditStagesAttendance is F1 of the SKILL-1 review:
// the EDIT option for a meeting whose invitation is NOT accepted must stage
// attendance as attended when the operator answers "yes, attended". Before the
// SKILL-1 change the edit path staged NO attendance at all, so review.go's
// fallback (a meeting is attended only when Preselected) recorded an
// operator-attended meeting as NOT attended. This asserts through to the persisted
// record, which is where the old fallback was observable. The project is left
// unset so the edit path asks for it in both the old and the new code (identical
// input scripting), making any pre-change failure attributable to attendance.
func TestGuided_UnacceptedMeetingEditStagesAttendance(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence,
			unacceptedMeetingEvidenceAt("cal1", time.Date(2026, 10, 6, 9, 0, 0, 0, loc), time.Date(2026, 10, 6, 10, 0, 0, 0, loc)))
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// EDIT the meeting (2) -> attendance asked (true); times/project/description
	// edits; no bulk (0); classification (0); no missing work; confirm the review.
	p := &fakePrompter{
		choose:   []int{2, 0, 0},
		inputs:   []string{"09:00", "10:00", "Proj", "edited meeting"},
		confirms: []bool{true, false, true},
	}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code != exitOK && code != exitBlocked {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want 1 confirmed meeting, got %+v", rec.Confirmed)
	}
	if !rec.Confirmed[0].Attendance {
		t.Fatalf("a meeting attended via the EDIT option must be recorded as attended, got %+v", rec.Confirmed[0])
	}
}

// TestGuided_MappedProjectIsNotPromptedInEdit: a block whose project is already
// set (from the source mapping) keeps it as-is; choosing the edit option does NOT
// prompt for the project, and the option label is honest about that.
func TestGuided_MappedProjectIsNotPromptedInEdit(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidenceWithProject("Proj"))
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// edit the block (2): the project ("Proj") is already set, so it is NOT
	// prompted; inputs are start, end, description. No bulk (0); classification (0);
	// no missing work; confirm the review.
	p := &fakePrompter{choose: []int{2, 0, 0}, inputs: []string{"16:00", "17:00", "new desc"}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if len(p.chooseOptions) == 0 || len(p.chooseOptions[0]) < 3 {
		t.Fatalf("want the block option list, got %v", p.chooseOptions)
	}
	if got := p.chooseOptions[0][2].Label; got != "Edit times / description" {
		t.Fatalf("a set project must make the edit label honest, got %q", got)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || rec.Confirmed[0].Project != "Proj" {
		t.Fatalf("a mapped project must be kept as-is, got %+v", rec.Confirmed)
	}
	if rec.Confirmed[0].Description != "new desc" {
		t.Fatalf("the description must still be editable, got %q", rec.Confirmed[0].Description)
	}
	if !strings.Contains(out.String(), "already set") {
		t.Fatalf("the kept project must be stated to the operator:\n%s", out.String())
	}
}

// TestGuided_UnmappedProjectStillPrompts: an EMPTY project is a genuine unknown, so
// the edit option still asks for it.
func TestGuided_UnmappedProjectStillPrompts(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence()) // no project
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// edit the block (2): the project is UNSET, so it IS prompted (start, end,
	// project, description). No bulk (0); classification (0); no missing work;
	// confirm the review.
	p := &fakePrompter{choose: []int{2, 0, 0}, inputs: []string{"16:00", "17:00", "Proj", "work"}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if len(p.chooseOptions) == 0 || len(p.chooseOptions[0]) < 3 {
		t.Fatalf("want the block option list, got %v", p.chooseOptions)
	}
	if got := p.chooseOptions[0][2].Label; got != "Edit times / project / description" {
		t.Fatalf("an unset project must keep the project in the edit label, got %q", got)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || rec.Confirmed[0].Project != "Proj" {
		t.Fatalf("an unset project must still be prompted and applied, got %+v", rec.Confirmed)
	}
}

// TestGuided_CarrySurfacedAndExplicit proves G1/G3: a carry from the previous
// day is surfaced and labeled "carryover", requires an explicit choice (never
// auto-confirmed), and the guided final confirmation defaults to "no".
func TestGuided_CarrySurfacedAndExplicit(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	// The PREVIOUS day holds a carry targeting testDate.
	seedRecord(t, storage, "2026-10-05", func(rec *workflow.Record) {
		rec.Carries = []workflow.CarryItem{{
			SourceDate: "2026-10-05", Date: testDate, Start: "00:00", End: "00:30",
			StartAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
			EndAt:   time.Date(2026, 10, 6, 0, 30, 0, 0, time.UTC),
			Minutes: 30, SplitGroupID: "split_late", PartIndex: 1,
			Kind: string(proposal.BlockManual), Project: "Proj", Description: "late work",
		}}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// confirm the carry block (0); no missing work (false); no bulk (0);
	// classification option 0; apply the review (true).
	p := &fakePrompter{choose: []int{0, 0, 0}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "carryover") {
		t.Fatalf("the guided run must label the carry block:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "carryover items:") {
		t.Fatalf("the review summary must disclose the carryover count:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 || !rec.Confirmed[0].Carryover {
		t.Fatalf("the carry must be confirmed ONLY by the explicit choice: %+v", rec.Confirmed)
	}
	if len(rec.Approvals) != 0 {
		t.Fatalf("the guided run must not approve")
	}
	if got := p.confirmDefaults[len(p.confirmDefaults)-1]; got != false {
		t.Fatalf("the guided final confirmation default must be no, got %v", got)
	}
}

// TestGuided_CarryNotConfirmedWritesNothing proves G3: answering "no" to the
// final confirmation leaves the carry unconfirmed and unapproved.
func TestGuided_CarryNotConfirmedWritesNothing(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, "2026-10-05", func(rec *workflow.Record) {
		rec.Carries = []workflow.CarryItem{{
			SourceDate: "2026-10-05", Date: testDate, Start: "00:00", End: "00:30",
			StartAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
			EndAt:   time.Date(2026, 10, 6, 0, 30, 0, 0, time.UTC),
			Minutes: 30, SplitGroupID: "split_late", PartIndex: 1,
			Kind: string(proposal.BlockManual), Project: "Proj", Description: "late work",
		}}
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// confirm the carry block (0); no missing work (false); no bulk (0);
	// classification 0; DECLINE the final confirmation (false).
	p := &fakePrompter{choose: []int{0, 0, 0}, confirms: []bool{false, false}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code == exitOK {
		t.Fatalf("a declined confirmation must not exit ok:\n%s", out.String())
	}
	rec, err := workflow.NewJSONFileStore(storage)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := rec.GetDay(context.Background(), testDate)
	if err == nil && (len(got.Confirmed) != 0 || len(got.Approvals) != 0) {
		t.Fatalf("a declined confirmation must write nothing: %+v", got)
	}
}

// --- B6: explicit --ack is staged, validated, and applied only on confirm ----
func TestGuided_ExplicitAckStagedNoPrompt(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Collection = incompleteCollection()
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// keep existing (0); NO ack prompt (already staged); no reset; no missing work; no bulk; confirm.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "cal-work", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review == nil || len(rec.Review.AcknowledgedSources) != 1 || rec.Review.AcknowledgedSources[0] != "cal-work" {
		t.Fatalf("the explicit --ack must be staged and applied, got %+v", rec.Review)
	}
	if strings.Contains(out.String(), "Acknowledge these incomplete sources") {
		t.Fatalf("an explicit --ack must NOT prompt again:\n%s", out.String())
	}
}

func TestGuided_UnknownAckReportedAndSkipped(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Collection = incompleteCollection()
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// keep existing (0); the unknown ack is skipped so the real prompt appears;
	// acknowledge it (true); no reset; no missing work; no bulk; confirm.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{true, false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "bogus", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "bogus") {
		t.Fatalf("an unknown --ack id must be reported, got:\n%s", errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review == nil || len(rec.Review.AcknowledgedSources) != 1 || rec.Review.AcknowledgedSources[0] != "cal-work" {
		t.Fatalf("want only cal-work acknowledged, got %+v", rec.Review)
	}
}

func TestGuided_ExplicitAckNotAppliedOnAbort(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Collection = incompleteCollection()
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// keep existing (0); no reset; no bulk (0); no missing work; CANCEL at the final confirmation.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{false, false, false}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "cal-work", testDate, p, &out, &errb)
	if code == exitOK {
		t.Fatalf("a cancelled final confirmation must not succeed")
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review != nil && len(rec.Review.AcknowledgedSources) != 0 {
		t.Fatalf("an aborted run must not apply the staged --ack, got %+v", rec.Review)
	}
}

// --- B7: terminal detection uses the PASSED writer's actual descriptor --------

func TestWriterIsTTY_RejectsBufferAndPipe(t *testing.T) {
	if writerIsTTY(&bytes.Buffer{}) {
		t.Fatalf("a buffer is not a terminal")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pr.Close()
	defer pw.Close()
	if writerIsTTY(pw) {
		t.Fatalf("a pipe is not a terminal")
	}
}

func TestGuided_RedirectedWriterRefusesTUI(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, guidedTestConfig())
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pr.Close()
	defer pw.Close()
	var stderr bytes.Buffer
	// stdin is reported as a terminal, but the PASSED stdout is a pipe -> refuse.
	code := runCLIWithIO(context.Background(),
		[]string{"run", "--interactive", "--config", cfgPath, "--storage-dir", filepath.Join(dir, "records"), "--date", testDate},
		strings.NewReader(""), pw, &stderr, true, writerIsTTY(pw))
	if code != exitUsage {
		t.Fatalf("a redirected stdout must refuse the TUI, got %d (%s)", code, stderr.String())
	}
}

// --- B8: an invalid time edit is re-prompted, never silently dropped ---------

func TestGuided_ExistingEditInvalidTimeReprompts(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// edit existing (1); no reset; an invalid start, then a valid 09:30-10:30; the
	// project is already set so it is NOT prompted; new description; no missing work;
	// confirm.
	p := &fakePrompter{
		choose:   []int{1, 0},
		inputs:   []string{"9am", "09:30", "10:30", "edited"},
		confirms: []bool{false, false, true},
	}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "invalid time") {
		t.Fatalf("an invalid time must be reported:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].StartTime != "09:30" || rec.Confirmed[0].EndTime != "10:30" {
		t.Fatalf("want the corrected span 09:30-10:30, got %s-%s", rec.Confirmed[0].StartTime, rec.Confirmed[0].EndTime)
	}
	if rec.Confirmed[0].Description != "edited" {
		t.Fatalf("want the description applied, got %q", rec.Confirmed[0].Description)
	}
}

func TestGuided_ProposedEditInvalidTimeReprompts(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// edit the proposed block (2); an invalid end, then a valid pair; project; desc;
	// no missing work; classification; confirm.
	p := &fakePrompter{
		choose:   []int{2, 0, 0},
		inputs:   []string{"16:00", "25:00", "17:00", "Proj", "work"},
		confirms: []bool{false, true},
	}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "invalid time") {
		t.Fatalf("an invalid time must be reported:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].StartTime != "16:00" || rec.Confirmed[0].EndTime != "17:00" {
		t.Fatalf("want the corrected span 16:00-17:00, got %s-%s", rec.Confirmed[0].StartTime, rec.Confirmed[0].EndTime)
	}
}

func TestGuided_EOFFollowingInvalidTimePreservesPriorState(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Approvals = []workflow.Approval{{ApprovalID: "a1", PlanID: "p1", Date: testDate}}
	})
	before := loadRecord(t, storage, testDate)
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// no reset (false); edit existing (1); an invalid start, then EOF (the fake runs out).
	p := &fakePrompter{choose: []int{1}, inputs: []string{"9am"}, confirms: []bool{false}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code == exitOK {
		t.Fatalf("an EOF after an invalid time must abort")
	}
	after := loadRecord(t, storage, testDate)
	if len(after.Confirmed) != len(before.Confirmed) || after.Confirmed[0].StartTime != before.Confirmed[0].StartTime {
		t.Fatalf("an abort must preserve the confirmed interval:\n before=%+v\n after=%+v", before.Confirmed, after.Confirmed)
	}
	if len(after.Approvals) != 1 || after.Approvals[0].InvalidatedAt != nil {
		t.Fatalf("an abort must preserve the approval, got %+v", after.Approvals)
	}
}

// --- in-run catalog memoization ---------------------------------------------

// countingCatalogFetch is the injected catalogFetch seam for the guided run's
// per-run memoization tests. It records every query it is asked for and returns
// the fixed catalog (or err), so a test can prove exactly how many live fetches
// a sequence of loadCatalog calls triggers. No live service is contacted.
type countingCatalogFetch struct {
	calls   []string
	catalog gradion.Catalog
	err     error
}

func (c *countingCatalogFetch) fetch(_ context.Context, _ cliConfig, _ bool, query string) (gradion.Catalog, error) {
	c.calls = append(c.calls, query)
	if c.err != nil {
		return gradion.Catalog{}, c.err
	}
	cat := c.catalog
	cat.Query = query
	cat.Source = "live"
	return cat, nil
}

// TestGuided_LoadCatalogMemoizesPerRun proves the guided run fetches each
// distinct query AT MOST ONCE per run: a repeated query returns the memoized
// catalog with the ORIGINAL fetch's provenance, while a different query fetches
// separately (and memoizes in turn). On the pre-fix code the repeated query
// fetched twice, so this is the regression guard.
func TestGuided_LoadCatalogMemoizesPerRun(t *testing.T) {
	t.Setenv(selection.EnvHome, t.TempDir())
	fetch := &countingCatalogFetch{catalog: gradion.Catalog{
		Complete: true, ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{{ID: "cls-1", Code: "INTGRADI2610", Name: "X", ActivityType: "internal project"}},
	}}
	g := &guided{catalogFetch: fetch.fetch}
	ctx := context.Background()

	c1, p1, err := g.loadCatalog(ctx, "")
	if err != nil {
		t.Fatalf("first loadCatalog: %v", err)
	}
	c2, p2, err := g.loadCatalog(ctx, "")
	if err != nil {
		t.Fatalf("second loadCatalog: %v", err)
	}
	if len(fetch.calls) != 1 {
		t.Fatalf("a repeated query must fetch exactly once per run, got %d fetches %v", len(fetch.calls), fetch.calls)
	}
	if p2 != p1 {
		t.Fatalf("the memoized provenance must equal the first fetch's, got %q then %q", p1, p2)
	}
	if len(c2.Options) != 1 || len(c1.Options) != 1 || c2.Options[0].ID != c1.Options[0].ID {
		t.Fatalf("the memoized catalog must equal the first fetch's, got %+v then %+v", c1.Options, c2.Options)
	}

	// A DIFFERENT query is a separate fetch, and memoizes too.
	if _, _, err := g.loadCatalog(ctx, "INTGRADI"); err != nil {
		t.Fatalf("different-query loadCatalog: %v", err)
	}
	if len(fetch.calls) != 2 || fetch.calls[1] != "INTGRADI" {
		t.Fatalf("a different query must fetch separately, got %v", fetch.calls)
	}
	if _, _, err := g.loadCatalog(ctx, "INTGRADI"); err != nil {
		t.Fatalf("repeat different-query loadCatalog: %v", err)
	}
	if len(fetch.calls) != 2 {
		t.Fatalf("the second query must also memoize, got %v", fetch.calls)
	}
}

// TestGuided_LoadCatalogMemoIsPerRun proves the memo is scoped to ONE run: two
// separate guided runs each fetch the same query once, so the memo never leaks
// across runs.
func TestGuided_LoadCatalogMemoIsPerRun(t *testing.T) {
	t.Setenv(selection.EnvHome, t.TempDir())
	fetch := &countingCatalogFetch{catalog: gradion.Catalog{Complete: true, ObservedAt: time.Now().UTC()}}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		g := &guided{catalogFetch: fetch.fetch}
		if _, _, err := g.loadCatalog(ctx, ""); err != nil {
			t.Fatalf("run %d loadCatalog: %v", i, err)
		}
	}
	if len(fetch.calls) != 2 {
		t.Fatalf("the memo must not leak across runs, want 2 fetches, got %d", len(fetch.calls))
	}
}

// TestGuided_LoadCatalogCacheFallbackOnLiveError proves the live-first cache
// fallback is preserved AND never bypassed by the memo: a live error returns the
// fresh cached catalog, and a repeat still tries live first (the error is not
// memoized).
func TestGuided_LoadCatalogCacheFallbackOnLiveError(t *testing.T) {
	t.Setenv(selection.EnvHome, t.TempDir())
	cachePath, err := selection.DefaultCatalogCachePath()
	if err != nil {
		t.Fatalf("cache path: %v", err)
	}
	cached := gradion.Catalog{
		Complete: true, ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{{ID: "cls-1", Name: "X", ActivityType: "internal project"}},
	}
	if err := selection.SaveCatalogCache(cachePath, cached); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	fetch := &countingCatalogFetch{err: errors.New("live catalog unavailable")}
	g := &guided{catalogFetch: fetch.fetch}
	ctx := context.Background()

	got, prov, err := g.loadCatalog(ctx, "")
	if err != nil {
		t.Fatalf("a live error must still fall back to the fresh cache, got %v", err)
	}
	if len(got.Options) != 1 || got.Options[0].ID != "cls-1" {
		t.Fatalf("want the cached catalog, got %+v", got.Options)
	}
	if !strings.Contains(prov, "cache") {
		t.Fatalf("want cache provenance, got %q", prov)
	}
	if _, _, err := g.loadCatalog(ctx, ""); err != nil {
		t.Fatalf("repeat fallback: %v", err)
	}
	if len(fetch.calls) != 2 {
		t.Fatalf("a live error must not be memoized (live-first preserved), want 2 live attempts, got %d", len(fetch.calls))
	}
}

// --- TUI-USABILITY-1: hierarchy, readable labels, provenance, markers --------

// firstLineWith returns the first line whose trimmed form starts with prefix.
func firstLineWith(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, prefix) {
			return t
		}
	}
	return ""
}

// TestGuided_SectionHierarchyAndProvenance proves the guided report reads
// top-down with titled sections, and that a proposed block renders its SOURCE
// provenance (git repo/branch) rather than only an opaque evidence id.
func TestGuided_SectionHierarchyAndProvenance(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence(), journalEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// exclude the git work block (1), skip the narrative (0), no missing work,
	// no bulk, confirm the review.
	p := &fakePrompter{choose: []int{1, 0, 0}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{
		"timesheet · guided review",
		"Preflight", "Sources", "Proposal", "Journal narratives", "Review summary", "Submission plan",
		"git acme/repo@feat/x",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guided output missing %q:\n%s", want, got)
		}
	}
}

// TestGuided_UnknownDurationMarkerIsExplicit proves an item whose duration is NOT
// known is marked with the ONE unmistakable marker and never a numeric duration,
// for BOTH a git work block (an observation window, not timed work) and an untimed
// journal narrative.
func TestGuided_UnknownDurationMarkerIsExplicit(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence(), journalEvidence())
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// leave the work block unconfirmed (3), skip the narrative (0), no missing
	// work, no bulk, confirm the review.
	p := &fakePrompter{choose: []int{3, 0, 0}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	got := out.String()
	work := firstLineWith(got, "work ")
	if work == "" {
		t.Fatalf("want a work block line:\n%s", got)
	}
	if !strings.Contains(work, "duration=unknown") {
		t.Fatalf("the git work block must be marked duration=unknown, got %q", work)
	}
	// A span is shown, but never a numeric duration for the unknown block.
	if !strings.Contains(work, "16:00") {
		t.Fatalf("the work block must still show its observed span, got %q", work)
	}
	if !strings.Contains(got, "duration=unknown") {
		t.Fatalf("want the unknown-duration marker in the report:\n%s", got)
	}
	// The untimed narrative carries the marker and its quoted provenance.
	if !strings.Contains(got, "stated=2026-10-06") {
		t.Fatalf("want the narrative's quoted provenance:\n%s", got)
	}
}

// TestGuided_IncompleteSourceWarningIsProminent proves the guided run names the
// incomplete source(s), states the preview is BLOCKED until acknowledged, and
// gives a secret-free detail, not just a bare status word.
func TestGuided_IncompleteSourceWarningIsProminent(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Collection = incompleteCollection()
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// do NOT acknowledge (false); no reset; no missing work; no bulk; confirm.
	p := &fakePrompter{choose: []int{0, 0}, confirms: []bool{false, false, false, true}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code != exitBlocked {
		t.Fatalf("want a blocked preview, got %d\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"Incomplete sources",
		"BLOCKED until each is acknowledged",
		"cal-work",
		"calendar",
		"SOURCE_ERROR",
		"incomplete_collection",
		// The Sources section points forward to the acknowledgment step, so an
		// incomplete source is findable where it is collected, not only later.
		"incomplete -> the preview blocks",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guided output missing %q:\n%s", want, got)
		}
	}
}

// TestGuided_MeetingAttendanceLabelAndCalendarProvenance proves a meeting shows a
// human attendance label (never the raw enum) and names its calendar provenance.
func TestGuided_MeetingAttendanceLabelAndCalendarProvenance(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence,
			meetingEvidenceAt("cal1", time.Date(2026, 10, 6, 9, 0, 0, 0, loc), time.Date(2026, 10, 6, 10, 0, 0, 0, loc)))
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// leave the meeting unconfirmed (3); no missing work; no bulk; confirm.
	p := &fakePrompter{choose: []int{3, 0}, confirms: []bool{false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	got := out.String()
	meeting := firstLineWith(got, "meeting ")
	if meeting == "" {
		t.Fatalf("want a meeting block line:\n%s", got)
	}
	if !strings.Contains(meeting, "attendance: unconfirmed") {
		t.Fatalf("a meeting must show a human attendance label, got %q", meeting)
	}
	if strings.Contains(meeting, "attendance=unresolved") {
		t.Fatalf("the raw attendance enum must not be printed, got %q", meeting)
	}
	if !strings.Contains(meeting, "calendar") {
		t.Fatalf("a meeting must name its calendar provenance, got %q", meeting)
	}
}

// TestTUIHelpNamesAbortPath proves every huh key-hint line names the abort key, so
// the abort path is never hidden in the terminal UI.
func TestTUIHelpNamesAbortPath(t *testing.T) {
	for name, help := range map[string]string{
		"select": helpSelect, "multi": helpMulti, "input": helpInput, "confirm": helpConfirm,
	} {
		if !strings.Contains(help, "abort") || !strings.Contains(help, "ctrl+c") {
			t.Errorf("%s help must name the abort key, got %q", name, help)
		}
	}
}

// TestTUIHelpReachesGroupHeader proves the key-hint line is actually carried into
// the rendered huh group header, not merely held in a constant: huh renders the
// group description as the header, which Group.View includes above the field.
func TestTUIHelpReachesGroupHeader(t *testing.T) {
	tp := newTUIPrompter(strings.NewReader(""), &bytes.Buffer{})
	cases := []struct {
		name  string
		field huh.Field
		help  string
	}{
		{"select", huh.NewSelect[int]().Title("t"), helpSelect},
		{"multi", huh.NewMultiSelect[int]().Title("t"), helpMulti},
		{"input", huh.NewInput().Title("t"), helpInput},
		{"confirm", huh.NewConfirm().Title("t"), helpConfirm},
	}
	for _, tc := range cases {
		if header := tp.newGroup(tc.field, tc.help).Header(); !strings.Contains(header, tc.help) {
			t.Errorf("%s help must reach the rendered group header, got %q", tc.name, header)
		}
	}
}

// TestExcludedLabelIdentifiesDetachedItem proves a detached excluded item is
// identifiable by its TITLE and the note's own STATED date, even with no heading.
func TestExcludedLabelIdentifiesDetachedItem(t *testing.T) {
	ex := proposal.ExcludedEvidence{
		EvidenceID: "j-detached-1", Reason: proposal.ExcludeDetached,
		Title: "Old parser note", NotePath: "02 - Daily & Logs/2026-10-05.md",
		StatedDate: "2026-10-05", LineStart: 4, LineEnd: 6,
	}
	got := excludedLabel(ex)
	for _, want := range []string{
		"reason=detached", "title=Old parser note", "stated=2026-10-05",
		"note=02 - Daily & Logs/2026-10-05.md", "lines=4-6",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("excluded label missing %q: %q", want, got)
		}
	}
}

// TestNarrativeHintDropsRedundantJournalLabel proves the link-option hint drops the
// leading "journal" label (the option is already labeled "journal:"), while keeping
// the unknown-duration marker.
func TestNarrativeHintDropsRedundantJournalLabel(t *testing.T) {
	evByID := map[string]evidence.Evidence{"j1": journalEvidence()}
	n := proposal.NarrativeCandidate{ID: "j1", Section: "Work", Description: "x", EvidenceIDs: []string{"j1"}}
	hint := narrativeHint(n, evByID)
	if strings.HasPrefix(hint, "journal") {
		t.Fatalf("the link hint must not repeat the journal label, got %q", hint)
	}
	if !strings.Contains(hint, "duration=unknown") {
		t.Fatalf("the link hint must keep the unknown-duration marker, got %q", hint)
	}
}

// TestPlainPrompter_PrintsOneTimeHelp proves the plain path prints its answer/abort
// hint exactly once per session (and never hides the abort path).
func TestPlainPrompter_PrintsOneTimeHelp(t *testing.T) {
	var out bytes.Buffer
	p := newPlainPrompter(strings.NewReader("1\ny\n"), &out)
	if _, err := p.Choose(context.Background(), "t", []promptOption{{Label: "a"}}, 0); err != nil {
		t.Fatalf("choose: %v", err)
	}
	if _, err := p.Confirm(context.Background(), "t", true); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if n := strings.Count(out.String(), "EOF or Ctrl-C aborts"); n != 1 {
		t.Fatalf("the help hint must print exactly once, got %d:\n%s", n, out.String())
	}
}

// --- BULK-REVIEW-1: scoped bulk actions --------------------------------------

// bulkTime is a fixed UTC clock for constructing proposed blocks in bulk tests.
func bulkTime(h, m int) time.Time { return time.Date(2026, 10, 6, h, m, 0, 0, time.UTC) }

func bulkClock(h, m int) string { return fmt.Sprintf("%02d:%02d", h, m) }

func bulkMeeting(id, project string, sh, sm, eh, em int, accepted bool) proposal.ProposedTimeBlock {
	return proposal.ProposedTimeBlock{
		ID: id, Kind: proposal.BlockMeeting, Date: testDate,
		Start: bulkClock(sh, sm), End: bulkClock(eh, em),
		StartAt: bulkTime(sh, sm), EndAt: bulkTime(eh, em),
		Duration:    evidence.KnownDuration((eh*60 + em) - (sh*60 + sm)),
		Preselected: accepted,
		Project:     proposal.ProjectResolution{Project: project},
	}
}

func bulkManual(id, project string, sh, sm, eh, em int) proposal.ProposedTimeBlock {
	return proposal.ProposedTimeBlock{
		ID: id, Kind: proposal.BlockManual, Date: testDate,
		Start: bulkClock(sh, sm), End: bulkClock(eh, em),
		StartAt: bulkTime(sh, sm), EndAt: bulkTime(eh, em),
		Duration: evidence.KnownDuration((eh*60 + em) - (sh*60 + sm)),
		Project:  proposal.ProjectResolution{Project: project},
	}
}

func bulkWork(id, project string, sh, sm, eh, em int) proposal.ProposedTimeBlock {
	return proposal.ProposedTimeBlock{
		ID: id, Kind: proposal.BlockWork, Date: testDate,
		Start: bulkClock(sh, sm), End: bulkClock(eh, em),
		StartAt: bulkTime(sh, sm), EndAt: bulkTime(eh, em),
		Duration: evidence.UnknownDuration(),
		Project:  proposal.ProjectResolution{Project: project},
	}
}

// bulkPreviewSection extracts the deterministic "Bulk actions" preview block (from
// its title through the counts line), so a parity check ignores prompt chrome.
func bulkPreviewSection(out string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "Bulk actions" {
			in = true
		}
		if !in {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		if strings.Contains(line, "requires explicit collision") {
			break
		}
	}
	return b.String()
}

// TestBulk_PreviewListsExactAffectedBlocks proves the preview lists EXACTLY the
// in-scope blocks with their per-block action and the counts.
func TestBulk_PreviewListsExactAffectedBlocks(t *testing.T) {
	meeting := bulkMeeting("meeting_aaa111", "Proj", 9, 0, 10, 0, true)
	work := bulkWork("work_bbb222", "", 11, 0, 11, 30)
	prop := proposal.Proposal{Work: []proposal.ProposedTimeBlock{work}, Meetings: []proposal.ProposedTimeBlock{meeting}}

	var out bytes.Buffer
	g := &guided{stdout: &out, stderr: &bytes.Buffer{}}
	scope := bulkScope{mode: bulkScopeAll}
	action := bulkAction{kind: bulkActionConfirm}
	plan := g.planBulk(prop, scope, action, nil, run.ReviewDecisions{})
	g.printBulkPreview(nil, scope, action, plan)

	if len(plan.candidates) != 2 {
		t.Fatalf("want exactly 2 in-scope blocks, got %d", len(plan.candidates))
	}
	if len(plan.applied) != 1 || plan.applied[0].ID != meeting.ID {
		t.Fatalf("want only the accepted meeting applied, got %+v", plan.applied)
	}
	if plan.disp[work.ID] != bulkSkipUnknown {
		t.Fatalf("want the work block skipped as unknown-duration, got %v", plan.disp[work.ID])
	}
	got := out.String()
	for _, want := range []string{
		"confirm 1", "skipped unknown-duration 1", "requires explicit collision 0",
		"apply", "skip (unknown)", shortID(meeting.ID), shortID(work.ID),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q:\n%s", want, got)
		}
	}
}

// TestBulk_UnknownDurationNeverConfirmed proves an unknown-duration block is never
// bulk-confirmed and never reaches dec.Confirm.
func TestBulk_UnknownDurationNeverConfirmed(t *testing.T) {
	work := bulkWork("work_bbb222", "", 11, 0, 11, 30)
	prop := proposal.Proposal{Work: []proposal.ProposedTimeBlock{work}}
	g := &guided{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	action := bulkAction{kind: bulkActionConfirm}
	plan := g.planBulk(prop, bulkScope{mode: bulkScopeAll}, action, nil, run.ReviewDecisions{})
	if plan.disp[work.ID] != bulkSkipUnknown {
		t.Fatalf("want the work block skipped as unknown-duration, got %v", plan.disp[work.ID])
	}
	dec := run.ReviewDecisions{}
	stageBulk(&dec, action, plan.applied)
	if len(dec.Confirm) != 0 {
		t.Fatalf("an unknown-duration block must never be bulk-confirmed, got %+v", dec.Confirm)
	}
}

// TestBulk_ExcludeDropsEveryEvidenceAndWarns proves a multi-evidence block's
// exclude stages EVERY evidence id (deduped), and the preview warns only for a
// confirmed interval that loses ALL of its evidence.
func TestBulk_ExcludeDropsEveryEvidenceAndWarns(t *testing.T) {
	work := bulkWork("work_bbb222", "Proj", 11, 0, 11, 30)
	work.EvidenceIDs = []string{"e1", "e2", "e1"} // deduped
	prop := proposal.Proposal{Work: []proposal.ProposedTimeBlock{work}}
	rec := &workflow.Record{Confirmed: []workflow.ConfirmedInterval{
		{ID: "c1", EvidenceIDs: []string{"e1"}},               // loses all evidence -> warn
		{ID: "c2", EvidenceIDs: []string{"e1", "e2"}},         // loses all evidence -> warn
		{ID: "c3", EvidenceIDs: []string{"e1", "e9"}},         // keeps e9 -> no warn
		{ID: "c4", Manual: true, EvidenceIDs: []string{"e1"}}, // manual -> no warn
	}}
	action := bulkAction{kind: bulkActionExclude}
	scope := bulkScope{mode: bulkScopeAll}

	var out bytes.Buffer
	g := &guided{stdout: &out, stderr: &bytes.Buffer{}}
	plan := g.planBulk(prop, scope, action, rec, run.ReviewDecisions{})
	g.printBulkPreview(rec, scope, action, plan)
	got := out.String()
	for _, want := range []string{"would drop confirmed interval c1", "would drop confirmed interval c2"} {
		if !strings.Contains(got, want) {
			t.Errorf("the preview must warn %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"interval c3", "interval c4"} {
		if strings.Contains(got, bad) {
			t.Errorf("the preview must NOT warn %q:\n%s", bad, got)
		}
	}

	dec := run.ReviewDecisions{}
	stageBulk(&dec, action, plan.applied)
	if len(dec.Exclude) != 2 {
		t.Fatalf("want every deduped evidence id staged for exclusion, got %+v", dec.Exclude)
	}
	for _, want := range []string{"e1", "e2"} {
		if !hasExclude(dec, want) {
			t.Fatalf("want %q staged for exclusion, got %+v", want, dec.Exclude)
		}
	}
}

// TestBulk_CollisionRequiresExplicitDecision proves a meeting overlapping a
// per-block-confirmed work block is excluded from the bulk set, is surfaced as
// requiring an explicit collision decision, and never reaches the record (so no
// overlap reason appears in Readiness).
func TestBulk_CollisionRequiresExplicitDecision(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		m1 := manualEvidenceAt("m1", "Proj", time.Date(2026, 10, 6, 9, 0, 0, 0, loc), time.Date(2026, 10, 6, 10, 0, 0, 0, loc))
		cal1 := meetingEvidenceAt("cal1", time.Date(2026, 10, 6, 9, 30, 0, 0, loc), time.Date(2026, 10, 6, 10, 30, 0, 0, loc))
		cal2 := meetingEvidenceAt("cal2", time.Date(2026, 10, 6, 13, 0, 0, 0, loc), time.Date(2026, 10, 6, 14, 0, 0, 0, loc))
		cal2.ProjectHints = []string{"Proj"}
		rec.CollectedEvidence = append(rec.CollectedEvidence, m1, cal1, cal2)
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// confirm M per-block (0); leave cal1, cal2 unconfirmed (3); bulk-confirm (1)
	// scope by kind (1) meeting (1); classification for M and cal2 (0,0); no
	// missing work; consent; apply.
	p := &fakePrompter{choose: []int{0, 3, 3, 1, 1, 1, 0, 0}, confirms: []bool{false, true, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 2 {
		t.Fatalf("want the per-block M + the non-colliding cal2, got %+v", rec.Confirmed)
	}
	for _, ci := range rec.Confirmed {
		if containsString(ci.EvidenceIDs, "cal1") {
			t.Fatalf("the colliding meeting must NOT be confirmed, got %+v", ci)
		}
	}
	foundCal2 := false
	for _, ci := range rec.Confirmed {
		if containsString(ci.EvidenceIDs, "cal2") {
			foundCal2 = true
		}
	}
	if !foundCal2 {
		t.Fatalf("want cal2 confirmed, got %+v", rec.Confirmed)
	}
	for _, reason := range review.Readiness(rec).Reasons {
		if strings.HasPrefix(reason, "overlap:") {
			t.Fatalf("no overlap may reach the record, got reason %q", reason)
		}
	}
	if !strings.Contains(out.String(), "skip (collision)") || !strings.Contains(out.String(), "requires explicit collision 1") {
		t.Fatalf("the collision must be surfaced as requiring an explicit decision:\n%s", out.String())
	}
}

// TestBulk_CollisionWithAlreadyConfirmedWorkIsSkipped is the F-A regression: a
// meeting overlapping an ALREADY-CONFIRMED work interval that is OUT of scope is
// still skipped as a collision (never silently bulk-confirmed into an overlap).
func TestBulk_CollisionWithAlreadyConfirmedWorkIsSkipped(t *testing.T) {
	meeting := bulkMeeting("meeting_aaa111", "Proj", 9, 30, 10, 30, true)
	prop := proposal.Proposal{Meetings: []proposal.ProposedTimeBlock{meeting}}
	// An already-confirmed work interval overlaps the meeting and is OUT of scope
	// (scope = by kind meeting).
	rec := &workflow.Record{Confirmed: []workflow.ConfirmedInterval{{
		ID: "c1", Kind: string(proposal.BlockWork), StartTime: "09:00", EndTime: "10:00", Project: "Proj",
	}}}
	scope := bulkScope{mode: bulkScopeByKind, kind: proposal.BlockMeeting}
	action := bulkAction{kind: bulkActionConfirm}
	g := &guided{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	plan := g.planBulk(prop, scope, action, rec, run.ReviewDecisions{})
	if plan.disp[meeting.ID] != bulkSkipCollision {
		t.Fatalf("a meeting overlapping already-confirmed work must be a collision, got %v", plan.disp[meeting.ID])
	}
	dec := run.ReviewDecisions{}
	stageBulk(&dec, action, plan.applied)
	if len(dec.Confirm) != 0 {
		t.Fatalf("the colliding meeting must never be bulk-confirmed, got %+v", dec.Confirm)
	}
}

// TestBulk_CollisionWithStagedManualIsSkipped is the NF-1 regression: a meeting
// left unconfirmed per-block, then a manual interval overlapping it added via the
// missing-work question (which runs BEFORE the bulk step and stages
// ManualDecision{Confirm:true}), then bulk-confirm by kind meeting -> the meeting
// is skipped as requiring an explicit collision decision, and the applied record
// has no overlapping confirmed intervals.
func TestBulk_CollisionWithStagedManualIsSkipped(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence,
			meetingEvidenceAt("cal1", time.Date(2026, 10, 6, 9, 30, 0, 0, loc), time.Date(2026, 10, 6, 10, 30, 0, 0, loc)))
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// leave cal1 unconfirmed (3); missing work = YES; manual 09:00-10:00 (no links,
	// no second interval); bulk-confirm (1) by kind (1) meeting (1); classification
	// (0); apply.
	p := &fakePrompter{
		choose:   []int{3, 1, 1, 1, 0},
		inputs:   []string{"09:00", "10:00", "Proj", "manual overlap"},
		confirms: []bool{true, false, true},
	}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("only the manual interval may be confirmed, got %+v", rec.Confirmed)
	}
	if containsString(rec.Confirmed[0].EvidenceIDs, "cal1") {
		t.Fatalf("the colliding meeting must NOT be confirmed, got %+v", rec.Confirmed[0])
	}
	for _, reason := range review.Readiness(rec).Reasons {
		if strings.HasPrefix(reason, "overlap:") {
			t.Fatalf("no overlap may reach the record, got reason %q", reason)
		}
	}
	if !strings.Contains(out.String(), "skip (collision)") || !strings.Contains(out.String(), "requires explicit collision 1") {
		t.Fatalf("the collision must be surfaced as requiring an explicit decision:\n%s", out.String())
	}
}

// TestBulk_PerBlockOverrideWinsBothOrders proves a per-block decision always wins,
// regardless of order: bulk staging is ADD-IF-ABSENT, per-block staging replaces.
func TestBulk_PerBlockOverrideWinsBothOrders(t *testing.T) {
	block := bulkMeeting("meeting_aaa111", "Proj", 9, 0, 10, 0, true)

	// Per-block first, then bulk: the per-block project must win.
	dec := run.ReviewDecisions{}
	addConfirm(&dec, run.ConfirmDecision{BlockID: block.ID, Project: "PerBlock"})
	stageBulk(&dec, bulkAction{kind: bulkActionAssignProject, project: "Bulk"}, []proposal.ProposedTimeBlock{block})
	if len(dec.Confirm) != 1 || dec.Confirm[0].Project != "PerBlock" {
		t.Fatalf("a per-block decision must win over a later bulk add, got %+v", dec.Confirm)
	}

	// Bulk first, then per-block: the per-block replacement must win.
	dec2 := run.ReviewDecisions{}
	stageBulk(&dec2, bulkAction{kind: bulkActionAssignProject, project: "Bulk"}, []proposal.ProposedTimeBlock{block})
	if len(dec2.Confirm) != 1 || dec2.Confirm[0].Project != "Bulk" {
		t.Fatalf("bulk must stage the project add-if-absent, got %+v", dec2.Confirm)
	}
	addConfirm(&dec2, run.ConfirmDecision{BlockID: block.ID, Project: "PerBlock"})
	if len(dec2.Confirm) != 1 || dec2.Confirm[0].Project != "PerBlock" {
		t.Fatalf("a per-block decision must replace a prior bulk add, got %+v", dec2.Confirm)
	}
}

// TestBulk_AbortAtBulkConsentAppliesNothing proves an abort at the bulk consent
// applies nothing (the staged-only model never writes before the atomic apply).
func TestBulk_AbortAtBulkConsentAppliesNothing(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence,
			meetingEvidenceAt("cal2", time.Date(2026, 10, 6, 13, 0, 0, 0, loc), time.Date(2026, 10, 6, 14, 0, 0, 0, loc)))
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// leave the meeting unconfirmed (3); bulk-confirm (1) scope all (0); then ABORT
	// at the bulk consent (the confirm queue runs out after the missing-work answer).
	p := &fakePrompter{choose: []int{3, 1, 0}, confirms: []bool{false}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code == exitOK {
		t.Fatalf("an abort at the bulk consent must not succeed:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 0 {
		t.Fatalf("an abort must apply nothing, got %+v", rec.Confirmed)
	}
	if rec.Review != nil && rec.Review.Reviewed {
		t.Fatalf("an abort must not mark the day reviewed")
	}
}

// TestBulk_DeclineFinalConsentAfterBulkAppliesNothing proves a declined final
// confirmation after a staged bulk action applies nothing.
func TestBulk_DeclineFinalConsentAfterBulkAppliesNothing(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence,
			meetingEvidenceAt("cal2", time.Date(2026, 10, 6, 13, 0, 0, 0, loc), time.Date(2026, 10, 6, 14, 0, 0, 0, loc)))
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// leave (3); bulk-confirm (1) scope all (0); classification (0); DECLINE final.
	p := &fakePrompter{choose: []int{3, 1, 0, 0}, confirms: []bool{false, true, false}}
	code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb)
	if code == exitOK {
		t.Fatalf("a declined final confirmation must not succeed:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 0 {
		t.Fatalf("a declined final confirmation must apply nothing, got %+v", rec.Confirmed)
	}
}

// TestBulk_PlainAndTUIParity proves the TUI and plain paths share the staging code
// through the prompter seam: the same scenario yields identical ReviewDecisions and
// an identical preview.
func TestBulk_PlainAndTUIParity(t *testing.T) {
	meeting := bulkMeeting("meeting_aaa111", "Proj", 9, 0, 10, 0, true)
	prop := proposal.Proposal{Meetings: []proposal.ProposedTimeBlock{meeting}}

	// TUI-equivalent (the fake prompter is 0-based).
	var tuiOut bytes.Buffer
	tui := &guided{p: &fakePrompter{choose: []int{1, 0}, confirms: []bool{true}}, stdout: &tuiOut, stderr: &bytes.Buffer{}}
	var tuiDec run.ReviewDecisions
	if err := tui.bulkReview(context.Background(), prop, nil, &tuiDec); err != nil {
		t.Fatalf("tui bulkReview: %v", err)
	}

	// Plain (1-based stdin: 2=Bulk-confirm, 1=All, y=consent).
	var plainOut bytes.Buffer
	plain := &guided{p: newPlainPrompter(strings.NewReader("2\n1\ny\n"), &plainOut), stdout: &plainOut, stderr: &bytes.Buffer{}}
	var plainDec run.ReviewDecisions
	if err := plain.bulkReview(context.Background(), prop, nil, &plainDec); err != nil {
		t.Fatalf("plain bulkReview: %v", err)
	}

	if !reflect.DeepEqual(tuiDec.Confirm, plainDec.Confirm) {
		t.Fatalf("staged decisions differ:\n tui=%+v\n plain=%+v", tuiDec.Confirm, plainDec.Confirm)
	}
	if len(tuiDec.Confirm) != 1 || tuiDec.Confirm[0].BlockID != meeting.ID {
		t.Fatalf("want the meeting staged once, got %+v", tuiDec.Confirm)
	}
	got, want := bulkPreviewSection(plainOut.String()), bulkPreviewSection(tuiOut.String())
	if got == "" || got != want {
		t.Fatalf("the preview must be identical:\n plain=%q\n tui=%q", got, want)
	}
}

// TestBulk_EmptyScopeStagesNothing proves an empty scope stages nothing and never
// falls through to mark-empty.
func TestBulk_EmptyScopeStagesNothing(t *testing.T) {
	work := bulkWork("work_bbb222", "", 11, 0, 11, 30)
	prop := proposal.Proposal{Work: []proposal.ProposedTimeBlock{work}}
	var out bytes.Buffer
	dec := run.ReviewDecisions{}
	// action=confirm (index 1), scope=explicit ids (index 3), a bogus id.
	g := &guided{p: &fakePrompter{choose: []int{1, 3}, inputs: []string{"bogus"}}, stdout: &out, stderr: &bytes.Buffer{}}
	if err := g.bulkReview(context.Background(), prop, nil, &dec); err != nil {
		t.Fatalf("bulkReview: %v", err)
	}
	if len(dec.Confirm) != 0 || len(dec.Exclude) != 0 || len(dec.Project) != 0 || dec.MarkEmpty {
		t.Fatalf("an empty scope must stage nothing, got %+v", dec)
	}
	if !strings.Contains(out.String(), "no blocks match this scope; nothing staged") {
		t.Fatalf("want the empty-scope message:\n%s", out.String())
	}
}

// TestBulk_EmptyScopeDoesNotMarkEmpty proves an empty-scope bulk step leaves an
// existing nonempty day non-empty (the empty-scope path never marks a day empty).
func TestBulk_EmptyScopeDoesNotMarkEmpty(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "X", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// keep existing (0); no reset; bulk-confirm (1) with an explicit bogus id (3) -> empty
	// scope; no missing work; confirm review.
	p := &fakePrompter{choose: []int{0, 1, 3}, inputs: []string{"bogus"}, confirms: []bool{false, false, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "no blocks match this scope; nothing staged") {
		t.Fatalf("want the empty-scope message:\n%s", out.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review != nil && rec.Review.Empty {
		t.Fatalf("an empty bulk scope must not mark the day empty")
	}
	if len(rec.Confirmed) != 1 {
		t.Fatalf("the existing confirmed work must be preserved, got %+v", rec.Confirmed)
	}
}

// --- bulk classification -----------------------------------------------------

// bulkClassificationRec is a day with one confirmed interval that carries no
// classification, so it is a classifiable target for the bulk action.
func bulkClassificationRec(t *testing.T) *workflow.Record {
	t.Helper()
	rec := readyRecord(testDate)
	rec.Confirmed[0].ClassificationID = ""
	rec.Confirmed[0].ActivityType = ""
	return &rec
}

// P4/C4: the bulk-classification action previews the EXACT affected interval and
// counts, and a consented pick stages ONE resolved ClassificationDecision.
func TestBulk_ClassificationPreviewAndStaging(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "Intern", Code: "INTGRADI2610", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	rec := bulkClassificationRec(t)

	var out bytes.Buffer
	// bulk-assign a classification (4); pick option 0; consent (true).
	g := &guided{cfg: cfg, stdout: &out, stderr: &bytes.Buffer{}, p: &fakePrompter{choose: []int{4, 0}, confirms: []bool{true}}}
	dec := run.ReviewDecisions{}
	if err := g.bulkReview(context.Background(), proposal.Proposal{}, rec, &dec); err != nil {
		t.Fatalf("bulkReview: %v", err)
	}
	if len(dec.Classification) != 1 {
		t.Fatalf("want exactly one staged classification, got %+v", dec.Classification)
	}
	got := dec.Classification[0]
	if got.BlockID != "c1" || got.ClassificationID != "cls-1" || got.ActivityType != "internal project" ||
		got.ClassificationCode != "INTGRADI2610" || got.ClassificationName != "Intern" {
		t.Fatalf("the staged decision must carry the resolved option: %+v", got)
	}
	s := out.String()
	if !strings.Contains(s, "assign-classification Intern (INTGRADI2610) [internal project]") {
		t.Fatalf("the preview must name the chosen classification:\n%s", s)
	}
	if !strings.Contains(s, "scope: all classifiable intervals (1 block(s))") || !strings.Contains(s, "applied 1") {
		t.Fatalf("the preview must show the exact affected count:\n%s", s)
	}
}

// P4: a declined consent stages nothing (staged-only model).
func TestBulk_ClassificationAbortStagesNothing(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "Intern", Code: "INTGRADI2610", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	rec := bulkClassificationRec(t)

	var out bytes.Buffer
	g := &guided{cfg: cfg, stdout: &out, stderr: &bytes.Buffer{}, p: &fakePrompter{choose: []int{4, 0}, confirms: []bool{false}}}
	dec := run.ReviewDecisions{}
	if err := g.bulkReview(context.Background(), proposal.Proposal{}, rec, &dec); err != nil {
		t.Fatalf("bulkReview: %v", err)
	}
	if len(dec.Classification) != 0 {
		t.Fatalf("a declined bulk classification must stage nothing, got %+v", dec.Classification)
	}
	if !strings.Contains(out.String(), "declined; nothing staged") {
		t.Fatalf("the decline must be disclosed:\n%s", out.String())
	}
}

// P4: an interval that already carries a classification is retained (skipped),
// never overwritten by the bulk action.
func TestBulk_ClassificationSkipsAlreadyClassified(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "Intern", Code: "INTGRADI2610", ActivityType: "internal project"})
	cfg := guidedTestConfig()
	rec := readyRecord(testDate) // c1 already carries cls-1
	rec.Confirmed[0].ClassificationID = "cls-existing"
	rec.Confirmed[0].ActivityType = "internal project"

	var out bytes.Buffer
	g := &guided{cfg: cfg, stdout: &out, stderr: &bytes.Buffer{}, p: &fakePrompter{choose: []int{4, 0}}}
	dec := run.ReviewDecisions{}
	if err := g.bulkReview(context.Background(), proposal.Proposal{}, &rec, &dec); err != nil {
		t.Fatalf("bulkReview: %v", err)
	}
	if len(dec.Classification) != 0 {
		t.Fatalf("an already-classified interval must be retained, got %+v", dec.Classification)
	}
	s := out.String()
	if !strings.Contains(s, "skipped already-classified 1") || !strings.Contains(s, "nothing to stage") {
		t.Fatalf("the retained interval must be surfaced and nothing staged:\n%s", s)
	}
}

// P4: a per-block classification decision always wins over the bulk add, in both
// orders (bulk staging is add-if-absent; per-block staging replaces).
func TestBulk_ClassificationPerBlockOverrideWinsBothOrders(t *testing.T) {
	opt := gradion.ClassificationOption{ID: "cls-bulk", Name: "Bulk", Code: "BULK-1", ActivityType: "internal project"}
	per := gradion.ClassificationOption{ID: "cls-per", Name: "Per", Code: "PER-1", ActivityType: "internal project"}
	target := classTarget{key: "c1", label: "existing interval c1"}

	// Per-block first, then bulk: the per-block choice must win.
	dec := run.ReviewDecisions{}
	addClassification(&dec, run.ClassificationDecision{BlockID: "c1", ClassificationID: per.ID})
	stageBulkClassification(&dec, opt, []classTarget{target})
	if len(dec.Classification) != 1 || dec.Classification[0].ClassificationID != per.ID {
		t.Fatalf("a per-block classification must win over a later bulk add, got %+v", dec.Classification)
	}

	// Bulk first, then per-block: the per-block replacement must win.
	dec2 := run.ReviewDecisions{}
	stageBulkClassification(&dec2, opt, []classTarget{target})
	addClassification(&dec2, run.ClassificationDecision{BlockID: "c1", ClassificationID: per.ID})
	if len(dec2.Classification) != 1 || dec2.Classification[0].ClassificationID != per.ID {
		t.Fatalf("a per-block classification must replace a prior bulk add, got %+v", dec2.Classification)
	}
}

// P4: the TUI and plain paths share the classification bulk staging through the
// prompter seam.
func TestBulk_ClassificationPlainAndTUIParity(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "Intern", Code: "INTGRADI2610", ActivityType: "internal project"})
	cfg := guidedTestConfig()

	tuiRec := bulkClassificationRec(t)
	var tuiOut bytes.Buffer
	tui := &guided{cfg: cfg, p: &fakePrompter{choose: []int{4, 0}, confirms: []bool{true}}, stdout: &tuiOut, stderr: &bytes.Buffer{}}
	var tuiDec run.ReviewDecisions
	if err := tui.bulkReview(context.Background(), proposal.Proposal{}, tuiRec, &tuiDec); err != nil {
		t.Fatalf("tui bulkReview: %v", err)
	}

	plainRec := bulkClassificationRec(t)
	var plainOut bytes.Buffer
	// 5 = bulk-assign a classification; 1 = the first catalog option; y = consent.
	plain := &guided{cfg: cfg, p: newPlainPrompter(strings.NewReader("5\n1\ny\n"), &plainOut), stdout: &plainOut, stderr: &bytes.Buffer{}}
	var plainDec run.ReviewDecisions
	if err := plain.bulkReview(context.Background(), proposal.Proposal{}, plainRec, &plainDec); err != nil {
		t.Fatalf("plain bulkReview: %v", err)
	}
	if !reflect.DeepEqual(tuiDec.Classification, plainDec.Classification) {
		t.Fatalf("staged classifications differ:\n tui=%+v\n plain=%+v", tuiDec.Classification, plainDec.Classification)
	}
	if len(tuiDec.Classification) != 1 {
		t.Fatalf("want one staged classification, got %+v", tuiDec.Classification)
	}
}

// P4/C6: a guided run that bulk-classifies an interval applies it end-to-end
// (re-resolved via AuthoritativeClassificationOption) and does not re-prompt for it.
func TestBulk_ClassificationGuidedIntegration(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	stubCatalog(t, gradion.ClassificationOption{ID: "cls-1", Name: "Intern", Code: "INTGRADI2610", ActivityType: "internal project"})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed[0].ClassificationID = ""
		rec.Confirmed[0].ActivityType = ""
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// keep existing (0); no reset; no missing work (false); bulk classification (4) pick 0,
	// consent true; final confirm true.
	p := &fakePrompter{choose: []int{0, 4, 0}, confirms: []bool{false, false, true, true}}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "cls-1" || rec.Confirmed[0].ActivityType != "internal project" {
		t.Fatalf("the bulk classification must be applied end-to-end: %+v", rec.Confirmed[0])
	}
	if !strings.Contains(out.String(), "classified by the bulk action") {
		t.Fatalf("the guided run must disclose the bulk-classified count:\n%s", out.String())
	}
}

// F1 regression: a guided Bulk-assign-classification whose pick is retrieved by a
// SEARCH beyond the unfiltered first page must COMPLETE — the widened validation
// catalog must survive classificationStep, so validateClassifications re-resolves
// the beyond-page id instead of aborting with ErrUnknownToken.
func TestBulk_ClassificationSearchBeyondPageValidates(t *testing.T) {
	t.Setenv("TIMESHEET_HOME", t.TempDir())
	fetch := &fakeCatalogFetcher{
		catalog: partialPage(),
		byQuery: map[string]gradion.Catalog{"intern": targetedIntern()},
	}
	withCatalogFetcher(t, fetch)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := guidedTestConfig()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed[0].ClassificationID = ""
		rec.Confirmed[0].ActivityType = ""
	})
	r, store := newGuidedRunner(t, storage, nil, cfg)

	var out, errb bytes.Buffer
	// keep existing (0); no reset; no missing work (false); bulk classification (4) -> search
	// (12) -> query "intern" -> pick the beyond-page option (0), consent true;
	// final confirm true.
	p := &fakePrompter{
		choose:   []int{0, 4, 12, 0},
		inputs:   []string{"intern"},
		confirms: []bool{false, false, true, true},
	}
	if code := cmdRunGuided(context.Background(), r, cfg, store, false, false, "", testDate, p, &out, &errb); code != exitOK {
		t.Fatalf("guided run: %d\n%s\n%s", code, out.String(), errb.String())
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "uuid-int" || rec.Confirmed[0].ActivityType != "internal project" {
		t.Fatalf("the beyond-page bulk choice must validate and apply: %+v", rec.Confirmed[0])
	}
}
