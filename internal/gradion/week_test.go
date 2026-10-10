package gradion

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
)

// weekFake declares timesheet__get_my_week so the client uses the structured week
// path (the verified live contract).
func weekFake(t *testing.T, onCall func(name string, args map[string]any) callOutcome) *Client {
	t.Helper()
	f := newFakeMCP()
	f.tools = []string{ToolListEntries, ToolGetMyWeek, ToolLogTime, ToolEditTime, ToolGetClassifications}
	f.onCall = onCall
	return newTestClient(t, f)
}

func weekEnvelope(days ...map[string]any) callOutcome {
	rb, _ := json.Marshal(map[string]any{
		"days": days, "weekStart": "2026-10-05",
		"minDate": "2026-01-01", "maxDate": "2026-12-31", "today": "2026-10-06",
	})
	return structured(rb)
}

func weekDayFixture(date string, entries ...map[string]any) map[string]any {
	return map[string]any{"date": date, "entries": entries}
}

func weekEntryFixture(id, start, end, class, desc, task string) map[string]any {
	return map[string]any{
		"id": id, "startTime": start, "endTime": end, "classificationId": class,
		"classificationName": "Proj", "description": desc, "durationMinutes": 60,
		"status": "confirmed", "task": task, "activityTypeLabel": "Work",
	}
}

// The reference envelope {resources:[...]} is the live list_my_entries shape and
// must NOT be parsed as week data.
func TestListEntries_ReferenceEnvelopeIsNotWeekData(t *testing.T) {
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return structured(json.RawMessage(`{"resources":[{"id":"e1","kind":"entry","label":"x"}]}`))
	})
	_, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-06", 0)
	if CodeOf(err) != CodeReadSchema {
		t.Fatalf("an unrecognized retrieval must fail closed, got %v", err)
	}
}

func TestListEntries_WeekEnvelopeMapsAllFields(t *testing.T) {
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return weekEnvelope(weekDayFixture("2026-10-06", weekEntryFixture("e1", "09:00", "10:00", "cls-1", "work", "task-1")))
	})
	l, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-06", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if !l.Complete || l.Truncated != 0 || l.Duplicates != 0 {
		t.Fatalf("want a complete listing, got %+v", l)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(l.Entries))
	}
	e := l.Entries[0]
	if e.RemoteID != "e1" || e.Date != "2026-10-06" || e.StartTime != "09:00" || e.EndTime != "10:00" ||
		e.ClassificationID != "cls-1" || e.Description != "work" || e.Task != "task-1" {
		t.Fatalf("unexpected mapping: %+v", e)
	}
	if e.Billable != nil {
		t.Errorf("billable must be unknown (the week shape does not expose it)")
	}
	if e.ObservedAt.IsZero() {
		t.Errorf("observed_at must be set")
	}
	if l.Pages != 1 {
		t.Errorf("want 1 week call, got %d", l.Pages)
	}
}

func TestListEntries_DateRangeFilteringAcrossWeeks(t *testing.T) {
	week := 0
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		week++
		if week == 1 {
			return weekEnvelope(
				weekDayFixture("2026-10-05", weekEntryFixture("a", "09:00", "10:00", "c", "before", "")),
				weekDayFixture("2026-10-06", weekEntryFixture("b", "09:00", "10:00", "c", "in-range", "")),
			)
		}
		return weekEnvelope(
			weekDayFixture("2026-10-12", weekEntryFixture("c", "09:00", "10:00", "c", "in-range-2", "")),
			weekDayFixture("2026-10-13", weekEntryFixture("d", "09:00", "10:00", "c", "after", "")),
		)
	})
	l, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-12", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if !l.Complete || l.Pages != 2 {
		t.Fatalf("want a complete 2-week listing, got %+v", l)
	}
	if len(l.Entries) != 2 || l.Entries[0].RemoteID != "b" || l.Entries[1].RemoteID != "c" {
		t.Fatalf("want only the in-range entries b,c, got %+v", l.Entries)
	}
}

func TestListEntries_DuplicateIDsMarkIncomplete(t *testing.T) {
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return weekEnvelope(
			weekDayFixture("2026-10-06", weekEntryFixture("dup", "09:00", "10:00", "c", "one", "")),
			weekDayFixture("2026-10-07", weekEntryFixture("dup", "09:00", "10:00", "c", "two", "")),
		)
	})
	l, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-07", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if l.Duplicates != 1 || l.Complete {
		t.Fatalf("a duplicate id must mark the listing incomplete, got %+v", l)
	}
	if len(l.Entries) != 1 {
		t.Fatalf("want the first id only, got %d", len(l.Entries))
	}
}

func TestListEntries_BoundHitMarksIncomplete(t *testing.T) {
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return weekEnvelope(weekDayFixture("2026-10-06", weekEntryFixture("e1", "09:00", "10:00", "c", "work", "")))
	})
	c.maxEntries = 1 // force a bound hit on the second week

	l, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-20", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if l.Truncated != 1 || l.Complete {
		t.Fatalf("a bound hit must mark the listing incomplete, got %+v", l)
	}
}

