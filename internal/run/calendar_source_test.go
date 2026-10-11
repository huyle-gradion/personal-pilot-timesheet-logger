package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
)

// runCalendarICS is a minimal ICS fixture with one accepted meeting on the
// target day, so a resolved calendar source yields exactly one meeting evidence.
const runCalendarICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:standup@test
DTSTART:20261006T100000Z
DTEND:20261006T110000Z
STATUS:CONFIRMED
SUMMARY:Standup
ATTENDEE;PARTSTAT=ACCEPTED;CN=Huy:mailto:huy@example.com
END:VEVENT
END:VCALENDAR`

// calendarCapture builds a CalendarPlugin whose fetcher records the cfg it
// receives and returns fixture bytes only when a resolved source is present
// (mirroring DefaultCalendarFetch's live contract).
func calendarCapture(captured *evidence.SourceConfig, calls *int, data string) *plugins.CalendarPlugin {
	return &plugins.CalendarPlugin{Fetch: func(_ context.Context, cfg evidence.SourceConfig, _ evidence.CollectRequest) ([]byte, error) {
		*calls++
		*captured = cfg
		if strings.TrimSpace(cfg.Config[plugins.CalendarConfigSource]) == "" {
			return nil, errors.New("calendar: no resolved source configured")
		}
		return []byte(data), nil
	}}
}

func calendarSourceConfig() evidence.SourceConfig {
	return evidence.SourceConfig{
		ID: "cal", Type: evidence.SourceCalendar, Enabled: true,
		Config: map[string]string{plugins.CalendarConfigURLRef: "keychain:calendar-feed"},
	}
}

// TestCollect_ResolvesCalendarSourceFromKeychain proves an enabled calendar
// source's url_ref keychain reference is resolved IN MEMORY into Config["source"]
// immediately before collection, so the fetcher receives the resolved ICS URL and
// the source yields meeting evidence.
func TestCollect_ResolvesCalendarSourceFromKeychain(t *testing.T) {
	const icsURL = "https://calendar.example/secret-feed.ics"
	store := newStore(t)
	var got evidence.SourceConfig
	var calls int
	reg := evidence.NewRegistry()
	reg.Register(calendarCapture(&got, &calls, runCalendarICS))

	r := newRunner(t, Deps{
		Store:    store,
		Registry: reg,
		Keychain: &fakeKeychain{values: map[string]string{"calendar-feed": icsURL}},
	}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{calendarSourceConfig()}}})

	res, err := r.Collect(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if calls != 1 {
		t.Fatalf("want exactly one fetch, got %d", calls)
	}
	if gotSrc := strings.TrimSpace(got.Config[plugins.CalendarConfigSource]); gotSrc != icsURL {
		t.Fatalf("the fetcher must receive the resolved ICS URL, got %q", gotSrc)
	}
	if len(res.Evidence) != 1 || res.Evidence[0].Kind != evidence.SourceCalendar {
		t.Fatalf("a resolved calendar source must yield one meeting evidence, got %+v", res.Evidence)
	}
	if !res.Summary.Complete {
		t.Fatalf("a resolved calendar source must be complete, got %+v", res.Summary)
	}
}

// TestCollect_CalendarKeychainNotFoundIsIncompleteNotFatal proves a NotFound
// keychain resolution leaves the source UNSET, the calendar source reports
// incomplete/SOURCE_ERROR with no events, requires acknowledgment, and does NOT
// abort the run.
func TestCollect_CalendarKeychainNotFoundIsIncompleteNotFatal(t *testing.T) {
	store := newStore(t)
	var got evidence.SourceConfig
	var calls int
	reg := evidence.NewRegistry()
	reg.Register(calendarCapture(&got, &calls, runCalendarICS))

	// An empty fake keychain returns NotFound for the referenced service.
	r := newRunner(t, Deps{Store: store, Registry: reg, Keychain: &fakeKeychain{}},
		Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{calendarSourceConfig()}}})

	res, err := r.Collect(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("a missing secret must not fail the run, got %v", err)
	}
	if gotSrc := strings.TrimSpace(got.Config[plugins.CalendarConfigSource]); gotSrc != "" {
		t.Fatalf("the fetcher must see an EMPTY source, got %q", gotSrc)
	}
	if len(res.Evidence) != 0 {
		t.Fatalf("an unresolved calendar source must yield no events, got %+v", res.Evidence)
	}
	if res.Summary.Complete {
		t.Fatal("an unresolved calendar source must not be complete")
	}
	sr := res.Summary.Sources[0]
	if sr.Status != evidence.StatusIncomplete || sr.ErrorCode != evidence.CodeSourceError {
		t.Fatalf("want %s/%s, got %+v", evidence.StatusIncomplete, evidence.CodeSourceError, sr)
	}
	if ackErr := evidence.RequireAcknowledgment(res.Summary, nil); ackErr == nil {
		t.Fatal("an incomplete calendar source must require explicit acknowledgment")
	}
}

// TestCollect_DoesNotMutateRunnerConfig proves resolution works on a COPY: after
// Collect the original source config still has url_ref and NO resolved source.
func TestCollect_DoesNotMutateRunnerConfig(t *testing.T) {
	const icsURL = "https://calendar.example/secret-feed.ics"
	store := newStore(t)
	var got evidence.SourceConfig
	var calls int
	reg := evidence.NewRegistry()
	reg.Register(calendarCapture(&got, &calls, runCalendarICS))

	orig := map[string]string{plugins.CalendarConfigURLRef: "keychain:calendar-feed"}
	sources := []evidence.SourceConfig{{ID: "cal", Type: evidence.SourceCalendar, Enabled: true, Config: orig}}
	r := newRunner(t, Deps{
		Store: store, Registry: reg,
		Keychain: &fakeKeychain{values: map[string]string{"calendar-feed": icsURL}},
	}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: sources}})

	if _, err := r.Collect(ctx, "2026-10-06"); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	cfg := r.cfg.Evidence.Sources[0].Config
	if cfg[plugins.CalendarConfigURLRef] != "keychain:calendar-feed" {
		t.Fatalf("the url_ref reference must be preserved, got %q", cfg[plugins.CalendarConfigURLRef])
	}
	if _, ok := cfg[plugins.CalendarConfigSource]; ok {
		t.Fatal("the runner's config must NOT gain a resolved source (a copy is resolved)")
	}
	if _, ok := orig[plugins.CalendarConfigSource]; ok {
		t.Fatal("the caller's config map must NOT be mutated")
	}
}

// TestCollect_ResolvedSecretNeverPersisted proves the resolved ICS URL (a secret)
// appears in NO marshaled record, export, warning, or error text.
func TestCollect_ResolvedSecretNeverPersisted(t *testing.T) {
	const sentinel = "SECRET-ICS-URL-DO-NOT-LEAK-7b21"
	store := newStore(t)
	var got evidence.SourceConfig
	var calls int
	reg := evidence.NewRegistry()
	reg.Register(calendarCapture(&got, &calls, runCalendarICS))
	exp := &fakeExporter{}
	r := newRunner(t, Deps{
		Store: store, Registry: reg, Exporter: exp,
		Keychain: &fakeKeychain{values: map[string]string{"calendar-feed": sentinel}},
	}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{calendarSourceConfig()}}})

	res, err := r.Collect(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res.Evidence) != 1 {
		t.Fatalf("the sentinel source must have fetched the fixture, got %+v", res.Evidence)
	}
	exportRes, err := r.Export(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	rec, err := store.GetDay(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	blob := mustJSON(t, rec) + mustJSON(t, res) + mustJSON(t, exp.last) + mustJSON(t, exportRes)
	for _, sr := range res.Summary.Sources {
		blob += strings.Join(sr.Warnings, "\n")
	}
	if strings.Contains(blob, sentinel) {
		t.Fatalf("the resolved secret leaked into persisted output: %s", blob)
	}

	// The resolver's ERROR text is another sink: a failing resolution whose error
	// embeds the sentinel must not surface it either. The source reports only the
	// stable SOURCE_ERROR code; the raw resolver error is discarded by the step and
	// by the registry (which never surfaces raw error text).
	errR := newRunner(t, Deps{
		Store: newStore(t), Registry: reg,
		Keychain: &fakeKeychain{err: errors.New("resolver failed: " + sentinel)},
	}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: []evidence.SourceConfig{calendarSourceConfig()}}})

	errRes, err := errR.Collect(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("a resolution error must not fail the run: %v", err)
	}
	errBlob := mustJSON(t, errRes)
	for _, sr := range errRes.Summary.Sources {
		errBlob += strings.Join(sr.Warnings, "\n")
	}
	if strings.Contains(errBlob, sentinel) {
		t.Fatalf("the resolver error must not leak the secret: %s", errBlob)
	}
	if got := errRes.Summary.Sources[0]; got.ErrorCode != evidence.CodeSourceError {
		t.Fatalf("want a stable %s code, got %+v", evidence.CodeSourceError, got)
	}
}

// TestCollect_ResolutionLeavesUnrelatedSourcesAlone proves collection is offline
// (nil Remote) and a NON-calendar source carrying a keychain: value is not
// resolved: it still collects and its config is passed through unchanged.
func TestCollect_ResolutionLeavesUnrelatedSourcesAlone(t *testing.T) {
	const icsURL = "https://calendar.example/secret-feed.ics"
	store := newStore(t)
	var gotCal evidence.SourceConfig
	var calCalls int
	git := &capPlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{gitEvidence("g1", "acme/repo", "local work")}}
	reg := evidence.NewRegistry()
	reg.Register(calendarCapture(&gotCal, &calCalls, runCalendarICS))
	reg.Register(git)

	gitCfg := map[string]string{"some_ref": "keychain:not-calendar"}
	sources := []evidence.SourceConfig{
		{ID: "git", Type: evidence.SourceLocalGit, Enabled: true, Config: gitCfg},
		calendarSourceConfig(),
	}
	// Deps.Remote is nil: offline. The calendar ICS fetch is a SOURCE fetch and
	// must still happen; only the Gradion remote is skipped.
	r := newRunner(t, Deps{
		Store: store, Registry: reg,
		Keychain: &fakeKeychain{values: map[string]string{"calendar-feed": icsURL}},
	}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: sources}})

	res, err := r.Collect(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !res.Summary.Complete {
		t.Fatalf("offline with resolvable sources must be complete, got %+v", res.Summary)
	}
	if !strings.Contains(mustJSON(t, res.Evidence), "g1") {
		t.Fatal("the unrelated git source must still collect its evidence")
	}
	if _, ok := git.got.Config[plugins.CalendarConfigSource]; ok {
		t.Fatal("a non-calendar source must NOT receive a resolved source")
	}
	if git.got.Config["some_ref"] != "keychain:not-calendar" {
		t.Fatalf("a non-calendar source's config must pass through unchanged, got %+v", git.got.Config)
	}
	if gotSrc := strings.TrimSpace(gotCal.Config[plugins.CalendarConfigSource]); gotSrc != icsURL {
		t.Fatalf("the calendar source must still resolve offline, got %q", gotSrc)
	}
}

// capPlugin is a minimally-instrumented plugin that captures the cfg it receives.
type capPlugin struct {
	kind evidence.SourceKind
	ev   []evidence.Evidence
	got  evidence.SourceConfig
}

func (p *capPlugin) Type() evidence.SourceKind { return p.kind }
func (p *capPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return nil
}
func (p *capPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{p.kind}
}
func (p *capPlugin) CheckContract(context.Context, evidence.SourceConfig) []evidence.ContractCheck {
	return nil
}
func (p *capPlugin) Collect(_ context.Context, cfg evidence.SourceConfig, _ evidence.CollectRequest) (evidence.CollectResult, error) {
	p.got = cfg
	return evidence.CollectResult{
		Evidence: p.ev,
		Result:   evidence.SourceResult{Status: evidence.StatusComplete, EvidenceCount: len(p.ev)},
	}, nil
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
