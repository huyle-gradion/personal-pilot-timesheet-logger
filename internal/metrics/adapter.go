package metrics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ErrUnknownProvider is returned when provider selection cannot identify a
// transcript format. Selection NEVER guesses: an unrecognized transcript fails
// closed.
var ErrUnknownProvider = errors.New("metrics: unknown provider transcript format")

// ErrDateRequired is returned when an import supplies no explicit --date. A day
// is never inferred from the transcript or the clock: nothing lands in a day's
// lane without an operator-stated day.
var ErrDateRequired = errors.New("metrics: import requires an explicit --date YYYY-MM-DD; no day is inferred from the transcript or the clock")

// ErrProviderMismatch is returned when a forced --provider parses the transcript
// to nothing while another REGISTERED adapter's Detect recognizes it. Refusing is
// the owner decision: an unreadable-by-this-provider transcript is never written
// as an empty record.
var ErrProviderMismatch = errors.New("metrics: transcript provider mismatch")

// ErrUnreadableTranscript is returned when a transcript parses to no calls and no
// registered adapter recognizes it. It is refused rather than written as an empty
// record (refuse-all is the owner-approved arm).
var ErrUnreadableTranscript = errors.New("metrics: transcript is unreadable: no call carried a usage object")

// detectScanWindowLines bounds Guard 1's diagnosis to the first 32 non-empty
// lines. Detect was built for `auto` on ONE header line; validating a whole file
// with it would refuse legitimate imports whose first line is not the probe line
// (resumed files, leading queue/snapshot lines).
const detectScanWindowLines = 32

// ErrTranscriptTooLarge is returned when a transcript exceeds MaxTranscriptBytes.
var ErrTranscriptTooLarge = errors.New("metrics: transcript exceeds maximum size")

// Adapter parses one provider's transcript format into a RunRecord.
//
// SAFETY: an adapter extracts only counters, ids, timestamps, and the subcommand
// token. It never unmarshals message text, tool arguments, or tool results into
// any persisted field.
type Adapter interface {
	// Name is the stable provider name ("pi", "claude", "antigravity").
	Name() string
	// Provenance is the measurement provenance the adapter produces.
	Provenance() Provenance
	// Detect reports whether the header (first non-empty line) belongs to this
	// provider.
	Detect(header []byte) bool
	// Parse reads a full transcript and returns a RunRecord.
	Parse(ctx context.Context, r io.Reader) (RunRecord, error)
}

// adapters returns every REGISTERED adapter in auto-detection order. Adding a
// provider is one entry here and one case in SelectAdapter: there is no dispatch
// table. The three are MUTUALLY EXCLUSIVE by construction (pi requires a
// type:"session" line; claude requires sessionId or summary+leafUuid; antigravity
// requires sessionId ABSENT), so this order is immaterial.
func adapters() []Adapter { return []Adapter{piAdapter{}, claudeAdapter{}, antigravityAdapter{}} }

// SelectAdapter chooses an adapter for the requested provider. provider is
// "auto", "pi", "claude", or "antigravity". For "auto" (or empty) it runs Detect
// over the header in order and returns ErrUnknownProvider when nothing matches.
func SelectAdapter(provider string, header []byte) (Adapter, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "pi":
		return piAdapter{}, nil
	case "claude":
		return claudeAdapter{}, nil
	case "antigravity":
		return antigravityAdapter{}, nil
	case "", "auto":
		for _, a := range adapters() {
			if a.Detect(header) {
				return a, nil
			}
		}
		return nil, ErrUnknownProvider
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
	}
}

