package keychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeSecurityIn installs a fake `security` shell script in dir and returns
// its absolute path. Kept hermetic: it never touches the real macOS Keychain.
func writeFakeSecurityIn(t *testing.T, dir, script string) string {
	t.Helper()
	path := filepath.Join(dir, "security")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake security: %v", err)
	}
	return path
}

func writeFakeSecurity(t *testing.T, script string) string {
	t.Helper()
	return writeFakeSecurityIn(t, t.TempDir(), script)
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	var opErr *OperatingError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected *OperatingError, got %T: %v", err, err)
	}
	if opErr.Code != want {
		t.Fatalf("expected code %q, got %q (err: %v)", want, opErr.Code, err)
	}
}

func TestLookup_Success(t *testing.T) {
	t.Setenv("USER", "tester")
	bin := writeFakeSecurity(t, "printf 'known-secret-value'\n")

	got, err := NewResolver(WithBinary(bin), WithTimeout(60*time.Second)).Lookup(context.Background(), ServiceGitHubToken, "")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if got != "known-secret-value" {
		t.Fatalf("expected trimmed secret %q, got %q", "known-secret-value", got)
	}
}

func TestLookup_DefaultResolverViaPath(t *testing.T) {
	dir := t.TempDir()
	writeFakeSecurityIn(t, dir, "printf 'path-secret'\n")
	restore := SetDefaultResolver(NewResolver(WithTimeout(60 * time.Second)))
	defer restore()
	t.Setenv("PATH", dir)
	t.Setenv("USER", "tester")

	got, err := Lookup(context.Background(), ServiceCalendarFeed)
	if err != nil {
		t.Fatalf("expected success via default resolver, got error: %v", err)
	}
	if got != "path-secret" {
		t.Fatalf("expected %q, got %q", "path-secret", got)
	}
}

func TestLookup_NotFoundExit44(t *testing.T) {
	t.Setenv("USER", "tester")
	bin := writeFakeSecurity(t, "exit 44\n")

	_, err := NewResolver(WithBinary(bin), WithTimeout(60*time.Second)).Lookup(context.Background(), ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected error for exit 44")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err)==true, got false (err: %v)", err)
	}
	assertCode(t, err, codeNotFound)
}

func TestLookup_FailureDoesNotLeakSecret(t *testing.T) {
	t.Setenv("USER", "tester")
	const secret = "leaked-secret-abc123"
	bin := writeFakeSecurity(t, fmt.Sprintf("printf '%s'\nexit 1\n", secret))

	_, err := NewResolver(WithBinary(bin), WithTimeout(60*time.Second)).Lookup(context.Background(), ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected error for nonzero exit")
	}
	assertCode(t, err, codeLookupFailed)
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error message leaked secret: %v", err)
	}
}

func TestRedact(t *testing.T) {
	const secret = "s3cr3t-value"
	msg := "boom: " + secret + " tail"

	out := Redact(msg, secret)
	if strings.Contains(out, secret) {
		t.Fatalf("Redact left secret in output: %q", out)
	}
	if out != "boom: [REDACTED] tail" {
		t.Fatalf("unexpected Redact output: %q", out)
	}

	// An error constructed around a secret must not expose it.
	err := &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    codeLookupFailed,
		Message: Redact("lookup failed: "+secret, secret),
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("constructed error leaked secret: %v", err)
	}
}

func TestLookup_OutputCapExceeded(t *testing.T) {
	t.Setenv("USER", "tester")
	bin := writeFakeSecurity(t, fmt.Sprintf("yes x | head -c %d\n", MaxOutputCapBytes+100))

	_, err := NewResolver(WithBinary(bin), WithTimeout(60*time.Second)).Lookup(context.Background(), ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected error for oversized output")
	}
	assertCode(t, err, codeBoundExceeded)
}

func TestLookup_SecretLengthExceeded(t *testing.T) {
	t.Setenv("USER", "tester")
	// Payload is under the output cap but above the allowed secret length.
	bin := writeFakeSecurity(t, fmt.Sprintf("yes x | head -c %d\n", MaxSecretLengthBytes+200))

	_, err := NewResolver(WithBinary(bin), WithTimeout(60*time.Second)).Lookup(context.Background(), ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected error for oversized secret")
	}
	assertCode(t, err, codeBoundExceeded)
}

func TestLookup_Timeout(t *testing.T) {
	t.Setenv("USER", "tester")
	bin := writeFakeSecurity(t, "sleep 5\nprintf 'late-secret'\n")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := NewResolver(WithBinary(bin)).Lookup(ctx, ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	assertCode(t, err, codeTimeout)
}

func TestLookup_MissingBinary(t *testing.T) {
	t.Setenv("USER", "tester")

	_, err := NewResolver(WithBinary("/nonexistent/security")).Lookup(context.Background(), ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err)==true, got false (err: %v)", err)
	}
	assertCode(t, err, codeUnavailable)
}

func TestLookup_NoAccount(t *testing.T) {
	t.Setenv("USER", "")

	_, err := NewResolver(WithBinary("/nonexistent/security")).Lookup(context.Background(), ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected error when no account is available")
	}
	assertCode(t, err, codeNoAccount)
}

func TestLookup_EmptySecret(t *testing.T) {
	t.Setenv("USER", "tester")
	bin := writeFakeSecurity(t, "printf '   \\n'\n")

	_, err := NewResolver(WithBinary(bin), WithTimeout(60*time.Second)).Lookup(context.Background(), ServiceGitHubToken, "")
	if err == nil {
		t.Fatal("expected error for empty secret")
	}
	assertCode(t, err, codeEmptySecret)
}

// fakeResolver is a subprocess-free Resolver used to exercise the seam.
type fakeResolver struct {
	secret string
}

func (f fakeResolver) Lookup(_ context.Context, _ string, _ string) (string, error) {
	return f.secret, nil
}

func TestSetDefaultResolver_Seam(t *testing.T) {
	original := DefaultResolver

	restore := SetDefaultResolver(fakeResolver{secret: "seam-secret"})
	got, err := Lookup(context.Background(), ServiceGradionMCP)
	if err != nil {
		t.Fatalf("expected fake resolver success, got error: %v", err)
	}
	if got != "seam-secret" {
		t.Fatalf("expected %q, got %q", "seam-secret", got)
	}

	restore()
	if DefaultResolver != original {
		t.Fatal("restore did not put the original resolver back")
	}
}
