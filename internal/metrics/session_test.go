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

// TestSession_ModelSourceObservedOnlyWhenCarried (D3/AC3) asserts the model source
// label: a transcript that carries a model is observed; an empty model is NEVER
// observed (antigravity carries none).
func TestSession_ModelSourceObservedOnlyWhenCarried(t *testing.T) {
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

	carried := BuildSession(sessionCallRun("pi", "m1", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts), nil, time.UTC)
	if carried.ModelSource != ModelSourceObserved {
		t.Fatalf("model source = %q, want observed", carried.ModelSource)
	}

	// A resumed session whose model appears only on its calls is still observed.
	callOnly := sessionCallRun("pi", "m1", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts)
	callOnly.Model = ""
	if got := BuildSession(callOnly, nil, time.UTC).ModelSource; got != ModelSourceObserved {
		t.Fatalf("call-level model source = %q, want observed", got)
	}

	modeless := sessionCallRun("antigravity", "", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts)
	modeless.Model = ""
	if got := BuildSession(modeless, nil, time.UTC).ModelSource; got != ModelSourceUnknown {
		t.Fatalf("model-less session source = %q, want unknown (an empty model is never observed)", got)
	}
}

// TestDeclareSessionModel_DeclaredStampsPriceByProviderAndModel (D3/AC3) asserts a
// declaration stamps the model, re-resolves the price by (provider, model) ONLY, and
// labels the source declared.
func TestDeclareSessionModel_DeclaredStampsPriceByProviderAndModel(t *testing.T) {
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	cat, err := ParseModelCatalog([]byte(`{
		"pi":   {"models":[{"id":"costed","cost":{"input":1,"output":2,"cacheRead":0.5,"cacheWrite":0}}]},
		"other":{"models":[{"id":"costed","cost":{"input":9,"output":9,"cacheRead":9,"cacheWrite":9}}]}
	}`))
	if err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	modeless := sessionCallRun("pi", "", KnownUsage(100, 10, 5, 1, 0), StepCollect, ts)
	modeless.Model = ""
	sess := BuildSession(modeless, cat, time.UTC)
	if sess.ModelSource != ModelSourceUnknown || sess.PriceSource != PriceSourceUnknown {
		t.Fatalf("pre-declaration = %q/%q, want unknown/unknown", sess.ModelSource, sess.PriceSource)
	}

	if !DeclareSessionModel(&sess, "costed", cat) {
		t.Fatal("declaration was refused")
	}
	if sess.Model != "costed" || sess.ModelSource != ModelSourceDeclared {
		t.Fatalf("declared model = %q/%q", sess.Model, sess.ModelSource)
	}
	if sess.PriceSource != PriceSourceCatalog {
		t.Fatalf("price source = %q, want catalog", sess.PriceSource)
	}
	// The pi-provider price (1/2/0.5/0), NOT the same model id under "other".
	if sess.Price.Input == nil || *sess.Price.Input != 1 {
		t.Fatalf("price input = %v, want the pi entry's 1 (provider-keyed lookup)", sess.Price.Input)
	}
	// 100*1 + 10*2 + 5*0.5 + 1*0 = 122.5 / 1e6
	if sess.Cost == nil || *sess.Cost != 0.0001225 {
		t.Fatalf("cost = %v, want 0.0001225", sess.Cost)
	}
	if len(sess.Models) != 1 || sess.Models[0].Model != "costed" || sess.Models[0].PriceSource != PriceSourceCatalog {
		t.Fatalf("per-model declaration not applied: %+v", sess.Models)
	}
}

// TestDeclareSessionModel_NoModelOnlyFallback (D3/AC3) asserts the lookup is
// (provider, model): a model listed only under ANOTHER provider is unknown, and the
// cost is nil — never 0.
func TestDeclareSessionModel_NoModelOnlyFallback(t *testing.T) {
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	cat, err := ParseModelCatalog([]byte(`{"other":{"models":[{"id":"costed","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}`))
	if err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	modeless := sessionCallRun("pi", "", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts)
	modeless.Model = ""
	sess := BuildSession(modeless, cat, time.UTC)
	if !DeclareSessionModel(&sess, "costed", cat) {
		t.Fatal("declaration was refused")
	}
	if sess.PriceSource != PriceSourceUnknown || sess.Cost != nil {
		t.Fatalf("price/cost = %q/%v, want unknown/nil (no model-only fallback)", sess.PriceSource, sess.Cost)
	}
	if sess.Price.Input != nil {
		t.Fatalf("price input = %v, want nil", *sess.Price.Input)
	}
}

