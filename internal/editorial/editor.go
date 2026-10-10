package editorial

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrNoCommand is returned when a command adapter has no configured command.
var ErrNoCommand = errors.New("editorial: no editor command configured")

// ErrCommandFailed is the secret-free sentinel for a failed editor command. The
// command's raw stdout/stderr are never echoed.
var ErrCommandFailed = errors.New("editorial: editor command failed")

// Editor synthesizes editorial text for a request. It is OPTIONAL: a nil editor
// means deterministic-only synthesis. It returns the parsed response, the token
// usage of the call (Unknown when unavailable), and an error on failure.
type Editor interface {
	Synthesize(ctx context.Context, req Request) (Response, Usage, error)
}

// CommandEditor runs a configured NON-SECRET command as the editorial agent: the
// request JSON is written to stdin and the response JSON is read from stdout. It
// is harness-agnostic; the concrete live command is configured at S8. Stdout is
// captured only up to MaxResponseBytes (a bounded writer stops accumulating beyond
// the cap and the call fails closed with CodeResponseTooLarge), and diagnostics
// are secret-free (raw stdout/stderr are never echoed).
//
// The command MAY report token usage as a single JSON object on stderr with the
// fields input_tokens/output_tokens (required) and cache_tokens (optional). When
// it does not, the usage is Unknown (never zero).
type CommandEditor struct {
	Command []string      // argv; non-secret
	Timeout time.Duration // 0 = DefaultCommandTimeout
	Bounds  Bounds
}

// Synthesize runs the command once and parses its bounded stdout FAIL-CLOSED.
func (c *CommandEditor) Synthesize(ctx context.Context, req Request) (Response, Usage, error) {
	if c == nil || len(c.Command) == 0 || strings.TrimSpace(c.Command[0]) == "" {
		return Response{}, UnknownUsage(), ErrNoCommand
	}
	if ctx == nil {
		return Response{}, UnknownUsage(), errors.New("editorial: nil context")
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, UnknownUsage(), fmt.Errorf("editorial: marshal request: %w", err)
	}
	if int64(len(payload)) > MaxRequestBytes {
		return Response{}, UnknownUsage(), &Error{Code: CodeRequestTooLarge, Excerpt: fmt.Sprintf("bytes=%d", len(payload))}
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	responseLimit := c.Bounds.responseBytes()
	cmd := exec.CommandContext(runCtx, c.Command[0], c.Command[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	stdout := newLimitedBuffer(responseLimit)
	stderr := newLimitedBuffer(MaxResponseBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return Response{}, UnknownUsage(), fmt.Errorf("%w: exit status %d", ErrCommandFailed, ee.ExitCode())
		}
		return Response{}, UnknownUsage(), ErrCommandFailed
	}

	if stdout.Overflowed() {
		return Response{}, UnknownUsage(), &Error{Code: CodeResponseTooLarge, Excerpt: fmt.Sprintf("limit_bytes=%d", responseLimit)}
	}

	allowed := make(map[string]struct{}, len(req.Targets))
	for _, t := range req.Targets {
		allowed[t.ID] = struct{}{}
	}
	resp, err := ParseResponse(bytes.NewReader(stdout.Bytes()), allowed, c.Bounds)
	if err != nil {
		return Response{}, UnknownUsage(), err
	}
	return resp, parseCommandUsage(stderr.Bytes()), nil
}

// limitedBuffer accumulates at most limit bytes and records overflow. Writes
// beyond the limit are discarded (the byte count is still reported so the child
// process is not blocked), so a runaway command cannot exhaust memory.
type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int64
	overflow bool
}

func newLimitedBuffer(limit int64) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	remaining := b.limit + 1 - int64(b.buf.Len())
	if remaining <= 0 {
		b.overflow = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		b.overflow = true
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte    { return b.buf.Bytes() }
func (b *limitedBuffer) Overflowed() bool { return b.overflow }

// commandUsage is the optional stderr usage convention.
type commandUsage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	CacheTokens  *int64 `json:"cache_tokens"`
	Usage        *struct {
		InputTokens  *int64 `json:"input_tokens"`
		OutputTokens *int64 `json:"output_tokens"`
		CacheTokens  *int64 `json:"cache_tokens"`
	} `json:"usage"`
}

// parseCommandUsage scans bounded stderr for the first JSON object that reports
// input_tokens AND output_tokens (cache_tokens is optional, defaulting to 0). When
// nothing usable is found the usage is Unknown (never zero).
func parseCommandUsage(stderr []byte) Usage {
	sc := bufio.NewScanner(bytes.NewReader(stderr))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var cu commandUsage
		if err := json.Unmarshal(line, &cu); err != nil {
			continue
		}
		if in, out, cache, ok := cu.values(); ok {
			return KnownUsage(in, out, cache)
		}
	}
	return UnknownUsage()
}

func (cu commandUsage) values() (int64, int64, int64, bool) {
	in, out, cache := cu.InputTokens, cu.OutputTokens, cu.CacheTokens
	if cu.Usage != nil {
		if in == nil {
			in = cu.Usage.InputTokens
		}
		if out == nil {
			out = cu.Usage.OutputTokens
		}
		if cache == nil {
			cache = cu.Usage.CacheTokens
		}
	}
	if in == nil || out == nil {
		return 0, 0, 0, false
	}
	var c int64
	if cache != nil {
		c = *cache
	}
	return *in, *out, c, true
}
