package run

import (
	"context"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

var ctx = context.Background()

func fixedNow() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }

func newStore(t *testing.T) *workflow.JSONFileStore {
	t.Helper()
	store, err := workflow.NewJSONFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	return store
}

func clsResolver(string) (string, bool) { return "cls-1", true }

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

func hasCheck(checks []Check, name, code string) bool {
	for _, c := range checks {
		if c.Name == name && c.Code == code && !c.Passed {
			return true
		}
	}
	return false
}

// --- fake plugin ------------------------------------------------------------

type fakePlugin struct {
	kind     evidence.SourceKind
	required []evidence.ConfigRequirement
	ev       []evidence.Evidence
	err      error

	liveCalls     int
	contractCalls int
}

func (p *fakePlugin) Type() evidence.SourceKind { return p.kind }
func (p *fakePlugin) RequiredConfig() []evidence.ConfigRequirement {
	return p.required
}
func (p *fakePlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{p.kind}
}
func (p *fakePlugin) CheckContract(ctx context.Context, cfg evidence.SourceConfig) []evidence.ContractCheck {
	return nil
}
func (p *fakePlugin) Collect(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (evidence.CollectResult, error) {
	if req.IsContract() {
		p.contractCalls++
	} else {
		p.liveCalls++
	}
	if p.err != nil {
		return evidence.CollectResult{}, p.err
	}
	return evidence.CollectResult{
		Evidence: p.ev,
		Result:   evidence.SourceResult{Status: evidence.StatusComplete},
	}, nil
}

// --- fake keychain ----------------------------------------------------------

type fakeKeychain struct {
	values map[string]string
	err    error
}

func (f *fakeKeychain) Lookup(ctx context.Context, service, account string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if v, ok := f.values[service]; ok {
		return v, nil
	}
	return "", &keychain.OperatingError{Outcome: keychain.OutcomeNotFound, Code: "KEYCHAIN_ERR_NOT_FOUND", Message: "absent"}
}

// --- fake remote ------------------------------------------------------------

type fakeRemote struct {
	entries     map[string]workflow.RemoteState
	seq         int
	complete    bool
	initCalls   int
	verifyCalls int
	listCalls   int
	creates     int
	edits       int
	initErr     error
	verifyErr   error
	onCreate    func(gradion.CreateRequest) (gradion.WriteResult, error)

	// catalogByQuery, when set, supplies ClassificationCatalog results for
	// targeted validation. catalogCalls records every query it received.
	catalogByQuery func(query string) (gradion.Catalog, error)
	catalogCalls   []string
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{entries: map[string]workflow.RemoteState{}, complete: true}
}

func (f *fakeRemote) Initialize(ctx context.Context) (mcp.ServerInfo, error) {
	f.initCalls++
	if f.initErr != nil {
		return mcp.ServerInfo{}, f.initErr
	}
	return mcp.ServerInfo{}, nil
}

func (f *fakeRemote) VerifyCapabilities(ctx context.Context) error {
	f.verifyCalls++
	return f.verifyErr
}

func (f *fakeRemote) Classifications(ctx context.Context, query string) ([]gradion.Classification, error) {
	return nil, nil
}

func (f *fakeRemote) ClassificationCatalog(ctx context.Context, query string) (gradion.Catalog, error) {
	f.catalogCalls = append(f.catalogCalls, query)
	if f.catalogByQuery != nil {
		return f.catalogByQuery(query)
	}
	return gradion.Catalog{Complete: true, Query: query, ObservedAt: fixedNow(), Source: "live"}, nil
}

func (f *fakeRemote) ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (gradion.Listing, error) {
	f.listCalls++
	entries := make([]workflow.RemoteState, 0, len(f.entries))
	for _, e := range f.entries {
		e.ContentObserved = true
		entries = append(entries, e)
	}
	return gradion.Listing{Observed: true, Complete: f.complete, ObservedAt: fixedNow(), Entries: entries}, nil
}

// EntryByID is the bounded stable-ID read-back used to verify an
// identity-acknowledged write.
func (f *fakeRemote) EntryByID(ctx context.Context, id, from, to string) (workflow.RemoteState, bool, error) {
	if !f.complete {
		return workflow.RemoteState{}, false, &gradion.Error{Code: gradion.CodeListingIncomplete, Message: "listing incomplete"}
	}
	e, ok := f.entries[id]
	if !ok {
		return workflow.RemoteState{}, false, nil
	}
	e.ContentObserved = true
	return e, true, nil
}

func (f *fakeRemote) CreateEntry(ctx context.Context, req gradion.CreateRequest) (gradion.WriteResult, error) {
	f.creates++
	if f.onCreate != nil {
		return f.onCreate(req)
	}
	f.seq++
	state := workflow.RemoteState{
		RemoteID: "new-" + itoa(f.seq), Date: req.Date, StartTime: req.StartTime,
		EndTime: req.EndTime, ClassificationID: req.ClassificationID,
		Description: req.Description, ObservedAt: fixedNow(),
		ContentObserved: true,
	}
	f.entries[state.RemoteID] = state
	return gradion.WriteResult{Status: gradion.WriteConfirmed, Entry: &state}, nil
}

func (f *fakeRemote) EditEntry(ctx context.Context, req gradion.EditRequest) (gradion.WriteResult, error) {
	f.edits++
	state := workflow.RemoteState{
		RemoteID: req.EntryID, Date: req.Date, StartTime: req.StartTime,
		EndTime: req.EndTime, ClassificationID: req.ClassificationID,
		Description: req.Description, ObservedAt: fixedNow(),
		ContentObserved: true,
	}
	f.entries[state.RemoteID] = state
	return gradion.WriteResult{Status: gradion.WriteConfirmed, Entry: &state}, nil
}

// --- fake editor ------------------------------------------------------------

type fakeEditor struct {
	calls int
	usage editorial.Usage
}

func (f *fakeEditor) Synthesize(ctx context.Context, req editorial.Request) (editorial.Response, editorial.Usage, error) {
	f.calls++
	m := make(map[string]editorial.Text, len(req.Targets))
	for _, t := range req.Targets {
		m[t.ID] = editorial.Text{Title: "AI title", Description: "AI description"}
	}
	return editorial.Response{Suggestions: m}, f.usage, nil
}

// --- fake exporter ----------------------------------------------------------

type fakeExporter struct {
	calls int
	err   error
	last  export.Note
}

func (f *fakeExporter) Export(ctx context.Context, note export.Note) (export.Result, error) {
	f.calls++
	f.last = note
	if f.err != nil {
		return export.Result{}, f.err
	}
	return export.Result{Status: note.Status, Written: true}, nil
}

// --- helpers ----------------------------------------------------------------

func newRunner(t *testing.T, deps Deps, cfg Config) *Runner {
	t.Helper()
	if deps.Now == nil {
		deps.Now = fixedNow
	}
	if deps.GitToken == "" {
		deps.GitToken = "test-git-token"
	}
	r, err := New(deps, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func gitEvidence(id, repo, title string) evidence.Evidence {
	start := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceLocalGit, Title: title,
		Start: start, End: start.Add(30 * time.Minute), Duration: evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: repo, Branch: "feat/x", At: start}},
		ObservedAt: start,
	}
}

