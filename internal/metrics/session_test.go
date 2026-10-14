package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// fp is a small helper for the nil-vs-pointer-to-zero assertions.
func fp(v float64) *float64 { return &v }

// sessionCallRun builds a single-call RunRecord for the session tests.
func sessionCallRun(provider, model string, usage Usage, step StepID, ts time.Time) RunRecord {
	return RunRecord{
		RunID:      "r-" + model,
		SessionID:  "s-" + model,
		Provider:   provider,
		Model:      model,
		StartedAt:  ts,
		EndedAt:    ts,
		Usage:      usage,
		Provenance: ProvenanceObserved,
		Calls: []CallRecord{{
			Provider: provider, Model: model, Timestamp: ts, Step: step, Usage: usage,
		}},
	}
}

// TestCacheHitPercent_BandsAndUnknown asserts the cache-hit band, the zero
// denominator, and the unknown component rule.
func TestCacheHitPercent_BandsAndUnknown(t *testing.T) {
	if got := CacheHitPercent(KnownUsage(100, 0, 100, 0, 0)); got == nil || *got != 50.0 {
		t.Fatalf("cache hit = %v, want 50.0", got)
	}
	if got := CacheHitPercent(KnownUsage(0, 0, 0, 0, 0)); got != nil {
		t.Fatalf("zero denominator cache hit = %v, want nil", got)
	}
	u := KnownUsage(100, 0, 100, 0, 0)
	u.CacheWrite = workflow.UnknownMeasurement()
	if got := CacheHitPercent(u); got != nil {
		t.Fatalf("unknown component cache hit = %v, want nil", got)
	}
}

// TestSession_UnknownPriceIsNilNotZero asserts a model ABSENT from the catalog is
// unknown (all-nil rates, nil cost, unknown source, JSON null), while a catalog
// model with cacheWrite 0 has a NON-NIL pointer to 0.
func TestSession_UnknownPriceIsNilNotZero(t *testing.T) {
	ts := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cat, err := ParseModelCatalog([]byte(`{
		"opencode": {"models":[{"id":"space-bunny-free","cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}
	}`))
	if err != nil {
		t.Fatalf("parse catalog: %v", err)
	}

	// Absent: (opencode-go, space-bunny-free) is not in the catalog.
	absent := BuildSession(sessionCallRun("opencode-go", "space-bunny-free", KnownUsage(100, 10, 5, 0, 0), StepCollect, ts), cat, time.UTC)
	for name, r := range map[string]*float64{
		"input":      absent.Price.Input,
		"output":     absent.Price.Output,
		"cacheRead":  absent.Price.CacheRead,
		"cacheWrite": absent.Price.CacheWrite,
	} {
		if r != nil {
			t.Fatalf("absent price %s = %v, want nil", name, *r)
		}
	}
	if absent.Cost != nil {
		t.Fatalf("absent cost = %v, want nil", *absent.Cost)
	}
	if absent.PriceSource != PriceSourceUnknown {
		t.Fatalf("absent price source = %q, want unknown", absent.PriceSource)
	}
	data, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("marshal absent: %v", err)
	}
	if !bytes.Contains(data, []byte(`"cost":null`)) {
		t.Fatalf("absent session cost not JSON null:\n%s", data)
	}
	if !bytes.Contains(data, []byte(`"cache_write":null`)) {
		t.Fatalf("absent session price rate not JSON null:\n%s", data)
	}

	// Present with a legitimate known-zero cacheWrite rate.
	present := BuildSession(sessionCallRun("opencode", "space-bunny-free", KnownUsage(100, 10, 5, 0, 0), StepCollect, ts), cat, time.UTC)
	if present.Price.CacheWrite == nil || *present.Price.CacheWrite != 0 {
		t.Fatalf("catalog cacheWrite = %v, want non-nil pointer to 0", present.Price.CacheWrite)
	}
	if present.PriceSource != PriceSourceCatalog {
		t.Fatalf("present price source = %q, want catalog", present.PriceSource)
	}
}

