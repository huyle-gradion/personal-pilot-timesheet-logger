package editorial

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes an executable hermetic helper script.
func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestCommandEditor_EndToEnd(t *testing.T) {
	script := writeScript(t, "emit.sh",
		"#!/bin/sh\ncat >/dev/null\n"+
			"printf '%s' '{\"c1\":{\"title\":\"t\",\"description\":\"d\"}}'\n"+
			"printf '%s\\n' '{\"input_tokens\":10,\"output_tokens\":20}' >&2\n")
	ed := &CommandEditor{Command: []string{"/bin/sh", script}}

	resp, usage, err := ed.Synthesize(context.Background(), Request{Date: testDate, Targets: []Target{{ID: "c1", Kind: "work"}}})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if resp.Suggestions["c1"].Title != "t" || resp.Suggestions["c1"].Description != "d" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if !usage.Input.Known || usage.Input.Value != 10 || usage.Output.Value != 20 || usage.Cache.Value != 0 {
		t.Errorf("want parsed stderr usage (cache defaults to 0), got %+v", usage)
	}
}

func TestCommandEditor_NoUsageIsUnknown(t *testing.T) {
	script := writeScript(t, "nousage.sh",
		"#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"c1\":{\"title\":\"t\",\"description\":\"d\"}}'\n")
	ed := &CommandEditor{Command: []string{"/bin/sh", script}}
	_, usage, err := ed.Synthesize(context.Background(), Request{Date: testDate, Targets: []Target{{ID: "c1"}}})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if usage.Total().Known {
		t.Errorf("missing usage must be Unknown, never zero; got %+v", usage)
	}
}

func TestCommandEditor_InvalidOutputRejected(t *testing.T) {
	script := writeScript(t, "bad.sh",
		"#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"c1\":{\"title\":\"t\",\"description\":\"d\",\"start_time\":\"09:00\"}}'\n")
	ed := &CommandEditor{Command: []string{"/bin/sh", script}}
	_, _, err := ed.Synthesize(context.Background(), Request{Date: testDate, Targets: []Target{{ID: "c1"}}})
	if CodeOf(err) != CodeUnexpectedKeys {
		t.Fatalf("want CodeUnexpectedKeys, got %v", err)
	}
}

func TestCommandEditor_FailureIsSecretFree(t *testing.T) {
	ed := &CommandEditor{Command: []string{"/bin/sh", "-c", "echo SUPERSECRET-VALUE >&2; exit 3"}}
	_, _, err := ed.Synthesize(context.Background(), Request{Date: testDate, Targets: []Target{{ID: "c1"}}})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "SUPERSECRET-VALUE") {
		t.Fatalf("a command error must not echo stderr, got %q", err.Error())
	}
}

// S7-F2: the response bound is enforced at CAPTURE, so a runaway command cannot
// exhaust memory and the call fails closed.
func TestCommandEditor_OversizedOutputRejected(t *testing.T) {
	script := writeScript(t, "flood.sh",
		"#!/bin/sh\ncat >/dev/null\n"+
			"dd if=/dev/zero bs=1024 count=100 2>/dev/null | tr '\\000' 'x'\n")
	ed := &CommandEditor{Command: []string{"/bin/sh", script}, Bounds: Bounds{MaxResponseBytes: 256}}
	_, _, err := ed.Synthesize(context.Background(), Request{Date: testDate, Targets: []Target{{ID: "c1"}}})
	if CodeOf(err) != CodeResponseTooLarge {
		t.Fatalf("want CodeResponseTooLarge, got %v", err)
	}
}

func TestCommandEditor_NoCommand(t *testing.T) {
	ed := &CommandEditor{}
	if _, _, err := ed.Synthesize(context.Background(), Request{Date: testDate}); err != ErrNoCommand {
		t.Fatalf("want ErrNoCommand, got %v", err)
	}
}