// TestDeclareSessionModel_ObservedOutranksDeclared (D3/AC3) asserts a transcript's
// own model is NEVER overridden by a declaration, AND that the run-level Provenance
// (the `[declared schema]` axis) is untouched by a model declaration.
func TestDeclareSessionModel_ObservedOutranksDeclared(t *testing.T) {
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	observed := BuildSession(sessionCallRun("pi", "observed-model", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts), nil, time.UTC)
	if DeclareSessionModel(&observed, "declared-model", nil) {
		t.Fatal("a declaration must NOT override an observed model")
	}
	if observed.Model != "observed-model" || observed.ModelSource != ModelSourceObserved {
		t.Fatalf("observed model changed: %q/%q", observed.Model, observed.ModelSource)
	}

	// Blank declarations are refused too.
	if DeclareSessionModel(&observed, "   ", nil) {
		t.Fatal("a blank declaration must be refused")
	}

	// The run-level provenance is a DIFFERENT axis: a declared-schema (claude) run
	// stays declared after a model declaration, so the report still prints the marker.
	claude := sessionCallRun("claude", "", KnownUsage(10, 1, 0, 0, 0), StepCollect, ts)
	claude.Model = ""
	claude.Provenance = ProvenanceDeclared
	sess := BuildSession(claude, nil, time.UTC)
	if !DeclareSessionModel(&sess, "declared-model", nil) {
		t.Fatal("declaration on a declared-schema run was refused")
	}
	if sess.Provenance != ProvenanceDeclared {
		t.Fatalf("run-level provenance = %q, want declared (a DIFFERENT axis, never changed)", sess.Provenance)
	}
}

// TestDeclareSessionTotal_SynthesizedLowerBound (D3 owner extension) asserts the
// declared total is the sum of each call's KNOWN components — a lower bound — while
// the call records keep their per-component unknowns exactly as parsed.
func TestDeclareSessionTotal_SynthesizedLowerBound(t *testing.T) {
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	// The antigravity shape: no cache-write counter, no reasoning count.
	u := KnownUsage(2740, 730, 1550, 0, 0)
	u.CacheWrite = workflow.UnknownMeasurement()
	u.Reasoning = workflow.UnknownMeasurement()
	run := sessionCallRun("antigravity", "", u, StepCollect, ts)
	run.Model = ""
	run.Usage = u

	sess := BuildSession(run, nil, time.UTC)
	if sess.TotalTokens.Known {
		t.Fatalf("pre-declaration total = %+v, want UNKNOWN", sess.TotalTokens)
	}
	if sess.TotalTokensSource != TotalTokensSourceUnknown {
		t.Fatalf("pre-declaration total source = %q, want unknown", sess.TotalTokensSource)
	}
	if !DeclareSessionTotal(&sess, run) {
		t.Fatal("declared total was refused")
	}
	assertKnown(t, "synthesized total", sess.TotalTokens, 2740+730+1550)
	if sess.TotalTokensSource != TotalTokensSourceSynthesized {
		t.Fatalf("total source = %q, want synthesized", sess.TotalTokensSource)
	}
	// The per-call components stay AS PARSED: the synthesis never invents a
	// cache-write or reasoning counter.
	if run.Calls[0].Usage.CacheWrite.Known || run.Calls[0].Usage.Reasoning.Known {
		t.Fatalf("call components were rewritten: %+v", run.Calls[0].Usage)
	}
	// ConsumedTokens is UNCHANGED by the declaration: a synthesized session total
	// never becomes an observed component sum.
	if run.Calls[0].Usage.ConsumedTokens().Known {
		t.Fatal("ConsumedTokens must stay UNKNOWN (the components are unchanged)")
	}
	// The declared TOTAL never makes the per-model totals observed.
	for _, m := range sess.Models {
		if m.TotalTokens.Known {
			t.Fatalf("per-model total = %+v, want UNKNOWN (only the session total is synthesized)", m.TotalTokens)
		}
	}
}

// TestDeclareSessionTotal_ObservedWins (D3 owner extension) asserts an observed
// total is never downgraded: the declaration is refused and the figure is unchanged.
// The same rule makes a re-import with REAL totals win over a stored synthesized one.
func TestDeclareSessionTotal_ObservedWins(t *testing.T) {
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	run := sessionCallRun("pi", "m", KnownUsage(100, 10, 5, 1, 0), StepCollect, ts)
	sess := BuildSession(run, nil, time.UTC)
	if sess.TotalTokensSource != TotalTokensSourceObserved {
		t.Fatalf("total source = %q, want observed", sess.TotalTokensSource)
	}
	if DeclareSessionTotal(&sess, run) {
		t.Fatal("a declaration must NOT downgrade an observed total")
	}
	assertKnown(t, "observed total", sess.TotalTokens, 116)
	if sess.TotalTokensSource != TotalTokensSourceObserved {
		t.Fatalf("total source = %q, want observed", sess.TotalTokensSource)
	}

	// Nothing known anywhere: no synthesized 0 is ever stamped.
	unknown := sessionCallRun("antigravity", "", UnknownUsage(), StepCollect, ts)
	unknown.Usage = UnknownUsage()
	us := BuildSession(unknown, nil, time.UTC)
	if DeclareSessionTotal(&us, unknown) {
		t.Fatal("a fully unknown run must not be stamped with a synthesized 0")
	}
	if us.TotalTokens.Known {
		t.Fatalf("total = %+v, want UNKNOWN", us.TotalTokens)
	}
}