// TestSession_MidnightSpanningAssociation asserts the local-midnight straddle
// leaves the association unknown and a single local date associates it.
func TestSession_MidnightSpanningAssociation(t *testing.T) {
	d1 := time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	run := RunRecord{
		RunID: "s1", SessionID: "s1", Provider: "p", Provenance: ProvenanceObserved,
		Calls: []CallRecord{
			{Provider: "p", Model: "m", Step: StepCollect, Timestamp: d1, Usage: KnownUsage(1, 1, 0, 0, 0)},
			{Provider: "p", Model: "m", Step: StepReview, Timestamp: d2, Usage: KnownUsage(1, 1, 0, 0, 0)},
		},
	}
	sess := BuildSession(run, nil, time.UTC)
	if sess.LoggedDate != "" {
		t.Fatalf("straddle logged date = %q, want empty", sess.LoggedDate)
	}
	if sess.Association != AssociationUnknown {
		t.Fatalf("straddle association = %q, want unknown", sess.Association)
	}
	if len(sess.LocalDates) != 2 {
		t.Fatalf("straddle local dates = %v, want 2 entries", sess.LocalDates)
	}

	// A single local date, and a StepUnknown call on another day does NOT count.
	run.Calls = []CallRecord{
		{Provider: "p", Model: "m", Step: StepCollect, Timestamp: d1, Usage: KnownUsage(1, 1, 0, 0, 0)},
		{Provider: "p", Model: "m", Step: StepReview, Timestamp: d1.Add(30 * time.Minute), Usage: KnownUsage(1, 1, 0, 0, 0)},
		{Provider: "p", Model: "m", Step: StepUnknown, Timestamp: d2.Add(48 * time.Hour), Usage: KnownUsage(1, 1, 0, 0, 0)},
	}
	sess = BuildSession(run, nil, time.UTC)
	if sess.LoggedDate != "2026-10-11" || sess.Association != AssociationLocalStepDate {
		t.Fatalf("single date = %q/%q, want 2026-10-11/local_step_date", sess.LoggedDate, sess.Association)
	}
	if len(sess.LocalDates) != 1 || sess.LocalDates[0] != "2026-10-11" {
		t.Fatalf("single local dates = %v, want [2026-10-11]", sess.LocalDates)
	}
}

// TestSession_MultiModel asserts a two-model session is attributed to no single
// model and that the per-band usage sums to the session usage.
func TestSession_MultiModel(t *testing.T) {
	ts := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ua := KnownUsage(100, 10, 5, 1, 7)
	ub := KnownUsage(20, 2, 1, 0, 3)
	run := RunRecord{
		RunID: "s1", SessionID: "s1", Provider: "p", Provenance: ProvenanceObserved,
		Usage: ua.Add(ub),
		Calls: []CallRecord{
			{Provider: "p", Model: "model-a", Step: StepCollect, Timestamp: ts, Usage: ua},
			{Provider: "p", Model: "model-b", Step: StepCollect, Timestamp: ts, Usage: ub},
		},
	}
	sess := BuildSession(run, nil, time.UTC)
	if sess.Model != "" {
		t.Fatalf("multi-model session model = %q, want empty", sess.Model)
	}
	if len(sess.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(sess.Models))
	}
	sum := sess.Models[0].Usage.Add(sess.Models[1].Usage)
	if sum.Input.Value != sess.Usage.Input.Value || sum.Output.Value != sess.Usage.Output.Value ||
		sum.CacheRead.Value != sess.Usage.CacheRead.Value || sum.CacheWrite.Value != sess.Usage.CacheWrite.Value {
		t.Fatalf("per-model usage sum %+v != session usage %+v", sum, sess.Usage)
	}
}

