package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// OBSERVATION-SURFACE-1 tests the three surfaces of the collection-time remote
// signals (issues #48/#49/#43): an offline preview is UNOBSERVED, `review`
// surfaces a failed read / the day's observed entries / collisions, and the guided
// path surfaces the same three.

var obsFixed = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// obsRemote returns a runner with an injected remote whose listing is controlled
// by the test, plus its store.
func obsRunner(t *testing.T, remote run.Remote) (*run.Runner, workflow.Store) {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	r, store := newGuidedRunner(t, storage, remote, guidedTestConfig())
	return r, store
}

// #48: an OFFLINE preview prints the UNOBSERVED line and complete=false, with no
// local timestamp. Fail-before: it printed `remote observed: <now>` and
// `remote complete: true`.
func TestCLI_PreviewOfflineIsUnobserved(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{Classifications: map[string]string{"Proj": "cls-1"}})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })

	code, stdout, stderr := runArgs(t, "preview", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--offline")
	if code != exitOK {
		t.Fatalf("preview: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "remote observed: unobserved (offline; no remote was contacted)") {
		t.Errorf("offline preview must report UNOBSERVED:\n%s", stdout)
	}
	if !strings.Contains(stdout, "remote complete: false") {
		t.Errorf("offline preview must report complete=false:\n%s", stdout)
	}
	// No timestamp may stand in for the remote observation.
	if line := valueAfter(stdout, "remote observed:"); strings.ContainsAny(line, "0123456789") {
		t.Errorf("offline preview must not print a timestamp, got %q", line)
	}
}

// #48 online: an ONLINE preview prints the observed timestamp and complete=true.
func TestCLI_PreviewOnlineReportsObserved(t *testing.T) {
	remote := &submitFakeRemote{listing: &gradion.Listing{Observed: true, Complete: true, ObservedAt: obsFixed}}
	r, store := obsRunner(t, remote)

	var out, errb bytes.Buffer
	if code := cmdPreview(context.Background(), store, r, guidedTestConfig(), testDate, &out, &errb); code != exitOK {
		t.Fatalf("preview: %d (%s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "remote observed: "+obsFixed.Format(time.RFC3339)) {
		t.Errorf("online preview must print the observed timestamp:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "remote complete: true") {
		t.Errorf("online preview must print complete=true:\n%s", out.String())
	}
	if strings.Contains(out.String(), "unobserved") {
		t.Errorf("an online preview must not claim to be unobserved:\n%s", out.String())
	}
}

// #49(a): `review` prints the observation-failure warning when the collection
// remote read fails.
func TestReview_SurfacesRemoteObservationFailure(t *testing.T) {
	remote := &submitFakeRemote{listErr: errors.New("boom: read failed")}
	r, _ := obsRunner(t, remote)

	var out, errb bytes.Buffer
	if code := cmdReview(context.Background(), r, guidedTestConfig(), false, "", "", testDate, "", "", &out, &errb); code != exitOK {
		t.Fatalf("review: %d (%s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "remote observation failed: boom: read failed") {
		t.Errorf("review must surface the failed remote read:\n%s", out.String())
	}
}

// #49: a successful read with NO entries and NO collisions adds NOTHING extra.
func TestReview_PrintsNothingExtraOnCleanRead(t *testing.T) {
	remote := &submitFakeRemote{listing: &gradion.Listing{Observed: true, Complete: true, ObservedAt: obsFixed}}
	r, _ := obsRunner(t, remote)

	var out, errb bytes.Buffer
	if code := cmdReview(context.Background(), r, guidedTestConfig(), false, "", "", testDate, "", "", &out, &errb); code != exitOK {
		t.Fatalf("review: %d (%s)", code, errb.String())
	}
	for _, unwanted := range []string{"remote observation failed:", "remote collision:", "Remote entries ("} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("a clean read must not print %q:\n%s", unwanted, out.String())
		}
	}
}

// #43/#49(b,c): `review` enumerates the day's OBSERVED remote entries with their
// ids and prints a non-blocking collision warning.
func TestReview_EnumeratesEntriesAndSurfacesCollision(t *testing.T) {
	// r1 spans exactly the seeded c1 (09:00-10:00) slot, so it is a collision too.
	entry := workflow.RemoteState{
		RemoteID: "r1", Date: testDate, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "already on the remote",
	}
	remote := &submitFakeRemote{listing: &gradion.Listing{Observed: true, Complete: true, ObservedAt: obsFixed, Entries: []workflow.RemoteState{entry}}}
	r, _ := obsRunner(t, remote)

	var out, errb bytes.Buffer
	if code := cmdReview(context.Background(), r, guidedTestConfig(), false, "", "", testDate, "", "", &out, &errb); code != exitOK {
		t.Fatalf("review: %d (%s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "Remote entries ("+testDate+"):") {
		t.Errorf("review must enumerate the day's observed entries:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "  r1  09:00-10:00  already on the remote") {
		t.Errorf("the entry must print its id, span and description:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "remote collision: r1") {
		t.Errorf("review must surface the collision ids:\n%s", out.String())
	}
}

// F1: an OBSERVED but INCOMPLETE read must not read as a complete enumeration:
// the header is labelled PARTIAL, never the bare complete header.
func TestReview_PartialReadLabelsTheEnumeration(t *testing.T) {
	entry := workflow.RemoteState{RemoteID: "r1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Description: "partial"}
	remote := &submitFakeRemote{listing: &gradion.Listing{Observed: true, Complete: false, ObservedAt: obsFixed, Entries: []workflow.RemoteState{entry}}}
	r, _ := obsRunner(t, remote)

	var out, errb bytes.Buffer
	if code := cmdReview(context.Background(), r, guidedTestConfig(), false, "", "", testDate, "", "", &out, &errb); code != exitOK {
		t.Fatalf("review: %d (%s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "Remote entries ("+testDate+") [PARTIAL - the read did not complete; this list may be incomplete]:") {
		t.Errorf("a partial read must label the enumeration PARTIAL:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Remote entries ("+testDate+"):") {
		t.Errorf("a partial read must not print the bare complete header:\n%s", out.String())
	}
}

// #49(b): an UNOBSERVED (offline) review prints no entries section.
func TestReview_UnobservedPrintsNoEntriesSection(t *testing.T) {
	r, _ := obsRunner(t, nil) // nil remote => offline

	var out, errb bytes.Buffer
	if code := cmdReview(context.Background(), r, guidedTestConfig(), false, "", "", testDate, "", "", &out, &errb); code != exitOK {
		t.Fatalf("review: %d (%s)", code, errb.String())
	}
	if strings.Contains(out.String(), "Remote entries (") {
		t.Errorf("an unobserved review must print no entries section:\n%s", out.String())
	}
}

// The GUIDED path surfaces the same three signals in its review section.
func TestGuidedPrintCollection_SurfacesRemoteSignals(t *testing.T) {
	var buf bytes.Buffer
	g := &guided{stdout: &buf, date: testDate}
	cr := run.CollectResult{
		Date:                   testDate,
		RemoteObservationError: "boom: read failed",
		RemoteObserved:         true,
		RemoteComplete:         true,
		RemoteEntries: []workflow.RemoteState{{
			RemoteID: "r1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Description: "remote work",
		}},
		RemoteCollisions: []string{"r1", "r2"},
	}
	g.printCollection(cr, &workflow.Record{})
	out := buf.String()
	for _, want := range []string{
		"remote observation failed: boom: read failed",
		"Remote entries (" + testDate + "):",
		"  r1  09:00-10:00  remote work",
		"remote collision: r1, r2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("guided review section missing %q:\n%s", want, out)
		}
	}
}

// A complete source carrying a warning must never read as a clean zero in the
// guided Sources section, and on an ordinary day it must not swallow the evidence
// count: both the warning line and "all enabled sources complete" are printed.
func TestGuidedPrintCollection_CompleteWarningIsVisible(t *testing.T) {
	var buf bytes.Buffer
	g := &guided{stdout: &buf, date: testDate}
	cr := run.CollectResult{
		Date: testDate,
		Summary: evidence.CollectionSummary{
			Date: testDate,
			Sources: []evidence.SourceResult{{
				SourceID: "journal-work",
				Status:   evidence.StatusComplete,
				Warnings: []string{plugins.JournalWarnDateUnstated},
			}},
			Complete: true,
		},
		Evidence: []evidence.Evidence{{ID: "e1"}},
	}
	g.printCollection(cr, &workflow.Record{})
	out := buf.String()
	if !strings.Contains(out, "source journal-work: complete (JOURNAL_DATE_UNSTATED)") {
		t.Errorf("a complete source warning must be printed:\n%s", out)
	}
	if !strings.Contains(out, "all enabled sources complete (1 evidence item(s))") {
		t.Errorf("a warning day with evidence must keep the count line:\n%s", out)
	}
	if strings.Contains(out, "no evidence collected") {
		t.Errorf("an uncertified note must not read as a clean zero:\n%s", out)
	}
}

// The fresh export-only shape is a COMPLETE source with zero evidence and a
// warning: it prints the warning, never "no evidence collected", and no count line
// (there is no evidence to count).
func TestGuidedPrintCollection_UncertifiedZeroIsNotACleanZero(t *testing.T) {
	var buf bytes.Buffer
	g := &guided{stdout: &buf, date: testDate}
	cr := run.CollectResult{
		Date: testDate,
		Summary: evidence.CollectionSummary{
			Date: testDate,
			Sources: []evidence.SourceResult{{
				SourceID: "journal-work",
				Status:   evidence.StatusComplete,
				Warnings: []string{plugins.JournalWarnDateUnstated},
			}},
			Complete: true,
		},
	}
	g.printCollection(cr, &workflow.Record{})
	out := buf.String()
	if !strings.Contains(out, "source journal-work: complete (JOURNAL_DATE_UNSTATED)") {
		t.Errorf("a complete source warning must be printed:\n%s", out)
	}
	if strings.Contains(out, "no evidence collected") || strings.Contains(out, "all enabled sources complete") {
		t.Errorf("an uncertified zero must print only its warning:\n%s", out)
	}
}

// A clean complete day prints exactly one count line (no duplication).
func TestGuidedPrintCollection_CleanCompletePrintsCountOnce(t *testing.T) {
	var buf bytes.Buffer
	g := &guided{stdout: &buf, date: testDate}
	cr := run.CollectResult{
		Date: testDate,
		Summary: evidence.CollectionSummary{
			Date: testDate,
			Sources: []evidence.SourceResult{{
				SourceID: "journal-work",
				Status:   evidence.StatusComplete,
			}},
			Complete: true,
		},
		Evidence: []evidence.Evidence{{ID: "e1"}, {ID: "e2"}},
	}
	g.printCollection(cr, &workflow.Record{})
	out := buf.String()
	if got := strings.Count(out, "all enabled sources complete (2 evidence item(s))"); got != 1 {
		t.Fatalf("want exactly one count line, got %d:\n%s", got, out)
	}
	if strings.Contains(out, "JOURNAL_") {
		t.Errorf("a clean day must print no source warning:\n%s", out)
	}
}

// The GUIDED path prints nothing extra for a clean, empty observed read.
func TestGuidedPrintCollection_CleanReadIsQuiet(t *testing.T) {
	var buf bytes.Buffer
	g := &guided{stdout: &buf, date: testDate}
	g.printCollection(run.CollectResult{Date: testDate, RemoteObserved: true}, &workflow.Record{})
	out := buf.String()
	for _, unwanted := range []string{"remote observation failed:", "remote collision:", "Remote entries ("} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a clean guided read must not print %q:\n%s", unwanted, out)
		}
	}
}

// The GUIDED preview reports UNOBSERVED offline, like the scripted preview.
func TestGuidedPrintPlan_OfflineIsUnobserved(t *testing.T) {
	storage := filepath.Join(t.TempDir(), "records")
	cfg := guidedTestConfig()
	r, store := newGuidedRunner(t, storage, nil, cfg)
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })
	pre, err := r.Preview(context.Background(), testDate, classificationResolver(cfg))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	var buf bytes.Buffer
	g := &guided{r: r, cfg: cfg, store: store, stdout: &buf, date: testDate}
	g.printPlan(pre)
	if !strings.Contains(buf.String(), "remote observed: unobserved (offline; no remote was contacted)") {
		t.Errorf("the guided preview must report UNOBSERVED offline:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "remote complete: false") {
		t.Errorf("the guided preview must report complete=false offline:\n%s", buf.String())
	}
}
