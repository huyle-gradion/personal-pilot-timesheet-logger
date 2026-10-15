// Package keychain reads pipeline secrets from the macOS Keychain.
//
// The Keychain is the authoritative secret store. Secrets are retrieved by
// invoking `security find-generic-password -s <service> -a <account> -w`,
// which prints ONLY the secret on stdout when the item exists. This package
// never places a secret on a command line, never propagates stdout into an
// error message, and discards stderr entirely, so a secret cannot leak through
// logs, error strings, or argv.
package keychain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Non-secret references to the Keychain services holding the three pipeline
// credentials. Service names are safe to log; the values they resolve to are not.
const (
	ServiceGitHubToken  = "gh_auth_token"
	ServiceCalendarFeed = "calendar_feed_url"
	ServiceGradionMCP   = "gradion.workspace.ApiKey"
)

// Named finite bounds with explicit units (TigerStyle).
const (
	LookupTimeoutDuration = 3 * time.Second // Bounded deadline per Keychain lookup
	MaxSecretLengthBytes  = 8192            // Maximum accepted secret length, in bytes
	MaxOutputCapBytes     = 16384           // Maximum bytes read from `security` stdout, in bytes
)

// defaultSecurityBinary is the system Keychain CLI on macOS.
const defaultSecurityBinary = "security"

// securityItemNotFoundExitCode is returned by `security` when the item is absent.
const securityItemNotFoundExitCode = 44

// Stable, secret-free error codes (TigerStyle). Callers may match on these.
const (
	codeUnavailable   = "KEYCHAIN_ERR_UNAVAILABLE"
	codeNotFound      = "KEYCHAIN_ERR_NOT_FOUND"
	codeLookupFailed  = "KEYCHAIN_ERR_LOOKUP_FAILED"
	codeEmptySecret   = "KEYCHAIN_ERR_EMPTY_SECRET"
	codeBoundExceeded = "KEYCHAIN_ERR_BOUND_EXCEEDED"
	codeTimeout       = "KEYCHAIN_ERR_TIMEOUT"
	codeNoAccount     = "KEYCHAIN_ERR_NO_ACCOUNT"
	codeNoResolver    = "KEYCHAIN_ERR_NO_RESOLVER"
)

// Outcome classifies the lookup result.
type Outcome int

const (
	OutcomeFound     Outcome = iota // secret retrieved successfully
	OutcomeNotFound                 // item absent, or Keychain tooling unavailable
	OutcomeRetryable                // transient failure; caller may retry
	OutcomeTerminal                 // permanent failure; caller must not retry
)

