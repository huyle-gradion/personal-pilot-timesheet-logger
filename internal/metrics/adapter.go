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
)

// ErrUnknownProvider is returned when provider selection cannot identify a
// transcript format. Selection NEVER guesses: an unrecognized transcript fails
// closed.
var ErrUnknownProvider = errors.New("metrics: unknown provider transcript format")

// ErrTranscriptTooLarge is returned when a transcript exceeds MaxTranscriptBytes.
var ErrTranscriptTooLarge = errors.New("metrics: transcript exceeds maximum size")

// Adapter parses one provider's transcript format into a RunRecord.
//
// SAFETY: an adapter extracts only counters, ids, timestamps, and the subcommand
// token. It never unmarshals message text, tool arguments, or tool results into
// any persisted field.
type Adapter interface {
	// Name is the stable provider name ("pi", "claude").
	Name() string
	// Provenance is the measurement provenance the adapter produces.
	Provenance() Provenance
	// Detect reports whether the header (first non-empty line) belongs to this
	// provider.
	Detect(header []byte) bool
	// Parse reads a full transcript and returns a RunRecord.
	Parse(ctx context.Context, r io.Reader) (RunRecord, error)
}

// SelectAdapter chooses an adapter for the requested provider. provider is
// "auto", "pi", or "claude". For "auto" (or empty) it runs Detect over the
// header in order and returns ErrUnknownProvider when nothing matches.
func SelectAdapter(provider string, header []byte) (Adapter, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "pi":
		return piAdapter{}, nil
	case "claude":
		return claudeAdapter{}, nil
	case "", "auto":
		for _, a := range []Adapter{piAdapter{}, claudeAdapter{}} {
			if a.Detect(header) {
				return a, nil
			}
		}
		return nil, ErrUnknownProvider
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
	}
}

// ImportOptions tunes Import.
type ImportOptions struct {
	// Provider is "auto" (default), "pi", or "claude".
	Provider string
	// Date overrides the transcript-derived date when non-empty.
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
func Import(ctx context.Context, path string, opts ImportOptions) (RunRecord, error) {
	if ctx == nil {
		return RunRecord{}, errors.New("metrics: nil context")
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

	sum := sha256.Sum256(data)
	rec.ContentHash = hex.EncodeToString(sum[:])
	if strings.TrimSpace(rec.RunID) == "" {
		if strings.TrimSpace(rec.SessionID) != "" {
			rec.RunID = rec.SessionID
		} else {
			rec.RunID = "sha256:" + rec.ContentHash
		}
	}
	if d := strings.TrimSpace(opts.Date); d != "" {
		rec.Date = d
	}
	// F9: FAIL CLOSED. A run whose date is neither derivable from the transcript
	// nor supplied with --date is never written with an empty Date (it would be
	// unlistable and unaggregatable).
	if strings.TrimSpace(rec.Date) == "" {
		return RunRecord{}, errors.New("metrics: cannot derive the run date from the transcript; pass --date YYYY-MM-DD")
	}
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
	if !in.startedAt.IsZero() {
		rec.Date = in.startedAt.UTC().Format("2006-01-02")
	}
	return rec
}