func meetingEvidence(id, title string) evidence.Evidence {
	start := time.Date(2026, 10, 6, 11, 0, 0, 0, time.UTC)
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceCalendar, Title: title,
		Start: start, End: start.Add(30 * time.Minute), Duration: evidence.KnownDuration(30),
		Invitation: evidence.InvitationAccepted, Status: evidence.EventStatusConfirmed,
		ObservedAt: start,
	}
}

func seedEvidence(t *testing.T, store workflow.Store, date string, ev []evidence.Evidence) {
	t.Helper()
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.CollectedEvidence = append(rec.CollectedEvidence, ev...)
		return nil
	}); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
}

func readyRecord(date string) workflow.Record {
	return workflow.Record{
		SchemaVersion: workflow.SchemaVersion,
		Date:          date,
		Confirmed: []workflow.ConfirmedInterval{{
			ID: "c1", Date: date, StartTime: "09:00", EndTime: "10:00",
			Project: "Proj", Description: "work", Attendance: true, ClassificationID: "cls-1",
			ClassificationCode: "CODE-cls-1", ClassificationName: "Class cls-1", ActivityType: "internal project",
		}},
		Review: &workflow.ReviewState{Reviewed: true},
	}
}

// seedApproved writes a ready record, previews a plan, and approves it.
func seedApproved(t *testing.T, store workflow.Store, r *Runner, date string) workflow.Approval {
	t.Helper()
	rec := readyRecord(date)
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	pre, err := r.Preview(ctx, date, clsResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pre.Blocked {
		t.Fatalf("preview blocked: %v", pre.BlockReasons)
	}
	approval, err := r.Approve(ctx, date, pre.Plan)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return approval
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