func (o Outcome) String() string {
	switch o {
	case OutcomeFound:
		return "OutcomeFound"
	case OutcomeNotFound:
		return "OutcomeNotFound"
	case OutcomeRetryable:
		return "OutcomeRetryable"
	case OutcomeTerminal:
		return "OutcomeTerminal"
	default:
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
}

// OperatingError is a typed, bounded, secret-free error with a stable code.
// Its Message and Err fields must never carry secret material; Error() is
// therefore guaranteed not to expose a secret.
type OperatingError struct {
	Outcome Outcome
	Code    string
	Message string
	Err     error
}

func (e *OperatingError) Error() string {
	if e.Err != nil {
		// Err is only ever set to an already-sanitized error (never stdout).
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Err.Error())
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *OperatingError) Unwrap() error {
	return e.Err
}

// Resolver retrieves a secret. account may be "" to mean $USER.
type Resolver interface {
	Lookup(ctx context.Context, service, account string) (string, error)
}

// resolver is the default security-backed Resolver.
type resolver struct {
	binary  string
	timeout time.Duration
}

// Option configures a Resolver.
type Option func(*resolver)

// WithBinary overrides the security binary path (test seam).
func WithBinary(path string) Option {
	return func(r *resolver) {
		if path != "" {
			r.binary = path
		}
	}
}

// WithTimeout overrides the per-lookup deadline (default: LookupTimeoutDuration).
// Non-positive values are ignored.
func WithTimeout(d time.Duration) Option {
	return func(r *resolver) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// NewResolver returns the default security-backed resolver.
func NewResolver(opts ...Option) Resolver {
	r := &resolver{
		binary:  defaultSecurityBinary,
		timeout: LookupTimeoutDuration,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// Lookup runs a bounded `security find-generic-password` invocation and returns
// the trimmed secret. It never passes the secret as an argv value and never
// includes stdout in an error.
func (r *resolver) Lookup(ctx context.Context, service, account string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	service = strings.TrimSpace(service)
	if service == "" {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeLookupFailed,
			Message: "service name must not be empty",
		}
	}

	if account == "" {
		account = strings.TrimSpace(os.Getenv("USER"))
	}
	if account == "" {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeNoAccount,
			Message: "no account available: account empty and USER unset",
		}
	}

	binaryPath, err := exec.LookPath(r.binary)
	if err != nil {
		// Graceful degradation: absent tooling (e.g. non-macOS) is "not found".
		return "", &OperatingError{
			Outcome: OutcomeNotFound,
			Code:    codeUnavailable,
			Message: "security binary not available",
		}
	}

	timeout := r.timeout
	if timeout <= 0 {
		timeout = LookupTimeoutDuration
	}
	lookupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(lookupCtx, binaryPath, "find-generic-password", "-s", service, "-a", account, "-w")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			time.Sleep(50 * time.Millisecond)
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	cmd.Stderr = nil // Discard stderr entirely; never surface it.

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeLookupFailed,
			Message: "failed to open security stdout pipe",
			Err:     err,
		}
	}

	if err := cmd.Start(); err != nil {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeLookupFailed,
			Message: "failed to start security process",
			Err:     err,
		}
	}

	var stdout bytes.Buffer
	_, copyErr := io.Copy(&stdout, io.LimitReader(stdoutPipe, int64(MaxOutputCapBytes)+1))
	waitErr := cmd.Wait()

	// Deadline/cancellation takes precedence over exit classification.
	if lookupCtx.Err() != nil {
		code := codeTimeout
		message := "keychain lookup cancelled"
		if errors.Is(lookupCtx.Err(), context.DeadlineExceeded) {
			message = "keychain lookup exceeded deadline"
		}
		return "", &OperatingError{Outcome: OutcomeTerminal, Code: code, Message: message}
	}

	raw := stdout.Bytes()
	if len(raw) > MaxOutputCapBytes {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeBoundExceeded,
			Message: fmt.Sprintf("security output exceeded %d byte cap", MaxOutputCapBytes),
		}
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == securityItemNotFoundExitCode {
			return "", &OperatingError{
				Outcome: OutcomeNotFound,
				Code:    codeNotFound,
				Message: fmt.Sprintf("keychain item absent for service %q", service),
			}
		}
		// Never include stderr/stdout here.
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeLookupFailed,
			Message: fmt.Sprintf("security lookup failed for service %q", service),
		}
	}

	if copyErr != nil {
		// The pipe failed mid-read while the process exited 0: the captured bytes
		// may be a truncated secret, so fail closed rather than return it.
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeLookupFailed,
			Message: "failed to read security output",
		}
	}

	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeEmptySecret,
			Message: fmt.Sprintf("keychain returned empty secret for service %q", service),
		}
	}
	if len(secret) > MaxSecretLengthBytes {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeBoundExceeded,
			Message: fmt.Sprintf("secret exceeded %d byte cap", MaxSecretLengthBytes),
		}
	}

	return secret, nil
}

// DefaultResolver is the resolver used by the package-level Lookup.
// It may be swapped by tests in other packages via SetDefaultResolver.
var DefaultResolver Resolver = NewResolver()

// SetDefaultResolver replaces DefaultResolver and returns a function that
// restores the previous value. Intended for tests in other packages; not
// safe for concurrent mutation.
func SetDefaultResolver(r Resolver) (restore func()) {
	previous := DefaultResolver
	DefaultResolver = r
	return func() {
		DefaultResolver = previous
	}
}

// Lookup resolves using the default resolver with account = $USER and a bounded timeout.
//
// It FAILS CLOSED when DefaultResolver is nil: it never builds a live resolver as a
// silent default, so an unset default can never reach the live Keychain. The nil
// state is unreachable in production (DefaultResolver initializes to NewResolver()
// and is never set nil) and no test sets it nil; the returned error is a terminal,
// secret-free *OperatingError with code codeNoResolver.
func Lookup(ctx context.Context, service string) (string, error) {
	r := DefaultResolver
	if r == nil {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    codeNoResolver,
			Message: "no default Keychain resolver configured",
		}
	}
	return r.Lookup(ctx, service, "")
}

// IsNotFound reports whether err means the secret could not be found: either the
// Keychain item is absent (exit 44) or the `security` tooling itself is
// unavailable. It does not distinguish these two causes; callers that need to
// tell them apart must inspect the OperatingError Code.
func IsNotFound(err error) bool {
	var opErr *OperatingError
	if errors.As(err, &opErr) {
		return opErr.Outcome == OutcomeNotFound
	}
	return false
}

// Redact removes any occurrence of secret from msg (defense in depth).
func Redact(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "[REDACTED]")
}
