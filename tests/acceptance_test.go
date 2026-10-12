// Package tests' acceptance harness (S9) drives the SHARED CORE (internal/run and
// its collaborators) end-to-end with fakes: a fake MCP Remote, a fake editorial
// Editor, a temp workflow.Store, and a temp vault. It is fully HERMETIC: it never
// contacts a live service, the Keychain, or the network.
//
// EVERY scenario is exercised THROUGH internal/run (no bypass): the harness seeds
// evidence/records, then calls the core's Collect/Propose/ApplyReview/Preview/
// Approve/Submit/Export/Resolve methods. Assertions are made on the DURABLE
// record / outcome / mapping state and on the fake remote's call counts, not only
// on returned values.
//
// Each test is named for traceability to the redesign's 10 required scenarios plus
// the fault-injection cases.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

var accCtx = context.Background()

const accDate = "2026-10-06"

var accNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// --- fakes ------------------------------------------------------------------

// fakeRemote is a hermetic run.Remote: an in-memory entry store with call counts.
type fakeRemote struct {
	mu       sync.Mutex
	entries  map[string]workflow.RemoteState
	seq      int
	complete bool

	initErr   error
	verifyErr error
	listErr   error

	initCalls   int
	verifyCalls int
	listCalls   int
	createCalls int
	editCalls   int

	onCreate func(gradion.CreateRequest) (gradion.WriteResult, error)
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{entries: map[string]workflow.RemoteState{}, complete: true}
}

func (f *fakeRemote) setEntry(state workflow.RemoteState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[state.RemoteID] = state
}

func (f *fakeRemote) Initialize(ctx context.Context) (mcp.ServerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initCalls++
	return mcp.ServerInfo{}, f.initErr
}

func (f *fakeRemote) VerifyCapabilities(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifyCalls++
	return f.verifyErr
}

func (f *fakeRemote) Classifications(ctx context.Context, query string) ([]gradion.Classification, error) {
	return nil, nil
}

func (f *fakeRemote) ClassificationCatalog(ctx context.Context, query string) (gradion.Catalog, error) {
	// A live, complete catalog carrying the FULL row for the classification the
	// ready record uses, so a TARGETED read by the recorded code resolves it.
	return gradion.Catalog{Complete: true, Query: query, ObservedAt: accNow, Source: "live",
		Options: []gradion.ClassificationOption{accClsOpt()}}, nil
}

// accClsOpt is the fully-populated catalog row matching accReadyRecord's stored
// retrieval metadata. A row missing any of id/name/code/activity type fails the
// submit proof closed.
func accClsOpt() gradion.ClassificationOption {
	return gradion.ClassificationOption{ID: "cls-1", Name: "Gradion Intern Academy 2026", Code: "INTGRADI2610", ActivityType: "internal project"}
}

func (f *fakeRemote) ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (gradion.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return gradion.Listing{}, f.listErr
	}
	entries := make([]workflow.RemoteState, 0, len(f.entries))
	for _, e := range f.entries {
		entries = append(entries, e)
	}
	return gradion.Listing{Complete: f.complete, ObservedAt: accNow, Entries: entries}, nil
}

func (f *fakeRemote) CreateEntry(ctx context.Context, req gradion.CreateRequest) (gradion.WriteResult, error) {
	f.mu.Lock()
	f.createCalls++
	override := f.onCreate
	if override == nil {
		f.seq++
		state := workflow.RemoteState{
			RemoteID: "new-" + itoa(f.seq), Date: req.Date, StartTime: req.StartTime,
			EndTime: req.EndTime, ClassificationID: req.ClassificationID,
			Description: req.Description, ObservedAt: accNow,
		}
		f.entries[state.RemoteID] = state
		f.mu.Unlock()
		return gradion.WriteResult{Status: gradion.WriteConfirmed, Entry: &state}, nil
	}
	f.mu.Unlock()
	return override(req)
}

func (f *fakeRemote) EditEntry(ctx context.Context, req gradion.EditRequest) (gradion.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editCalls++
	state := workflow.RemoteState{
		RemoteID: req.EntryID, Date: req.Date, StartTime: req.StartTime,
		EndTime: req.EndTime, ClassificationID: req.ClassificationID,
		Description: req.Description, ObservedAt: accNow,
	}
	f.entries[req.EntryID] = state
	return gradion.WriteResult{Status: gradion.WriteConfirmed, Entry: &state}, nil
}

func (f *fakeRemote) writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls + f.editCalls
}

// fakeEditor is a hermetic editorial.Editor.
type fakeEditor struct {
	mu    sync.Mutex
	calls int
	usage editorial.Usage
}

func (f *fakeEditor) Synthesize(ctx context.Context, req editorial.Request) (editorial.Response, editorial.Usage, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	m := make(map[string]editorial.Text, len(req.Targets))
	for _, t := range req.Targets {
		m[t.ID] = editorial.Text{Title: "AI title", Description: "AI description"}
	}
	return editorial.Response{Suggestions: m}, f.usage, nil
}