// detectAdapter runs every registered adapter's UNCHANGED Detect logic over the
// first detectScanWindowLines non-empty lines and returns the first match, or nil
// when nothing matches. It is used ONLY to diagnose an empty parse (Guard 1),
// never to validate a readable one.
func detectAdapter(data []byte) Adapter {
	remaining := data
	for line := 0; line < detectScanWindowLines; {
		var raw []byte
		if nl := bytes.IndexByte(remaining, '\n'); nl < 0 {
			raw, remaining = remaining, nil
		} else {
			raw, remaining = remaining[:nl], remaining[nl+1:]
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 {
			line++
			for _, a := range adapters() {
				if a.Detect(trimmed) {
					return a
				}
			}
		}
		if len(remaining) == 0 {
			break
		}
	}
	return nil
}

// ImportOptions tunes Import.
type ImportOptions struct {
	// Provider is "auto" (default), "pi", "claude", or "antigravity".
	Provider string
	// Date is the operator's explicit day (YYYY-MM-DD). It is REQUIRED: no day is
	// inferred from the transcript or the clock.
	Date string
	// Commit/WorkflowVersion/ConfigMode/OptimizationChange are non-secret run
	// annotations.
	Commit             string
	WorkflowVersion    string
	ConfigMode         string
	OptimizationChange string
}

// Import reads a transcript file, detects its provider, parses it, and returns a
// finalized RunRecord. ContentHash is the sha256 of the raw file bytes; RunID is
// the session id when present, else "sha256:"+ContentHash.
//
// Two guards fail closed, both AFTER a parse that produced no call:
//   - ErrProviderMismatch when another registered adapter recognizes the
//     transcript, naming the requested and the detected provider;
//   - ErrUnreadableTranscript when nothing does.
//
// A parse that produced calls NEVER refuses.
func Import(ctx context.Context, path string, opts ImportOptions) (RunRecord, error) {
	if ctx == nil {
		return RunRecord{}, errors.New("metrics: nil context")
	}
	// The day is an operator input, never derived: no transcript timestamp and no
	// clock value is ever used to attribute a run to a day.
	date := strings.TrimSpace(opts.Date)
	if date == "" {
		return RunRecord{}, ErrDateRequired
	}
	if err := workflow.ValidateDate(date); err != nil {
		return RunRecord{}, fmt.Errorf("metrics: invalid --date %q: %w", date, err)
	}
	data, err := readTranscript(path)
	if err != nil {
		return RunRecord{}, err
	}

	header := firstNonEmptyLine(data)
	adapter, err := SelectAdapter(opts.Provider, header)
	if err != nil {
		return RunRecord{}, err
	}

	rec, err := adapter.Parse(ctx, bytes.NewReader(data))
	if err != nil {
		return RunRecord{}, err
	}

	// Guard 1: refuse, never write an empty record. The harm exists only where the
	// parse produced no call, so a readable import is never refused. Observations
	// are not persisted on a RunRecord (they only derive durations), so the
	// observable empty-parse condition is the call set.
	if len(rec.Calls) == 0 {
		if detected := detectAdapter(data); detected != nil && detected.Name() != adapter.Name() {
			return RunRecord{}, fmt.Errorf("%w: requested %q, detected %q", ErrProviderMismatch, adapter.Name(), detected.Name())
		}
		return RunRecord{}, ErrUnreadableTranscript
	}

	sum := sha256.Sum256(data)
	rec.ContentHash = hex.EncodeToString(sum[:])
	if strings.TrimSpace(rec.RunID) == "" {
		if strings.TrimSpace(rec.SessionID) != "" {
			rec.RunID = rec.SessionID
		} else {
			rec.RunID = "sha256:" + rec.ContentHash
		}
	}
	rec.Date = date
	rec.Commit = opts.Commit
	rec.WorkflowVersion = opts.WorkflowVersion
	rec.ConfigMode = opts.ConfigMode
	rec.OptimizationChange = opts.OptimizationChange
	return rec, nil
}

// readTranscript reads at most MaxTranscriptBytes from a regular file. A symlink
// is refused (O_NOFOLLOW) so a path cannot be redirected to a secret target.
func readTranscript(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("metrics: empty transcript path")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("metrics: open transcript: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("metrics: stat transcript: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("metrics: transcript is not a regular file")
	}
	if fi.Size() > MaxTranscriptBytes {
		return nil, ErrTranscriptTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxTranscriptBytes+1))
	if err != nil {
		return nil, fmt.Errorf("metrics: read transcript: %w", err)
	}
	if int64(len(data)) > MaxTranscriptBytes {
		return nil, ErrTranscriptTooLarge
	}
	return data, nil
}

// firstNonEmptyLine returns the first non-blank line of data (trailing CR/LF
// stripped). It returns nil when no non-blank line exists.
func firstNonEmptyLine(data []byte) []byte {
	for len(data) > 0 {
		nl := bytes.IndexByte(data, '\n')
		var line []byte
		if nl < 0 {
			line, data = data, nil
		} else {
			line, data = data[:nl], data[nl+1:]
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			return line
		}
	}
	return nil
}

// jsonLineType decodes only the top-level "type" field of a JSON line. It is used
// by Detect and never retains any other content.
func jsonLineType(header []byte) string {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(header, &probe); err != nil {
		return ""
	}
	return probe.Type
}

// parseTimestamp decodes a timestamp that is either an RFC3339 string (a pi
// session line, a claude line) or an epoch-millisecond number (a pi message).
func parseTimestamp(raw json.RawMessage) (time.Time, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return time.Time{}, false
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return time.Time{}, false
		}
		str = strings.TrimSpace(str)
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, str); err == nil {
				return t.UTC(), true
			}
		}
		return time.Time{}, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return time.Time{}, false
	}
	ms, err := n.Int64()
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}

// earlier returns the earlier of two possibly-zero times.
func earlier(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if b.Before(a) {
		return b
	}
	return a
}

// later returns the later of two possibly-zero times.
func later(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if b.After(a) {
		return b
	}
	return a
}

// runParts is the adapter-agnostic input to buildRun.
type runParts struct {
	sessionID    string
	provider     string
	model        string
	startedAt    time.Time
	endedAt      time.Time
	calls        []CallRecord
	observations []Observation
	closingTurn  bool
	cancelled    bool
	provenance   Provenance
}

// buildRun assembles and finalizes a RunRecord: it sums call usage, derives
// durations and steps, and derives RunState. It is the single shared assembly
// path for every adapter, so the derived fields are consistent across providers.
func buildRun(in runParts) RunRecord {
	rec := RunRecord{
		SessionID:  in.sessionID,
		Provider:   in.provider,
		Model:      in.model,
		StartedAt:  in.startedAt,
		EndedAt:    in.endedAt,
		Calls:      in.calls,
		Provenance: in.provenance,
	}
	if len(in.calls) == 0 {
		rec.Usage = UnknownUsage()
	} else {
		rec.Usage = ZeroUsage()
		for _, c := range in.calls {
			rec.Usage = rec.Usage.Add(c.Usage)
		}
	}
	rec.EndToEnd, rec.Active, rec.HumanWait = ComputeTranscriptDurations(in.observations)
	rec.HarnessStart = HarnessStartUnknown()
	rec.Steps = AttributeCalls(rec.Calls)
	rec.State = DeriveState(StateInput{
		Steps:       rec.Steps,
		Cancelled:   in.cancelled,
		ClosingTurn: in.closingTurn,
	})
	// The day is NEVER derived from the transcript: Import requires an explicit
	// --date and assigns it after the parse.
	return rec
}