// F1: the entry bound is HARD — a single large week must not append past
// maxEntries and still report Complete.
func TestListEntries_WeekEntryBoundIsHard(t *testing.T) {
	var entries []map[string]any
	for i := 0; i < 5; i++ {
		entries = append(entries, weekEntryFixture("e"+strconv.Itoa(i), "09:00", "10:00", "c", "work", ""))
	}
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return weekEnvelope(weekDayFixture("2026-10-06", entries...))
	})
	c.maxEntries = 3

	l, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-06", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(l.Entries) != 3 {
		t.Fatalf("want exactly 3 entries (the bound), got %d", len(l.Entries))
	}
	if l.Truncated != 1 || l.Complete {
		t.Fatalf("a hard bound hit must mark the listing incomplete, got %+v", l)
	}
}

// F2: the cursor advance is clamped to +7 days, so a response whose days span
// wider than a week can never skip un-scanned weeks.
func TestListEntries_CursorNeverSkipsWeeks(t *testing.T) {
	var weekStarts []string
	c := weekFake(t, func(name string, args map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		ws, _ := args["weekStart"].(string)
		weekStarts = append(weekStarts, ws)
		if len(weekStarts) == 1 {
			// A bogus wide span: days 10-06 .. 10-16 (10 days).
			return weekEnvelope(
				weekDayFixture("2026-10-06", weekEntryFixture("a", "09:00", "10:00", "c", "w1", "")),
				weekDayFixture("2026-10-16", weekEntryFixture("b", "09:00", "10:00", "c", "wide", "")),
			)
		}
		return weekEnvelope(weekDayFixture(ws, weekEntryFixture("u"+strconv.Itoa(len(weekStarts)), "09:00", "10:00", "c", "w", "")))
	})
	if _, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-20", 0); err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(weekStarts) < 2 || weekStarts[1] != "2026-10-13" {
		t.Fatalf("the cursor must be clamped to +7 days (2026-10-13), got %v", weekStarts)
	}
}

// F3: the legacy path treats an empty object as a schema error, not an empty
// Complete listing.
func TestListEntries_LegacyEmptyObjectFailsClosed(t *testing.T) {
	f := newFakeMCP() // does NOT declare get_my_week -> the legacy path
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		return structured(json.RawMessage(`{}`))
	}
	c := newTestClient(t, f)
	l, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-06", 0)
	if CodeOf(err) != CodeReadSchema || len(l.Entries) != 0 {
		t.Fatalf("an empty legacy object must fail closed, got entries=%d err=%v", len(l.Entries), err)
	}
}

func TestListEntries_FailedWeekIsAnErrorNotEmpty(t *testing.T) {
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return callOutcome{IsError: true, Text: "tool failed"}
	})
	_, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-06", 0)
	if CodeOf(err) != CodeReadToolError {
		t.Fatalf("a failed week must be an error, not an empty listing; got %v", err)
	}
}

func TestListEntries_EmptyWeekPayloadIsSchemaError(t *testing.T) {
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return callOutcome{}
	})
	l, err := c.ListEntries(context.Background(), "2026-10-06", "2026-10-06", 0)
	// An empty retrieval fails closed at the MCP layer (the typed no-output error),
	// never as an empty timesheet.
	if err == nil || !strings.Contains(err.Error(), mcp.CodeNoToolOutput) || len(l.Entries) != 0 {
		t.Fatalf("an empty retrieval must fail closed with %s, got entries=%d err=%v", mcp.CodeNoToolOutput, len(l.Entries), err)
	}
}

func TestClassifications_ReferenceEnvelopeFailsClosedOnUnknownShape(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolGetClassifications {
			return callOutcome{}
		}
		rb, _ := json.Marshal(map[string]any{"classifications": []map[string]any{{"id": "c1"}}})
		return structured(rb)
	}
	c := newTestClient(t, f)
	if _, err := c.Classifications(context.Background(), ""); CodeOf(err) != CodeReadSchema {
		t.Fatalf("an unrecognized classifications shape must fail closed, got %v", err)
	}
}

func TestEntryByID_WeekPathCompleteRules(t *testing.T) {
	c := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return weekEnvelope(weekDayFixture("2026-10-06", weekEntryFixture("e1", "09:00", "10:00", "c", "work", "")))
	})
	if e, found, err := c.EntryByID(context.Background(), "e1", "2026-10-06", "2026-10-06"); err != nil || !found || e.RemoteID != "e1" {
		t.Fatalf("want e1 found, got %+v found=%v err=%v", e, found, err)
	}
	if _, found, err := c.EntryByID(context.Background(), "missing", "2026-10-06", "2026-10-06"); err != nil || found {
		t.Fatalf("absence from a COMPLETE listing must be (false, nil), got found=%v err=%v", found, err)
	}

	// An INCOMPLETE listing (a duplicate) must refuse to claim absence.
	dup := weekFake(t, func(name string, _ map[string]any) callOutcome {
		if name != ToolGetMyWeek {
			return callOutcome{}
		}
		return weekEnvelope(
			weekDayFixture("2026-10-06", weekEntryFixture("dup", "09:00", "10:00", "c", "one", "")),
			weekDayFixture("2026-10-07", weekEntryFixture("dup", "09:00", "10:00", "c", "two", "")),
		)
	})
	if _, _, err := dup.EntryByID(context.Background(), "missing", "2026-10-06", "2026-10-07"); CodeOf(err) != CodeListingIncomplete {
		t.Fatalf("an incomplete listing must not claim absence, got %v", err)
	}
}