func (f *fakeEditor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakePlugin is a configurable evidence.Plugin.
type fakePlugin struct {
	kind     evidence.SourceKind
	required []evidence.ConfigRequirement
	ev       []evidence.Evidence
	status   evidence.SourceStatus
	live     int
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
	if !req.IsContract() {
		p.live++
	}
	status := p.status
	if status == "" {
		status = evidence.StatusComplete
	}
	return evidence.CollectResult{Evidence: p.ev, Result: evidence.SourceResult{Status: status}}, nil
}

// secretRefPlugin declares a SECRET config key, so a raw secret in it is rejected
// by the contract check (and the check names only the key).
type secretRefPlugin struct{ fakePlugin }

func (p *secretRefPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{{Key: "url_ref", Secret: true, Required: true}}
}

// misdeclaringPlugin emits an evidence kind it did not declare, i.e. malformed
// plugin output that must fail the contract check.
type misdeclaringPlugin struct{ fakePlugin }

func (p *misdeclaringPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{evidence.SourceCalendar}
}

// accTransport is a hermetic mcp.Transport whose tools/call response ECHOES the
// bearer token in the error body (the realistic leak attempt). The MCP layer must
// redact it.
type accTransport struct{ token string }

func (t *accTransport) Do(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req struct {
		ID     *int64 `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &req)
	if req.ID == nil {
		return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	switch req.Method {
	case "initialize":
		return accRPCResp(*req.ID, map[string]any{"protocolVersion": mcp.ProtocolVersion, "serverInfo": map[string]any{"name": "x", "version": "1"}}), nil
	case "tools/list":
		decls := []map[string]any{}
		for _, n := range []string{gradion.ToolListEntries, gradion.ToolLogTime, gradion.ToolEditTime, gradion.ToolGetClassifications} {
			decls = append(decls, map[string]any{"name": n})
		}
		return accRPCResp(*req.ID, map[string]any{"tools": decls}), nil
	default:
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("server error: token=" + t.token)),
		}, nil
	}
}

func accRPCResp(id int64, result any) *http.Response {
	rb, _ := json.Marshal(result)
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": json.RawMessage(rb)})
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(payload))}
}

// --- helpers ----------------------------------------------------------------
func accStore(t *testing.T) *workflow.JSONFileStore {
	t.Helper()
	return accStoreAt(t, t.TempDir())
}

// accStoreAt opens a (possibly fresh) store handle on dir, so a reload can be
// proven with a genuinely new handle.
func accStoreAt(t *testing.T, dir string) *workflow.JSONFileStore {
	t.Helper()
	store, err := workflow.NewJSONFileStore(dir)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	return store
}

func accRegistry(plugins ...evidence.Plugin) *evidence.Registry {
	reg := evidence.NewRegistry()
	for _, p := range plugins {
		reg.Register(p)
	}
	return reg
}

func accRunner(t *testing.T, deps run.Deps, cfg run.Config) *run.Runner {
	t.Helper()
	if deps.Store == nil {
		deps.Store = accStore(t)
	}
	if deps.Registry == nil {
		deps.Registry = evidence.NewRegistry()
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return accNow }
	}
	r, err := run.New(deps, cfg)
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	return r
}

func accSeed(t *testing.T, store workflow.Store, date string, mutate func(*workflow.Record)) {
	t.Helper()
	if _, err := store.UpdateDay(accCtx, date, func(rec *workflow.Record) error {
		if mutate != nil {
			mutate(rec)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed %s: %v", date, err)
	}
}

func accLoad(t *testing.T, store workflow.Store, date string) workflow.Record {
	t.Helper()
	rec, err := store.GetDay(accCtx, date)
	if err != nil {
		t.Fatalf("GetDay %s: %v", date, err)
	}
	return *rec
}

func accResolver(string) (string, bool) { return "cls-1", true }

// accCatalog is a FRESH, FULL live catalog carrying the classification the ready
// record uses. Every submission must supply a catalog: a nil one fails closed.
func accCatalog() *gradion.Catalog {
	return &gradion.Catalog{
		Complete: true, Source: "live", ObservedAt: accNow,
		Options: []gradion.ClassificationOption{accClsOpt()},
	}
}

func accGit(id, repo, branch, title string, start time.Time) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceLocalGit, Title: title,
		Start: start, End: start.Add(30 * time.Minute), Duration: evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: repo, Branch: branch, At: start}},
		ObservedAt: start,
	}
}

func accMeeting(id, title string, start, end time.Time) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceCalendar, Title: title,
		Start: start, End: end, Duration: evidence.KnownDuration(int(end.Sub(start) / time.Minute)),
		Invitation: evidence.InvitationAccepted, Status: evidence.EventStatusConfirmed,
		ObservedAt: start,
	}
}

// accReadyRecord is a reviewed day with one classified interval.
func accReadyRecord(date string) workflow.Record {
	return workflow.Record{
		SchemaVersion: workflow.SchemaVersion,
		Date:          date,
		Confirmed: []workflow.ConfirmedInterval{{
			ID: "c1", Date: date, StartTime: "09:00", EndTime: "10:00",
			Project: "Proj", Description: "work", Attendance: true, ClassificationID: "cls-1",
			ClassificationCode: "INTGRADI2610", ClassificationName: "Gradion Intern Academy 2026", ActivityType: "internal project",
		}},
		Review: &workflow.ReviewState{Reviewed: true},
	}
}

// accPlanFor previews and approves a plan for a ready day.
func accPlanFor(t *testing.T, store workflow.Store, r *run.Runner, date string) workflow.Approval {
	t.Helper()
	pre, err := r.Preview(accCtx, date, accResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pre.Blocked {
		t.Fatalf("unexpected preview block: %v", pre.BlockReasons)
	}
	approval, err := r.Approve(accCtx, date, pre.Plan)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return approval
}

func accJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
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

// accMinutes parses an "HH:MM" clock string into minutes.
func accMinutes(hhmm string) int {
	parts := strings.Split(strings.TrimSpace(hhmm), ":")
	if len(parts) != 2 {
		return -1
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return -1
	}
	return h*60 + m
}

// --- Scenario 1: duplicate collection ---------------------------------------

func TestAcceptance_01_DuplicateCollection(t *testing.T) {
	store := accStore(t)
	plug := &fakePlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{
		accGit("e1", "acme/repo", "feat/x", "fix parser", time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)),
		accGit("e2", "acme/repo", "feat/x", "tidy up", time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)),
	}}
	cfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "git", Type: evidence.SourceLocalGit, Enabled: true},
	}}}
	r := accRunner(t, run.Deps{Store: store, Registry: accRegistry(plug)}, cfg)

	first, err := r.Collect(accCtx, accDate)
	if err != nil {
		t.Fatalf("Collect 1: %v", err)
	}
	rec1 := accLoad(t, store, accDate)
	prop1, err := r.Propose(accCtx, accDate)
	if err != nil {
		t.Fatalf("Propose 1: %v", err)
	}

	second, err := r.Collect(accCtx, accDate)
	if err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	rec2 := accLoad(t, store, accDate)
	prop2, err := r.Propose(accCtx, accDate)
	if err != nil {
		t.Fatalf("Propose 2: %v", err)
	}

	// Durable evidence is upserted, never duplicated.
	if len(rec1.CollectedEvidence) != 2 || len(rec2.CollectedEvidence) != 2 {
		t.Fatalf("a repeated collection must not duplicate evidence: %d then %d",
			len(rec1.CollectedEvidence), len(rec2.CollectedEvidence))
	}
	if plug.live != 2 {
		t.Fatalf("want two live collections, got %d", plug.live)
	}
	if len(first.Evidence) != 2 || len(second.Evidence) != 2 {
		t.Fatalf("each collection returns the source's 2 items")
	}

	// Candidates are stable: identical topic and block IDs across passes.
	if len(prop1.Topics) != 1 || len(prop2.Topics) != 1 {
		t.Fatalf("want exactly one topic, got %d then %d", len(prop1.Topics), len(prop2.Topics))
	}
	if prop1.Topics[0].ID != prop2.Topics[0].ID {
		t.Fatalf("topic IDs must be stable: %s vs %s", prop1.Topics[0].ID, prop2.Topics[0].ID)
	}
	if len(prop1.Work) != len(prop2.Work) {
		t.Fatalf("work block count must be stable")
	}
	for i := range prop1.Work {
		if prop1.Work[i].ID != prop2.Work[i].ID {
			t.Fatalf("work block IDs must be stable: %s vs %s", prop1.Work[i].ID, prop2.Work[i].ID)
		}
	}
}

// --- Scenario 2: preserve confirmed edits -----------------------------------

func TestAcceptance_02_PreserveConfirmedEdits(t *testing.T) {
	dir := t.TempDir()
	store := accStoreAt(t, dir)
	plug := &fakePlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{
		accGit("e1", "acme/repo", "feat/x", "fix parser", time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)),
	}}
	cfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "git", Type: evidence.SourceLocalGit, Enabled: true},
	}}}
	r := accRunner(t, run.Deps{Store: store, Registry: accRegistry(plug)}, cfg)

	if _, err := r.Collect(accCtx, accDate); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	prop, err := r.Propose(accCtx, accDate)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work) == 0 {
		t.Fatalf("want a work block, got %+v", prop)
	}
	blockID := prop.Work[0].ID

	// The user confirms with a custom project and description.
	decisions := run.ReviewDecisions{Confirm: []run.ConfirmDecision{{BlockID: blockID, Project: "Proj", Attendance: boolPtr(true)}}}
	if err := r.ApplyReview(accCtx, accDate, decisions); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	// Edit the description to a user-authored value.
	if _, err := store.UpdateDay(accCtx, accDate, func(rec *workflow.Record) error {
		rec.Confirmed[0].Description = "USER TEXT"
		return nil
	}); err != nil {
		t.Fatalf("edit: %v", err)
	}

	// A refresh (re-collection) must preserve the confirmed interval + its text.
	if _, err := r.Collect(accCtx, accDate); err != nil {
		t.Fatalf("refresh collect: %v", err)
	}
	rec := accLoad(t, store, accDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want 1 confirmed interval after refresh, got %d", len(rec.Confirmed))
	}
	if rec.Confirmed[0].Description != "USER TEXT" || rec.Confirmed[0].Project != "Proj" {
		t.Fatalf("refresh must preserve user decisions, got %+v", rec.Confirmed[0])
	}

	// A genuinely fresh store handle on the same directory (a reload).
	fresh := accStoreAt(t, dir)
	reloaded, err := fresh.GetDay(accCtx, accDate)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Confirmed) != 1 || reloaded.Confirmed[0].Description != "USER TEXT" {
		t.Fatalf("reload must preserve the description, got %+v", reloaded.Confirmed)
	}
}

func boolPtr(b bool) *bool { return &b }

// --- Scenario 3: unknown duration/attendance/project blocks progression ------

func TestAcceptance_03_UnresolvedWorkBlocksSubmission(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*workflow.Record)
		reason string
	}{
		{
			name: "unknown_duration",
			mutate: func(rec *workflow.Record) {
				rec.Confirmed[0].StartTime = "09:00"
				rec.Confirmed[0].EndTime = "09:00"
			},
			reason: "unconfirmed_duration",
		},
		{
			name: "unresolved_project",
			mutate: func(rec *workflow.Record) {
				rec.Confirmed[0].Project = ""
			},
			reason: "unresolved_project",
		},
		{
			name: "not_attended_as_work",
			mutate: func(rec *workflow.Record) {
				rec.Confirmed[0].EvidenceIDs = []string{"m1"}
				rec.Review.Attendance = []workflow.AttendanceDecision{{EvidenceID: "m1", Attended: false}}
			},
			reason: "not_attended_as_work",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := accStore(t)
			remote := newFakeRemote()
			r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
			accSeed(t, store, accDate, func(rec *workflow.Record) {
				*rec = accReadyRecord(accDate)
				tc.mutate(rec)
			})

			pre, err := r.Preview(accCtx, accDate, accResolver)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if !pre.Blocked {
				t.Fatalf("unresolved work must block the preview")
			}
			found := false
			for _, reason := range pre.BlockReasons {
				if strings.Contains(reason, tc.reason) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want a %q block reason, got %v", tc.reason, pre.BlockReasons)
			}

			// No plan is produced, so no approval can be bound, and submission is
			// refused with zero writes.
			if _, err := r.Approve(accCtx, accDate, workflow.SubmissionPlan{}); err == nil {
				t.Fatalf("a blocked day must not be approvable")
			}
			res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()})
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			if !res.Blocked {
				t.Fatalf("submission must be refused for unresolved work")
			}
			if remote.writes() != 0 {
				t.Fatalf("unresolved work must cause zero writes, got %d", remote.writes())
			}
		})
	}
}

// --- Scenario 4: meeting plus commit counts once ----------------------------

func TestAcceptance_04_MeetingPlusCommitCountsOnce(t *testing.T) {
	store := accStore(t)
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	plug := &fakePlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{
		accGit("e1", "acme/repo", "feat/x", "fix parser", base.Add(20*time.Minute)),
		accGit("e2", "acme/repo", "feat/x", "add tests", base.Add(30*time.Minute)),
	}}
	cal := &fakePlugin{kind: evidence.SourceCalendar, ev: []evidence.Evidence{
		accMeeting("m1", "Sprint Planning", base.Add(15*time.Minute), base.Add(45*time.Minute)),
	}}
	cfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "git", Type: evidence.SourceLocalGit, Enabled: true},
		{ID: "cal", Type: evidence.SourceCalendar, Enabled: true},
	}}}
	r := accRunner(t, run.Deps{Store: store, Registry: accRegistry(plug, cal)}, cfg)

	if _, err := r.Collect(accCtx, accDate); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	prop, err := r.Propose(accCtx, accDate)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work) != 1 || prop.Work[0].Kind != proposal.BlockJoint {
		t.Fatalf("want one joint work block, got %+v", prop.Work)
	}
	joint := prop.Work[0]

	// Both descriptions are present and the evidence links cover both.
	if !strings.Contains(joint.Description, "fix parser") || !strings.Contains(joint.Description, "Sprint Planning") {
		t.Fatalf("the joint description must mention both activities, got %q", joint.Description)
	}
	if !contains(joint.EvidenceIDs, "e1") || !contains(joint.EvidenceIDs, "e2") || !contains(joint.EvidenceIDs, "m1") {
		t.Fatalf("the joint must link both commits and the meeting, got %v", joint.EvidenceIDs)
	}
	// Time is counted ONCE: the meeting's known duration, not the union span.
	if !joint.Duration.Known || joint.Duration.Minutes != 30 {
		t.Fatalf("the joint must count the meeting duration once (30), got %+v", joint.Duration)
	}

	// The standalone meeting is flagged as a collision so it is not double-counted.
	if len(prop.Meetings) != 1 || !prop.Meetings[0].Collision {
		t.Fatalf("the meeting must be marked as a collision, got %+v", prop.Meetings)
	}

	// Confirming ONLY the joint yields exactly one durable interval.
	if err := r.ApplyReview(accCtx, accDate, run.ReviewDecisions{
		Confirm: []run.ConfirmDecision{{BlockID: joint.ID, Project: "Proj", Attendance: boolPtr(true)}},
	}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec := accLoad(t, store, accDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want exactly one confirmed interval, got %d", len(rec.Confirmed))
	}
	ci := rec.Confirmed[0]
	if ci.Kind != string(proposal.BlockJoint) {
		t.Fatalf("want a joint confirmed interval, got %q", ci.Kind)
	}
	if !contains(ci.EvidenceIDs, "e1") || !contains(ci.EvidenceIDs, "m1") {
		t.Fatalf("the confirmed joint must keep both links, got %v", ci.EvidenceIDs)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// --- Scenario 5: exact midnight / four-hour splits --------------------------

func TestAcceptance_05_MidnightAndFourHourSplits(t *testing.T) {
	// (a) A meeting crossing local midnight is split into two LINKED fragments on
	// consecutive local dates: the same-day fragment stays on D, the later-day
	// fragment is a durable carry surfaced on D+1 as an UNCONFIRMED block. The
	// exact total is preserved, the carry is visible in the export, and nothing is
	// auto-approved or auto-written.
	store := accStore(t)
	remote := newFakeRemote()
	start := time.Date(2026, 10, 6, 23, 30, 0, 0, time.UTC)
	end := time.Date(2026, 10, 7, 0, 30, 0, 0, time.UTC)
	nextDate := "2026-10-07"
	cal := &fakePlugin{kind: evidence.SourceCalendar, ev: []evidence.Evidence{accMeeting("m1", "Late review", start, end)}}
	cfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "cal", Type: evidence.SourceCalendar, Enabled: true},
	}}}
	r := accRunner(t, run.Deps{Store: store, Registry: accRegistry(cal), Remote: remote}, cfg)

	if _, err := r.Collect(accCtx, accDate); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	prop, err := r.Propose(accCtx, accDate)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Meetings) != 1 {
		t.Fatalf("want exactly one same-day meeting fragment, got %d", len(prop.Meetings))
	}
	frag := prop.Meetings[0]
	if int(frag.EndAt.Sub(frag.StartAt)/time.Minute) != 30 {
		t.Fatalf("the same-day fragment must be 30 minutes, got %s-%s", frag.Start, frag.End)
	}
	if len(prop.Carries) != 1 {
		t.Fatalf("want one later-day carry fragment, got %d", len(prop.Carries))
	}
	carry := prop.Carries[0]
	if carry.Date != nextDate || carry.Start != "00:00" || carry.End != "00:30" {
		t.Fatalf("carry fragment = %s %s-%s, want %s 00:00-00:30", carry.Date, carry.Start, carry.End, nextDate)
	}
	if carry.SplitGroupID != frag.SplitGroupID || frag.PartIndex != 0 || carry.PartIndex != 1 {
		t.Fatalf("fragments must be linked: meeting=%+v carry=%+v", frag, carry)
	}
	if frag.Duration.Minutes+carry.Minutes != 60 {
		t.Fatalf("exact total = %d, want 60", frag.Duration.Minutes+carry.Minutes)
	}

	// Confirm ONLY the same-day fragment on D.
	if err := r.ApplyReview(accCtx, accDate, run.ReviewDecisions{
		Confirm: []run.ConfirmDecision{{BlockID: frag.ID, Attendance: boolPtr(true)}},
	}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec := accLoad(t, store, accDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want one confirmed same-day fragment, got %d", len(rec.Confirmed))
	}
	if rec.Confirmed[0].StartTime != "23:30" || rec.Confirmed[0].EndTime != "00:00" {
		t.Fatalf("the confirmed fragment must preserve exact boundaries, got %s-%s", rec.Confirmed[0].StartTime, rec.Confirmed[0].EndTime)
	}
	if rec.Confirmed[0].SplitGroupID != carry.SplitGroupID {
		t.Fatalf("the confirmed fragment must keep the split linkage")
	}
	if len(rec.Carries) != 1 || rec.Carries[0].Date != nextDate || rec.Carries[0].SplitGroupID != carry.SplitGroupID {
		t.Fatalf("the carry must be recorded durably on D, got %+v", rec.Carries)
	}

	// The carry is visible in D's export (marker/note + warning bullet naming D+1).
	rendered := export.Render(export.BuildNote(rec))
	for _, want := range []string{nextDate, "00:00-00:30", carry.SplitGroupID, "carry:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the export must show the carry (%q):\n%s", want, rendered)
		}
	}

	// D+1: the carry surfaces as an UNCONFIRMED block. Proposing writes nothing.
	propNext, err := r.Propose(accCtx, nextDate)
	if err != nil {
		t.Fatalf("Propose(D+1): %v", err)
	}
	var carryBlock proposal.ProposedTimeBlock
	found := false
	for _, b := range append(append([]proposal.ProposedTimeBlock{}, propNext.Meetings...), propNext.Manual...) {
		if b.SplitGroupID == carry.SplitGroupID {
			carryBlock = b
			found = true
		}
	}
	if !found {
		t.Fatalf("D+1 must surface the carry as an unconfirmed block, got %+v / %+v", propNext.Meetings, propNext.Manual)
	}
	if !carryBlock.Carryover || carryBlock.Preselected {
		t.Fatalf("the D+1 carry block must be an unconfirmed carryover (not preselected): %+v", carryBlock)
	}
	if recNext, err := store.GetDay(accCtx, nextDate); err == nil && len(recNext.Confirmed) != 0 {
		t.Fatalf("proposing D+1 must not auto-confirm the carry: %+v", recNext.Confirmed)
	}

	// Only an explicit confirmation persists the D+1 interval, linked to the SAME
	// split group. Nothing is auto-approved or auto-written.
	if err := r.ApplyReview(accCtx, nextDate, run.ReviewDecisions{
		Confirm: []run.ConfirmDecision{{BlockID: carryBlock.ID, Attendance: boolPtr(true)}},
	}); err != nil {
		t.Fatalf("ApplyReview(D+1): %v", err)
	}
	recNext := accLoad(t, store, nextDate)
	if len(recNext.Confirmed) != 1 {
		t.Fatalf("want one confirmed D+1 carry interval, got %d", len(recNext.Confirmed))
	}
	ciNext := recNext.Confirmed[0]
	if ciNext.SplitGroupID != carry.SplitGroupID || ciNext.PartIndex != carry.PartIndex {
		t.Fatalf("the D+1 interval must link to the same split group: %+v", ciNext)
	}
	if !ciNext.Carryover || ciNext.Date != nextDate {
		t.Fatalf("the D+1 interval must be a carryover on its own date: %+v", ciNext)
	}
	if len(recNext.Approvals) != 0 {
		t.Fatalf("the carry path must create NO approval, got %+v", recNext.Approvals)
	}
	if remote.writes() != 0 {
		t.Fatalf("the carry path must cause zero remote writes, got %d", remote.writes())
	}

	// (b) Four-hour split through the SHARED CORE (no bypass): a >4h manual
	// interval is proposed as linked fragments, and confirming them via
	// run.ApplyReview persists linked durable intervals with an exact total.
	splitStore := accStore(t)
	manualStart := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	manualEnd := time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC) // 6 hours
	accSeed(t, splitStore, accDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, evidence.Evidence{
			ID: "man1", Kind: evidence.SourceManual, Title: "long session",
			Start: manualStart, End: manualEnd, Duration: evidence.KnownDuration(360),
			SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceManual}}, ObservedAt: manualStart,
		})
	})
	splitRunner := accRunner(t, run.Deps{Store: splitStore}, run.Config{})
	prop2, err := splitRunner.Propose(accCtx, accDate)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop2.Manual) < 2 {
		t.Fatalf("a 6-hour manual interval must be proposed as linked fragments, got %d", len(prop2.Manual))
	}
	groupID := prop2.Manual[0].SplitGroupID
	if groupID == "" {
		t.Fatalf("split fragments must share a SplitGroupID")
	}
	var confirms []run.ConfirmDecision
	for i, b := range prop2.Manual {
		if b.SplitGroupID != groupID || b.PartIndex != i {
			t.Fatalf("fragments must be linked (shared group, sequential index): %+v", b)
		}
		if int(b.EndAt.Sub(b.StartAt)/time.Minute) > proposal.DefaultMaxEntryMinutes {
			t.Fatalf("fragment %d exceeds the max entry minutes", i)
		}
		confirms = append(confirms, run.ConfirmDecision{BlockID: b.ID, Project: "Proj"})
	}
	if err := splitRunner.ApplyReview(accCtx, accDate, run.ReviewDecisions{Confirm: confirms}); err != nil {
		t.Fatalf("ApplyReview (four-hour split): %v", err)
	}
	rec2 := accLoad(t, splitStore, accDate)
	if len(rec2.Confirmed) != len(prop2.Manual) {
		t.Fatalf("want %d linked confirmed fragments, got %d", len(prop2.Manual), len(rec2.Confirmed))
	}
	sum := 0
	for i, ci := range rec2.Confirmed {
		if ci.SplitGroupID != groupID || ci.PartIndex != i {
			t.Fatalf("durable fragments must stay linked: %+v", ci)
		}
		minutes := accMinutes(ci.EndTime) - accMinutes(ci.StartTime)
		if minutes > proposal.DefaultMaxEntryMinutes {
			t.Fatalf("durable fragment %d exceeds the max entry minutes", i)
		}
		sum += minutes
	}
	if sum != 360 {
		t.Fatalf("confirmed fragments must sum to the exact total (360), got %d", sum)
	}
}

// --- Scenario 6: remote changes invalidate approval -------------------------

func TestAcceptance_06_RemoteChangeInvalidatesApproval(t *testing.T) {
	store := accStore(t)
	remote := newFakeRemote()
	remote.setEntry(workflow.RemoteState{
		RemoteID: "r1", Date: accDate, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "old text", ObservedAt: accNow,
	})
	r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})

	accSeed(t, store, accDate, func(rec *workflow.Record) {
		*rec = accReadyRecord(accDate)
		rec.RemoteMappings = []workflow.RemoteMapping{{
			LocalID: "c1", RemoteID: "r1", Date: accDate, StartTime: "09:00", EndTime: "10:00",
			ClassificationID: "cls-1", Description: "old text",
			LastObserved: workflow.RemoteState{
				RemoteID: "r1", Date: accDate, StartTime: "09:00", EndTime: "10:00",
				ClassificationID: "cls-1", Description: "old text", ObservedAt: accNow,
			},
		}}
	})

	pre, err := r.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pre.Blocked {
		t.Fatalf("unexpected block: %v", pre.BlockReasons)
	}
	if len(pre.Plan.Operations) != 1 || pre.Plan.Operations[0].Kind != workflow.PlanOpEdit {
		t.Fatalf("want one edit operation, got %+v", pre.Plan.Operations)
	}
	approval, err := r.Approve(accCtx, accDate, pre.Plan)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// The remote changes between preview and submit.
	remote.setEntry(workflow.RemoteState{
		RemoteID: "r1", Date: accDate, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "changed remotely", ObservedAt: accNow,
	})

	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked {
		t.Fatalf("a stale approval must block the submission")
	}
	if remote.writes() != 0 {
		t.Fatalf("a stale approval must cause ZERO writes, got %d", remote.writes())
	}
	rec := accLoad(t, store, accDate)
	invalidated := false
	for _, a := range rec.Approvals {
		if a.ApprovalID == approval.ApprovalID && a.InvalidatedAt != nil {
			invalidated = true
		}
	}
	if !invalidated {
		t.Fatalf("the stale approval must be durably invalidated")
	}
}

// --- Scenario 7: no blind duplicates after failure --------------------------

func TestAcceptance_07_NoBlindDuplicatesAfterFailure(t *testing.T) {
	store := accStore(t)
	remote := newFakeRemote()
	// The create actually applied remotely but the response was lost (uncertain).
	remote.onCreate = func(req gradion.CreateRequest) (gradion.WriteResult, error) {
		return gradion.WriteResult{Status: gradion.WriteUncertain, ErrorCode: gradion.CodeWriteUncertain}, nil
	}
	r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	accPlanFor(t, store, r, accDate)

	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked && !res.Execution.Partial {
		t.Fatalf("an uncertain write must stop the run as partial/blocked")
	}
	if remote.writes() != 1 {
		t.Fatalf("an uncertain write must stop after ONE dispatch, got %d", remote.writes())
	}
	rec := accLoad(t, store, accDate)
	if blocked, reasons := submit.RestartGate(rec); !blocked {
		t.Fatalf("the restart gate must block after an uncertain write, got %v", reasons)
	}
	if res.Execution.RemainingPlan != nil {
		t.Fatalf("an unresolved operation must block the remaining plan")
	}

	// The remote actually holds the entry; adopt it explicitly.
	remote.setEntry(workflow.RemoteState{
		RemoteID: "r1", Date: accDate, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "work", ObservedAt: accNow,
	})
	key := ""
	for _, o := range rec.Outcomes {
		key = o.OperationKey
	}
	if key == "" {
		t.Fatalf("want a durable outcome key")
	}
	if _, err := r.Resolve(accCtx, accDate, []submit.Resolution{{
		OperationKey: key, Kind: submit.ResolutionAdopt, RemoteID: "r1", LocalID: "c1",
	}}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rec2 := accLoad(t, store, accDate)
	if blocked, reasons := submit.RestartGate(rec2); blocked {
		t.Fatalf("the restart gate must clear after adoption, got %v", reasons)
	}

	// A re-plan is a no-op (the adopted create is now mapped and matching).
	pre2, err := r.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview 2: %v", err)
	}
	if pre2.Blocked {
		t.Fatalf("the re-plan must not block: %v", pre2.BlockReasons)
	}
	if len(pre2.Plan.Operations) != 0 {
		t.Fatalf("the adopted create must become a no-op, got %+v", pre2.Plan.Operations)
	}
	if _, err := r.Approve(accCtx, accDate, pre2.Plan); err != nil {
		t.Fatalf("Approve 2: %v", err)
	}
	if _, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()}); err != nil {
		t.Fatalf("Submit 2: %v", err)
	}
	if remote.writes() != 1 {
		t.Fatalf("a restart must never re-create, got %d writes", remote.writes())
	}
}

// --- Scenario 8: visible plugin failures ------------------------------------

func TestAcceptance_08_VisiblePluginFailuresRequireAcknowledgment(t *testing.T) {
	store := accStore(t)
	plug := &fakePlugin{kind: evidence.SourceCalendar, status: evidence.StatusIncomplete}
	cfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "cal", Type: evidence.SourceCalendar, Enabled: true},
	}}}
	r := accRunner(t, run.Deps{Store: store, Registry: accRegistry(plug)}, cfg)
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })

	collect, err := r.Collect(accCtx, accDate)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if collect.Summary.Complete {
		t.Fatalf("an incomplete source must mark the collection incomplete")
	}
	rec := accLoad(t, store, accDate)
	if rec.Collection == nil || rec.Collection.Complete {
		t.Fatalf("the incomplete collection state must be durable, got %+v", rec.Collection)
	}

	// Submission is blocked until the incomplete source is acknowledged.
	pre, err := r.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !pre.Blocked {
		t.Fatalf("an unacknowledged incomplete source must block submission")
	}
	blockedReason := false
	for _, reason := range pre.BlockReasons {
		if strings.Contains(reason, "incomplete_collection") {
			blockedReason = true
		}
	}
	if !blockedReason {
		t.Fatalf("want an incomplete_collection block, got %v", pre.BlockReasons)
	}

	// Acknowledge explicitly, then the block clears.
	if err := r.Acknowledge(accCtx, accDate, []string{"cal"}); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	pre2, err := r.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview 2: %v", err)
	}
	if pre2.Blocked {
		t.Fatalf("an acknowledged source must unblock, got %v", pre2.BlockReasons)
	}

	// A MALFORMED plugin output (an undeclared evidence kind) is a visible contract
	// failure, not a silent success: it blocks submission until acknowledged.
	malStore := accStore(t)
	malPlugin := &misdeclaringPlugin{fakePlugin{
		kind: evidence.SourceCalendar,
		ev:   []evidence.Evidence{accGit("bad", "acme/repo", "feat/x", "undeclared kind", time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC))},
	}}
	malCfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "cal", Type: evidence.SourceCalendar, Enabled: true},
	}}}
	malRunner := accRunner(t, run.Deps{Store: malStore, Registry: accRegistry(malPlugin)}, malCfg)
	accSeed(t, malStore, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	malCollect, err := malRunner.Collect(accCtx, accDate)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if malCollect.Summary.Complete {
		t.Fatalf("malformed output must mark the collection incomplete")
	}
	code := ""
	for _, s := range malCollect.Summary.Sources {
		if s.ErrorCode != "" {
			code = s.ErrorCode
		}
	}
	if code != evidence.CodeDeclaredKindsMismatch {
		t.Fatalf("want a declared-kinds contract failure, got %q", code)
	}
	malPre, err := malRunner.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !malPre.Blocked {
		t.Fatalf("malformed output must block submission until acknowledged")
	}
	if err := malRunner.Acknowledge(accCtx, accDate, []string{"cal"}); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	malPre2, err := malRunner.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview 2: %v", err)
	}
	if malPre2.Blocked {
		t.Fatalf("an acknowledged malformed source must unblock, got %v", malPre2.BlockReasons)
	}
}

// --- Scenario 9: secret protection ------------------------------------------

// secretSentinel is a distinctive literal used to detect leakage. This is a
// BOUNDED, LITERAL check: it proves the sentinel does not appear verbatim in the
// asserted artifacts. It does NOT prove protection against encoded, split, or
// transformed forms (documented limit).
const secretSentinel = "S3CRET-SENTINEL-9c1f-DO-NOT-LEAK"

type sentinelKeychain struct{}

func (sentinelKeychain) Lookup(ctx context.Context, service, account string) (string, error) {
	return secretSentinel, nil
}

func TestAcceptance_09_SecretProtection(t *testing.T) {
	// BOUNDED, LITERAL check: the sentinel must not appear verbatim in the asserted
	// artifacts. It does NOT prove protection against encoded/split/transformed
	// forms (documented limit).

	// (a) A secret-flagged config key holding a RAW secret is rejected by the
	// contract check; the sentinel never reaches the durable record, and the check
	// names only the KEY.
	rawCfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "cal", Type: evidence.SourceCalendar, Enabled: true, Config: map[string]string{"url_ref": secretSentinel}},
	}}}
	rawRunner := accRunner(t, run.Deps{
		Store: accStore(t), Registry: accRegistry(&secretRefPlugin{fakePlugin{kind: evidence.SourceCalendar}}), GitToken: "test-git-token",
	}, rawCfg)
	rawPre, err := rawRunner.Preflight(accCtx, run.PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if rawPre.OK {
		t.Fatalf("a raw secret in a secret config key must be refused")
	}
	if strings.Contains(accJSON(t, rawPre), secretSentinel) {
		t.Fatalf("the preflight result must not echo the raw secret")
	}

	// (b) A resolved Keychain credential is used in memory only: it never enters the
	// durable record or the exported note.
	vault := t.TempDir()
	store := accStore(t)
	cfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "cal", Type: evidence.SourceCalendar, Enabled: true, Config: map[string]string{"url_ref": "keychain:calendar_feed_url"}},
	}}}
	r := accRunner(t, run.Deps{
		Store: store, Registry: accRegistry(&secretRefPlugin{fakePlugin{kind: evidence.SourceCalendar}}),
		Keychain: sentinelKeychain{}, Exporter: export.NewNoteExporter(vault, "Daily"), GitToken: "test-git-token",
	}, cfg)
	pre, err := r.Preflight(accCtx, run.PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !pre.OK {
		t.Fatalf("a keychain reference must resolve: %+v", pre.Checks)
	}
	if strings.Contains(accJSON(t, pre), secretSentinel) {
		t.Fatalf("the preflight result must not carry the resolved secret")
	}
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	if err := r.ApplyReview(accCtx, accDate, run.ReviewDecisions{}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	if _, err := r.Export(accCtx, accDate); err != nil {
		t.Fatalf("Export: %v", err)
	}
	rec := accLoad(t, store, accDate)
	if strings.Contains(accJSON(t, rec), secretSentinel) {
		t.Fatalf("the durable record must not carry the secret")
	}
	note, err := os.ReadFile(filepath.Join(vault, "Daily", accDate+".md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if strings.Contains(string(note), secretSentinel) {
		t.Fatalf("the exported note must not carry the secret")
	}

	// (c) A remote error body that ECHOES the bearer token must not surface it: the
	// MCP layer redacts the token from the error.
	sess, err := mcp.NewSession(mcp.Config{Endpoint: "http://invalid.local", Token: secretSentinel, Transport: &accTransport{token: secretSentinel}})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	client := gradion.NewClient(sess)
	if _, err := client.Initialize(accCtx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := client.VerifyCapabilities(accCtx); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}
	errStore := accStore(t)
	accSeed(t, errStore, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	errRunner := accRunner(t, run.Deps{Store: errStore, Registry: evidence.NewRegistry(), Remote: client}, run.Config{})
	if _, err := errRunner.Preview(accCtx, accDate, accResolver); err == nil {
		t.Fatalf("want a remote error")
	} else if strings.Contains(err.Error(), secretSentinel) {
		t.Fatalf("a remote error must not echo the token, got %q", err.Error())
	}

	// (d) A terminal credential failure surfaces a stable code, never the secret.
	terminal := accRunner(t, run.Deps{
		Store: accStore(t), Registry: accRegistry(&secretRefPlugin{fakePlugin{kind: evidence.SourceCalendar}}),
		Keychain: terminalSentinelKeychain{}, GitToken: "test-git-token",
	}, cfg)
	tres, err := terminal.Preflight(accCtx, run.PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight (terminal): %v", err)
	}
	if strings.Contains(accJSON(t, tres), secretSentinel) {
		t.Fatalf("a terminal failure must not echo the secret")
	}
}

type terminalSentinelKeychain struct{}

func (terminalSentinelKeychain) Lookup(ctx context.Context, service, account string) (string, error) {
	return "", &keychain.OperatingError{Outcome: keychain.OutcomeTerminal, Code: "KEYCHAIN_ERR_TIMEOUT", Message: "keychain lookup exceeded deadline"}
}

// --- Scenario 10: shared no-AI workflow -------------------------------------

func TestAcceptance_10_SharedNoAIWorkflow(t *testing.T) {
	store := accStore(t)
	vault := t.TempDir()
	remote := newFakeRemote()
	plug := &fakePlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{
		accGit("e1", "acme/repo", "feat/x", "fix parser", time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)),
		accGit("e2", "acme/repo", "feat/x", "add tests", time.Date(2026, 10, 6, 9, 15, 0, 0, time.UTC)),
	}}
	cfg := run.Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{
		{ID: "git", Type: evidence.SourceLocalGit, Enabled: true},
	}}}
	// No editorial agent is configured at all.
	r := accRunner(t, run.Deps{
		Store:    store,
		Registry: accRegistry(plug),
		Remote:   remote,
		Exporter: export.NewNoteExporter(vault, "Daily"),
	}, cfg)

	if _, err := r.Collect(accCtx, accDate); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	prop, err := r.Propose(accCtx, accDate)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work) == 0 {
		t.Fatalf("want a work block, got %+v", prop)
	}

	// The no-AI path is deterministic and never invokes an agent.
	syn, err := r.Synthesize(accCtx, accDate, prop)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if syn.Report.Invoked {
		t.Fatalf("no-AI must not invoke an agent")
	}
	for _, s := range syn.Suggestions {
		if s.Origin != editorial.OriginDeterministic {
			t.Fatalf("no-AI must yield deterministic text, got %s", s.Origin)
		}
	}
	rec := accLoad(t, store, accDate)
	if len(rec.Editorial) != 0 || rec.Usage != nil {
		t.Fatalf("the no-AI path must not persist editorial state")
	}

	// The whole flow completes: review -> preview -> approve -> submit -> export.
	if err := r.ApplyReview(accCtx, accDate, run.ReviewDecisions{
		Confirm: []run.ConfirmDecision{{BlockID: prop.Work[0].ID, Project: "Proj", Attendance: boolPtr(true)}},
	}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	// RESOLVER-AUTOFILL REMOVED: an unset classification blocks with a NON-APPLIED
	// suggestion; it is never auto-filled from the project->classification map.
	blockedPre, err := r.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview (unset): %v", err)
	}
	if !blockedPre.Blocked || !strings.Contains(strings.Join(blockedPre.BlockReasons, ","), "unresolved_classification") {
		t.Fatalf("an unset classification must block, got %v", blockedPre.BlockReasons)
	}
	if len(blockedPre.Suggestions) == 0 {
		t.Fatalf("the resolver value must surface as a suggestion, got none")
	}
	// The user explicitly selects the suggested classification (what
	// `timesheet classify select` does), and the flow then completes.
	accSeed(t, store, accDate, func(rec *workflow.Record) {
		for i := range rec.Confirmed {
			rec.Confirmed[i].ClassificationID = "cls-1"
			rec.Confirmed[i].ClassificationCode = "INTGRADI2610"
			rec.Confirmed[i].ClassificationName = "Gradion Intern Academy 2026"
			rec.Confirmed[i].ActivityType = "internal project"
		}
	})
	pre, err := r.Preview(accCtx, accDate, accResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pre.Blocked {
		t.Fatalf("unexpected block: %v", pre.BlockReasons)
	}
	if _, err := r.Approve(accCtx, accDate, pre.Plan); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("the no-AI flow must submit, got blocked %v", res.BlockReasons)
	}
	if remote.writes() != 1 {
		t.Fatalf("want exactly one remote write, got %d", remote.writes())
	}
	if _, err := os.Stat(filepath.Join(vault, "Daily", accDate+".md")); err != nil {
		t.Fatalf("the managed note must exist: %v", err)
	}
}

// --- Submission boundary: classification validation is never bypassed --------

// W1: at the shared submission boundary, a remote configured with a NIL catalog
// fails closed (missing_catalog) BEFORE any write — the closed bypass.
func TestAcceptance_SubmissionBoundary_MissingCatalogBlocks(t *testing.T) {
	store := accStore(t)
	remote := newFakeRemote()
	r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	accPlanFor(t, store, r, accDate)

	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver}) // no Catalog
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReasonStr(res.BlockReasons, run.BlockMissingCatalog) {
		t.Fatalf("want %s, got %v", run.BlockMissingCatalog, res.BlockReasons)
	}
	if remote.writes() != 0 {
		t.Fatalf("a missing catalog must produce ZERO writes, got %d", remote.writes())
	}
}

// W1: a PARTIAL catalog that does not contain the planned classification is
// resolved by a TARGETED read, so a valid recent preference outside the current
// search is never treated as invalid.
func TestAcceptance_SubmissionBoundary_TargetedReadResolvesPartialCatalog(t *testing.T) {
	store := accStore(t)
	remote := newFakeRemote()
	r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	accPlanFor(t, store, r, accDate)

	partial := &gradion.Catalog{
		Complete: false, Source: "live", Query: "something-else", ObservedAt: accNow,
		Options: []gradion.ClassificationOption{{ID: "other", Name: "Other", Code: "OTHER", ActivityType: "internal project"}},
	}
	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: partial})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a targeted read that finds cls-1 must clear the block, got %v", res.BlockReasons)
	}
	if remote.writes() != 1 {
		t.Fatalf("want one write, got %d", remote.writes())
	}
}

func hasReasonStr(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// --- Fault injection --------------------------------------------------------

// failingIntentStore fails the FIRST Session.Update (the pre-write intent), so no
// dispatch may happen.
type failingIntentStore struct {
	workflow.Store
	failed bool
}

func (s *failingIntentStore) BeginDaySession(ctx context.Context, date string) (workflow.Session, error) {
	inner, err := s.Store.BeginDaySession(ctx, date)
	if err != nil {
		return nil, err
	}
	return &failingIntentSession{Session: inner, owner: s}, nil
}

type failingIntentSession struct {
	workflow.Session
	owner *failingIntentStore
}

func (s *failingIntentSession) Update(ctx context.Context, mutate func(*workflow.Record) error) (*workflow.Record, error) {
	if !s.owner.failed {
		s.owner.failed = true
		return nil, errors.New("injected durability failure: intent persist failed")
	}
	return s.Session.Update(ctx, mutate)
}

func TestAcceptance_Fault_IntentPersistFailureZeroWrites(t *testing.T) {
	base := accStore(t)
	remote := newFakeRemote()
	wrapped := &failingIntentStore{Store: base}
	r := accRunner(t, run.Deps{Store: wrapped, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accSeed(t, base, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })

	// Build the plan/approval against the plain store, then submit through the
	// failing store.
	plainRunner := accRunner(t, run.Deps{Store: base, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accPlanFor(t, base, plainRunner, accDate)

	if _, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()}); err == nil {
		t.Fatalf("an intent-persist failure must surface as an error")
	}
	if remote.writes() != 0 {
		t.Fatalf("no dispatch may happen when the intent cannot be persisted, got %d", remote.writes())
	}
}

func TestAcceptance_Fault_RemoteWriteUncertaintyStopsAndBlocks(t *testing.T) {
	store := accStore(t)
	remote := newFakeRemote()
	remote.onCreate = func(req gradion.CreateRequest) (gradion.WriteResult, error) {
		return gradion.WriteResult{Status: gradion.WriteUncertain, ErrorCode: gradion.CodeWriteUncertain}, nil
	}
	r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	accPlanFor(t, store, r, accDate)

	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Execution.Partial {
		t.Fatalf("an uncertain write must mark the run partial")
	}
	if remote.writes() != 1 {
		t.Fatalf("an uncertain write must stop after one dispatch, got %d", remote.writes())
	}
	rec := accLoad(t, store, accDate)
	if blocked, _ := submit.RestartGate(rec); !blocked {
		t.Fatalf("the restart gate must block after an uncertain write")
	}
}

func TestAcceptance_Fault_RemoteChangeBetweenPreviewAndSubmit(t *testing.T) {
	// A CREATE whose slot becomes occupied between preview and submit.
	store := accStore(t)
	remote := newFakeRemote()
	r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	accPlanFor(t, store, r, accDate)

	// A remote entry now occupies the create's slot.
	remote.setEntry(workflow.RemoteState{
		RemoteID: "r-new", Date: accDate, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "work", ObservedAt: accNow,
	})

	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked {
		t.Fatalf("an appearing remote must block the create")
	}
	if remote.writes() != 0 {
		t.Fatalf("a stale create must cause ZERO writes, got %d", remote.writes())
	}
}

type failingExporter struct{ err error }

func (f *failingExporter) Export(ctx context.Context, note export.Note) (export.Result, error) {
	return export.Result{}, f.err
}

func TestAcceptance_Fault_ExportFailureDoesNotReplay(t *testing.T) {
	store := accStore(t)
	vault := t.TempDir()
	remote := newFakeRemote()
	exp := &failingExporter{err: errors.New("injected vault write failure")}
	r := accRunner(t, run.Deps{
		Store: store, Registry: evidence.NewRegistry(), Remote: remote, Exporter: exp,
	}, run.Config{})
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	accPlanFor(t, store, r, accDate)

	if _, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()}); err == nil {
		t.Fatalf("the export failure must surface")
	}
	if remote.writes() != 1 {
		t.Fatalf("want exactly one write (no replay), got %d", remote.writes())
	}
	rec := accLoad(t, store, accDate)
	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != workflow.OutcomeConfirmed {
		t.Fatalf("the outcome must be durable, got %+v", rec.Outcomes)
	}

	// A re-export with a working exporter succeeds and never replays a write.
	good := accRunner(t, run.Deps{
		Store: store, Registry: evidence.NewRegistry(), Remote: remote,
		Exporter: export.NewNoteExporter(vault, "Daily"),
	}, run.Config{})
	if _, err := good.Export(accCtx, accDate); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if remote.writes() != 1 {
		t.Fatalf("a re-export must not replay a write, got %d", remote.writes())
	}
}

func TestAcceptance_Fault_ConcurrentSecondWriter(t *testing.T) {
	store := accStore(t)
	remote := newFakeRemote()
	r := accRunner(t, run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	accSeed(t, store, accDate, func(rec *workflow.Record) { *rec = accReadyRecord(accDate) })
	accPlanFor(t, store, r, accDate)

	held, err := store.BeginDaySession(accCtx, accDate)
	if err != nil {
		t.Fatalf("BeginDaySession: %v", err)
	}
	defer func() { _ = held.Close() }()

	res, err := r.Submit(accCtx, accDate, run.SubmitOptions{Resolver: accResolver, Catalog: accCatalog()})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked {
		t.Fatalf("a concurrent writer must block the submission")
	}
	found := false
	for _, reason := range res.BlockReasons {
		if reason == "writer_busy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want writer_busy, got %v", res.BlockReasons)
	}
	if remote.writes() != 0 {
		t.Fatalf("a busy writer must cause ZERO writes, got %d", remote.writes())
	}
}

// --- CLI / skill parity (F4) ------------------------------------------------

// TestAcceptance_11_CLISkillParity asserts the CLI is a THIN adapter over the SAME
// shared core this harness drives, and that the skill delegates to the CLI's
// subcommands (parity with tests/skill_test.go + the e2e suite).
func TestAcceptance_11_CLISkillParity(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	cli, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "timesheet", "main.go"))
	if err != nil {
		t.Fatalf("read CLI: %v", err)
	}
	for _, want := range []string{"internal/run", "run.New(", ".Preflight(", ".ApplyReview(", ".Preview(", ".Approve(", ".Submit(", ".Export("} {
		if !strings.Contains(string(cli), want) {
			t.Errorf("the CLI must delegate %q to the shared core", want)
		}
	}

	wf, err := os.ReadFile(filepath.Join(repoRoot, ".claude", "skills", "timesheet", "workflow.md"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	for _, cmd := range []string{"preflight", "review", "preview", "approve", "submit", "export", "resolve"} {
		if !strings.Contains(string(wf), "$TS "+cmd) && !strings.Contains(string(wf), "timesheet "+cmd) {
			t.Errorf("the skill must invoke the %q subcommand", cmd)
		}
	}
}