// TestCatalog_KeyIsProviderAndModel asserts the SAME model id under two providers
// resolves different rates.
func TestCatalog_KeyIsProviderAndModel(t *testing.T) {
	cat, err := ParseModelCatalog([]byte(`{
		"opencode": {"models":[{"id":"m","cost":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4}}]},
		"other":    {"models":[{"id":"m","cost":{"input":10,"output":20,"cacheRead":30,"cacheWrite":40}}]}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p1, ok1 := cat.Lookup("opencode", "m")
	p2, ok2 := cat.Lookup("other", "m")
	if !ok1 || !ok2 {
		t.Fatalf("lookup misses: %v %v", ok1, ok2)
	}
	if *p1.Input != 1 || *p2.Input != 10 {
		t.Fatalf("same id different providers = %v / %v, want 1 / 10", *p1.Input, *p2.Input)
	}
	if _, ok := cat.Lookup("opencode", "absent"); ok {
		t.Fatal("absent model should miss")
	}
	if _, ok := cat.Lookup("absent", "m"); ok {
		t.Fatal("absent provider should miss")
	}
}

// TestAggregate_SessionsSeparateFromOrchestration asserts the session lane is
// separate from orchestration/editorial and that SessionTotalTokens excludes
// reasoning.
func TestAggregate_SessionsSeparateFromOrchestration(t *testing.T) {
	ctx := context.Background()
	orchRun := RunRecord{RunID: "r1", Provenance: ProvenanceObserved,
		Calls: []CallRecord{{Category: CategoryOrchestration, Usage: KnownUsage(1000, 100, 0, 0, 0)}}}
	agg, err := BuildAggregate(ctx, "2026-10-11", []RunRecord{orchRun}, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	beforeOrch := agg.Orchestration
	beforeEditorial := agg.Editorial

	ts := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	sUsage := KnownUsage(100, 10, 5, 1, 999)
	sess := BuildSession(sessionCallRun("p", "m", sUsage, StepCollect, ts), nil, time.UTC)
	applySessions(&agg, []SessionRecord{sess})

	if !reflect.DeepEqual(agg.Orchestration, beforeOrch) {
		t.Fatalf("applySessions disturbed orchestration: %+v", agg.Orchestration)
	}
	if !reflect.DeepEqual(agg.Editorial, beforeEditorial) {
		t.Fatalf("applySessions disturbed editorial: %+v", agg.Editorial)
	}
	if agg.SessionCount != 1 || len(agg.SessionIDs) != 1 {
		t.Fatalf("session ids = %v count=%d", agg.SessionIDs, agg.SessionCount)
	}
	// reasoning (999) must be EXCLUDED from the consumed total (100+10+5+1=116).
	assertKnown(t, "session total tokens", agg.SessionTotalTokens, 116)
	if agg.SessionUsage.Reasoning.Value != 999 {
		t.Fatalf("session reasoning = %d, want 999 carried separately", agg.SessionUsage.Reasoning.Value)
	}
}

// TestStore_WriteSessionIdempotent asserts a content-hash match is a no-op and a
// missing sessions directory yields zero sessions, never an error.
func TestStore_WriteSessionIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ts := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	sess := BuildSession(sessionCallRun("p", "m", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts), nil, time.UTC)
	sess.ContentHash = "hash-a"

	wrote, err := store.WriteSession(sess)
	if err != nil || !wrote {
		t.Fatalf("first write: wrote=%v err=%v", wrote, err)
	}
	wrote, err = store.WriteSession(sess)
	if err != nil || wrote {
		t.Fatalf("second write: wrote=%v err=%v (want no-op)", wrote, err)
	}
	got, err := store.ReadSession(sess.SessionID)
	if err != nil || got.ContentHash != "hash-a" {
		t.Fatalf("read session: %+v err=%v", got, err)
	}
	list, err := store.ListSessions()
	if err != nil || len(list) != 1 {
		t.Fatalf("list sessions = %d err=%v, want 1", len(list), err)
	}

	// A missing sessions directory yields zero sessions, never an error.
	if err := os.RemoveAll(filepath.Join(dir, sessionsSubdir)); err != nil {
		t.Fatalf("remove sessions dir: %v", err)
	}
	list, err = store.ListSessions()
	if err != nil || len(list) != 0 {
		t.Fatalf("list on missing dir = %d err=%v, want 0/nil", len(list), err)
	}
}

// TestReport_SessionUnknownPriceRendersUnknown asserts the table prints "unknown"
// for an unavailable session cost/cache-hit, never "0".
func TestReport_SessionUnknownPriceRendersUnknown(t *testing.T) {
	agg := DailyAggregate{
		SchemaVersion: 1,
		Date:          "2026-10-11",
		RunIDs:        []string{},
		SessionIDs:    []string{},
		Budget:        BudgetReport{Target: 1000, Spent: workflow.UnknownMeasurement(), State: BudgetUnknownState},
	}
	var buf bytes.Buffer
	if err := RenderTable(&buf, TableInput{Aggregate: agg}); err != nil {
		t.Fatalf("render table: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("table too short:\n%s", buf.String())
	}
	header := strings.Fields(lines[0])
	row := strings.Fields(lines[1])
	idx := -1
	for i, h := range header {
		if h == "SESS-COST" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("SESS-COST column missing:\n%s", buf.String())
	}
	if idx >= len(row) {
		t.Fatalf("row shorter than header:\n%s", buf.String())
	}
	if row[idx] != "unknown" {
		t.Fatalf("session cost cell = %q, want unknown", row[idx])
	}
	if row[idx] == "0" {
		t.Fatal("unknown session cost rendered as 0")
	}
}

// TestSession_EmptyCatalogCostIsUnknownPrice (F1) asserts a catalog entry that
// EXISTS but carries no cost block (all four rates nil) is treated as an unknown
// price, never labelled `catalog`: the label is never stronger than the evidence.
func TestSession_EmptyCatalogCostIsUnknownPrice(t *testing.T) {
	ts := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cat, err := ParseModelCatalog([]byte(`{"opencode":{"models":[{"id":"m"}]}}`))
	if err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	sess := BuildSession(sessionCallRun("opencode", "m", KnownUsage(100, 10, 5, 1, 0), StepCollect, ts), cat, time.UTC)
	for name, r := range map[string]*float64{
		"input":      sess.Price.Input,
		"output":     sess.Price.Output,
		"cacheRead":  sess.Price.CacheRead,
		"cacheWrite": sess.Price.CacheWrite,
	} {
		if r != nil {
			t.Fatalf("empty-cost price %s = %v, want nil", name, *r)
		}
	}
	if sess.PriceSource != PriceSourceUnknown {
		t.Fatalf("empty-cost price source = %q, want unknown", sess.PriceSource)
	}
	if sess.Cost != nil {
		t.Fatalf("empty-cost cost = %v, want nil", *sess.Cost)
	}
	if len(sess.Models) != 1 || sess.Models[0].PriceSource != PriceSourceUnknown {
		t.Fatalf("empty-cost per-model source = %v, want one entry with unknown", sess.Models)
	}
}

// TestStore_WriteSessionReassociationIsNotNoOp (F2) asserts a re-import of the
// SAME transcript with a corrected logged date IS applied — the operator's stated
// day is never silently dropped — while a byte-identical repeat stays a no-op.
func TestStore_WriteSessionReassociationIsNotNoOp(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ts := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	sess := BuildSession(sessionCallRun("p", "m", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts), nil, time.UTC)
	sess.ContentHash = "hash-a"
	sess.LoggedDate = ""
	sess.Association = AssociationLocalStepDate

	if wrote, err := store.WriteSession(sess); err != nil || !wrote {
		t.Fatalf("first write: wrote=%v err=%v", wrote, err)
	}
	// A byte-identical repeat is still a no-op.
	if wrote, err := store.WriteSession(sess); err != nil || wrote {
		t.Fatalf("identical repeat: wrote=%v err=%v (want no-op)", wrote, err)
	}

	// The SAME content, now carrying the operator's corrected day.
	corrected := sess
	corrected.LoggedDate = "2026-10-06"
	corrected.Association = AssociationImportFlag
	wrote, err := store.WriteSession(corrected)
	if err != nil || !wrote {
		t.Fatalf("reassociation write: wrote=%v err=%v (want applied)", wrote, err)
	}
	got, err := store.ReadSession(sess.SessionID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if got.LoggedDate != "2026-10-06" || got.Association != AssociationImportFlag {
		t.Fatalf("stored session = %q/%q, want 2026-10-06/import_flag", got.LoggedDate, got.Association)
	}
	if got.ContentHash != "hash-a" {
		t.Fatalf("content hash = %q, want hash-a", got.ContentHash)
	}
}

// sessionIDList renders session ids for test failure messages.
func sessionIDList(sessions []SessionRecord) []string {
	out := make([]string, 0, len(sessions))
	for i := range sessions {
		out = append(out, sessions[i].SessionID)
	}
	return out
}

// TestAggregate_UnassociatedSessionNotFoldedIntoDay asserts a day's session lane
// contains ONLY sessions associated with that exact day: an unassociated session
// whose workflow-step calls touch the day is reported as UnassociatedSessions but
// its tokens are never folded into the day's SessionUsage (and it is never counted
// on more than one day).
func TestAggregate_UnassociatedSessionNotFoldedIntoDay(t *testing.T) {
	ctx := context.Background()
	day := "2026-10-13"

	// Associated: every workflow-step call falls on the day.
	assocUsage := KnownUsage(100, 10, 5, 1, 0)
	assoc := BuildSession(sessionCallRun("p", "m-assoc", assocUsage, StepCollect,
		time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)), nil, time.UTC)
	if assoc.LoggedDate != day || assoc.Association != AssociationLocalStepDate {
		t.Fatalf("associated session = %q/%q, want %q/local_step_date", assoc.LoggedDate, assoc.Association, day)
	}

	// Unassociated: workflow-step calls straddle local midnight, so its day cannot
	// be established but LocalDates DO include the day under test.
	straddleRun := RunRecord{
		RunID: "r-straddle", SessionID: "s-straddle", Provider: "p", Provenance: ProvenanceObserved,
		Usage: KnownUsage(7, 3, 0, 0, 0),
		Calls: []CallRecord{
			{Provider: "p", Model: "m-straddle", Step: StepCollect, Timestamp: time.Date(2026, 10, 12, 23, 0, 0, 0, time.UTC), Usage: KnownUsage(7, 3, 0, 0, 0)},
			{Provider: "p", Model: "m-straddle", Step: StepReview, Timestamp: time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC), Usage: KnownUsage(0, 0, 0, 0, 0)},
		},
	}
	straddle := BuildSession(straddleRun, nil, time.UTC)
	if straddle.LoggedDate != "" || straddle.Association != AssociationUnknown {
		t.Fatalf("straddle = %q/%q, want empty/unknown", straddle.LoggedDate, straddle.Association)
	}
	if !containsDate(straddle.LocalDates, day) {
		t.Fatalf("straddle LocalDates = %v, must include %s", straddle.LocalDates, day)
	}

	sessions := []SessionRecord{assoc, straddle}

	// The selection contains ONLY the associated session.
	selected := sessionsForDate(sessions, day)
	if len(selected) != 1 || selected[0].SessionID != assoc.SessionID {
		t.Fatalf("sessionsForDate(%s) = %v, want only [%s]", day, sessionIDList(selected), assoc.SessionID)
	}

	// The day's aggregate keeps the unassociated session VISIBLE but never folds
	// its tokens into the day's lane.
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	for _, s := range sessions {
		if _, err := store.WriteSession(s); err != nil {
			t.Fatalf("write session %s: %v", s.SessionID, err)
		}
	}
	agg, err := store.Aggregate(ctx, day, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.UnassociatedSessions != 1 {
		t.Fatalf("unassociated sessions = %d, want 1", agg.UnassociatedSessions)
	}
	if agg.SessionCount != 1 || len(agg.SessionIDs) != 1 || agg.SessionIDs[0] != assoc.SessionID {
		t.Fatalf("day lane ids = %v count=%d, want only [%s]", agg.SessionIDs, agg.SessionCount, assoc.SessionID)
	}
	if !reflect.DeepEqual(agg.SessionUsage, assoc.Usage) {
		t.Fatalf("day SessionUsage = %+v, want the associated session's %+v (straddle excluded)", agg.SessionUsage, assoc.Usage)
	}
	// 100+10+5+1 = 116; the straddle's 7+3 must not appear.
	assertKnown(t, "day session total tokens", agg.SessionTotalTokens, 116)
}