// TestStore_WriteSessionDeclarationTerms (D3/AC3) asserts the idempotency decision
// carries the four declaration terms: a NEW declaration rewrites ONCE, an identical
// repeat no-ops, and an OBSERVED build wins over a stored synthesized record.
func TestStore_WriteSessionDeclarationTerms(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	u := KnownUsage(100, 10, 5, 0, 0)
	u.CacheWrite = workflow.UnknownMeasurement()
	run := sessionCallRun("antigravity", "", u, StepCollect, ts)
	run.Model = ""
	run.Usage = u
	run.ContentHash = "hash-same"

	// 1. Stored SYNTHESIZED, model declared.
	declared := BuildSession(run, nil, time.UTC)
	declared.ContentHash = "hash-same"
	declared.LoggedDate = "2026-10-16"
	declared.Association = AssociationImportFlag
	if !DeclareSessionModel(&declared, "declared-model", nil) {
		t.Fatal("model declaration refused")
	}
	if !DeclareSessionTotal(&declared, run) {
		t.Fatal("total declaration refused")
	}
	if wrote, err := store.WriteSession(declared); err != nil || !wrote {
		t.Fatalf("first write: wrote=%v err=%v", wrote, err)
	}
	if wrote, err := store.WriteSession(declared); err != nil || wrote {
		t.Fatalf("identical repeat: wrote=%v err=%v (want no-op)", wrote, err)
	}

	// 2. A NEW declaration rewrites (the terms force it), then no-ops.
	redeclared := declared
	if !DeclareSessionModel(&redeclared, "other-model", nil) {
		t.Fatal("re-declaration refused")
	}
	if wrote, err := store.WriteSession(redeclared); err != nil || !wrote {
		t.Fatalf("re-declaration write: wrote=%v err=%v (want applied)", wrote, err)
	}
	if wrote, err := store.WriteSession(redeclared); err != nil || wrote {
		t.Fatalf("repeat after re-declaration: wrote=%v err=%v (want no-op)", wrote, err)
	}

	// 3. An OBSERVED build of the same session wins over the stored synthesized one.
	observedRun := sessionCallRun("pi", "declared-model", KnownUsage(100, 10, 5, 1, 0), StepCollect, ts)
	observed := BuildSession(observedRun, nil, time.UTC)
	observed.ContentHash = "hash-new"
	observed.LoggedDate = "2026-10-16"
	observed.Association = AssociationImportFlag
	if observed.TotalTokensSource != TotalTokensSourceObserved {
		t.Fatalf("observed build source = %q", observed.TotalTokensSource)
	}
	wrote, err := store.WriteSession(observed)
	if err != nil || !wrote {
		t.Fatalf("observed re-import: wrote=%v err=%v (want applied)", wrote, err)
	}
	got, err := store.ReadSession(observed.SessionID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if got.TotalTokensSource != TotalTokensSourceObserved {
		t.Fatalf("stored total source = %q, want observed (observed outranks synthesized)", got.TotalTokensSource)
	}
	assertKnown(t, "stored observed total", got.TotalTokens, 116)
}

// TestSynthesizedSessionTotal_ExcludesReasoning (F1) asserts the synthesized total
// sums EXACTLY the four components ConsumedTokens sums. Reasoning is not a consumed
// component, so counting it would break the "lower bound" claim. The reviewer's
// probe: input 1000, output 500, cache-read 0, reasoning 700, cache-write UNKNOWN
// => 1500, never 2200.
func TestSynthesizedSessionTotal_ExcludesReasoning(t *testing.T) {
	u := KnownUsage(1000, 500, 0, 0, 700)
	u.CacheWrite = workflow.UnknownMeasurement()
	got := SynthesizedSessionTotal([]CallRecord{{Usage: u}})
	assertKnown(t, "synthesized total (cache-write unknown)", got, 1500)
	if got.Value == 2200 {
		t.Fatal("reasoning (700) was summed into the synthesized total: 2200 is not a lower bound of the consumed total")
	}

	// With all four consumed components known, the synthesized figure EQUALS
	// ConsumedTokens — the same quantity, minus the reasoning column.
	full := KnownUsage(1000, 500, 250, 100, 700)
	assertKnown(t, "ConsumedTokens", full.ConsumedTokens(), 1850)
	assertKnown(t, "synthesized total (all four known)", SynthesizedSessionTotal([]CallRecord{{Usage: full}}), 1850)

	// Reasoning ALONE known is not a measurement of consumption: no synthesized 0.
	reasoningOnly := UnknownUsage()
	reasoningOnly.Reasoning = workflow.KnownMeasurement(700)
	if got := SynthesizedSessionTotal([]CallRecord{{Usage: reasoningOnly}}); got.Known {
		t.Fatalf("reasoning-only usage synthesized %+v, want UNKNOWN (no consumed component is known)", got)
	}
}

// TestEffectiveTotalTokensSource_LegacyNormalization (F10) asserts the ONE
// normalization of a legacy record's EMPTY total_source: observed-when-known, else
// unknown. A legacy observed total can therefore never be downgraded to synthesized
// by a later --declare-total.
func TestEffectiveTotalTokensSource_LegacyNormalization(t *testing.T) {
	ts := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	run := sessionCallRun("pi", "m", KnownUsage(100, 10, 5, 1, 0), StepCollect, ts)

	legacyObserved := SessionRecord{TotalTokens: workflow.KnownMeasurement(116)}
	if got := EffectiveTotalTokensSource(legacyObserved); got != TotalTokensSourceObserved {
		t.Fatalf("legacy known total effective source = %q, want observed", got)
	}
	if DeclareSessionTotal(&legacyObserved, run) {
		t.Fatal("a legacy OBSERVED total was downgraded to synthesized")
	}
	assertKnown(t, "legacy observed total after a refused declaration", legacyObserved.TotalTokens, 116)
	if legacyObserved.TotalTokensSource != "" {
		t.Fatalf("a refused declaration must change nothing: source = %q", legacyObserved.TotalTokensSource)
	}

	legacyUnknown := SessionRecord{TotalTokens: workflow.UnknownMeasurement()}
	if got := EffectiveTotalTokensSource(legacyUnknown); got != TotalTokensSourceUnknown {
		t.Fatalf("legacy unknown total effective source = %q, want unknown", got)
	}
	// A legacy UNKNOWN total stays declarable (it was never a measurement).
	if !DeclareSessionTotal(&legacyUnknown, run) {
		t.Fatal("a legacy unknown total was refused a declaration")
	}
	if legacyUnknown.TotalTokensSource != TotalTokensSourceSynthesized {
		t.Fatalf("declared legacy total source = %q, want synthesized", legacyUnknown.TotalTokensSource)
	}

	// An explicit source is passed through unchanged on every arm.
	for _, src := range []TotalTokensSource{TotalTokensSourceObserved, TotalTokensSourceSynthesized, TotalTokensSourceUnknown} {
		if got := EffectiveTotalTokensSource(SessionRecord{TotalTokensSource: src}); got != src {
			t.Fatalf("explicit source %q normalized to %q", src, got)
		}
	}
}

// TestEffectiveModelSource_LegacyNormalization (R2-5) asserts the model-axis twin of
// EffectiveTotalTokensSource: an empty source with a real model reads as observed, an
// empty source with no model reads as unknown, and an explicit source passes through.
func TestEffectiveModelSource_LegacyNormalization(t *testing.T) {
	if got := EffectiveModelSource(SessionRecord{Model: "gemini-pro"}); got != ModelSourceObserved {
		t.Fatalf("legacy record with a model: effective source = %q, want observed", got)
	}
	if got := EffectiveModelSource(SessionRecord{}); got != ModelSourceUnknown {
		t.Fatalf("legacy record with no model: effective source = %q, want unknown", got)
	}
	for _, src := range []ModelSource{ModelSourceObserved, ModelSourceDeclared, ModelSourceUnknown} {
		if got := EffectiveModelSource(SessionRecord{ModelSource: src, Model: "m"}); got != src {
			t.Fatalf("explicit source %q normalized to %q", src, got)
		}
	}

	// The declare glue reads it too: a legacy OBSERVED model is never overwritten.
	legacy := SessionRecord{Model: "gemini-pro"}
	if DeclareSessionModel(&legacy, "declared-model", nil) {
		t.Fatal("a legacy observed model was downgraded by a declaration")
	}
	if legacy.Model != "gemini-pro" || legacy.ModelSource != "" {
		t.Fatalf("a refused declaration must change nothing: %q/%q", legacy.Model, legacy.ModelSource)
	}
}
