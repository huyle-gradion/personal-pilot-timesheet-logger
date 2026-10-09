package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

// fakeResolver is an in-process keychain.Resolver stand-in: no subprocess is
// spawned and the real macOS Keychain is never consulted.
type fakeResolver struct {
	secret string
	err    error
}

func (f fakeResolver) Lookup(context.Context, string, string) (string, error) {
	return f.secret, f.err
}

// keychainNotFound mirrors the typed error the real resolver returns for an
// absent Keychain item.
func keychainNotFound() error {
	return &keychain.OperatingError{
		Outcome: keychain.OutcomeNotFound,
		Code:    "KEYCHAIN_ERR_NOT_FOUND",
		Message: "absent",
	}
}

// keychainTerminal mirrors a non-NotFound Keychain failure (timeout, access
// failure, malformed output, ...).
func keychainTerminal(code, message string) error {
	return &keychain.OperatingError{
		Outcome: keychain.OutcomeTerminal,
		Code:    code,
		Message: message,
	}
}

// installFakeGH puts a fake `gh` on PATH that records its invocation and prints
// token. It returns the marker path whose existence proves `gh` was executed.
// The marker is created with a shell builtin redirection because PATH is
// replaced and external tools (like `touch`) would not resolve.
func installFakeGH(t *testing.T, dir, token string) string {
	t.Helper()
	marker := filepath.Join(dir, "gh-called")
	script := "#!/bin/sh\n: > \"" + marker + "\"\nprintf '" + token + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", dir)
	return marker
}

func TestCheckPreflight_ExplicitTokenAndRepos(t *testing.T) {
	ctx := context.Background()
	status := CheckPreflight(ctx, "test-token-123", []string{"owner/repo"})

	if !status.Ready {
		t.Fatalf("expected Ready=true, got false (missing: %v, guide: %s)", status.MissingKeys, status.Guide)
	}
	if len(status.MissingKeys) != 0 {
		t.Errorf("expected 0 missing keys, got %v", status.MissingKeys)
	}
	if status.Token != "test-token-123" {
		t.Errorf("expected token 'test-token-123', got %q", status.Token)
	}
}

func TestCheckPreflight_MissingToken(t *testing.T) {
	ctx := context.Background()
	// Temporarily override env vars
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	// Also override PATH to make sure `gh` CLI isn't found if present
	t.Setenv("PATH", t.TempDir())

	status := CheckPreflight(ctx, "", []string{"owner/repo"})

	if status.Ready {
		t.Fatal("expected Ready=false when token missing, got true")
	}

	foundTokenKey := false
	for _, k := range status.MissingKeys {
		if k == "GITHUB_TOKEN" {
			foundTokenKey = true
		}
	}
	if !foundTokenKey {
		t.Errorf("expected GITHUB_TOKEN in MissingKeys, got %v", status.MissingKeys)
	}

	if !strings.Contains(status.Guide, "gh auth login") {
		t.Errorf("expected guide to contain 'gh auth login', got:\n%s", status.Guide)
	}
	if !strings.Contains(status.Guide, "repo") || !strings.Contains(status.Guide, "read:user") {
		t.Errorf("expected guide to contain scopes 'repo' and 'read:user', got:\n%s", status.Guide)
	}
}

func TestResolveAuthToken_KeychainFallback(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	restore := keychain.SetDefaultResolver(fakeResolver{secret: "keychain-token-abc"})
	t.Cleanup(restore)

	got, err := ResolveAuthToken(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "keychain-token-abc" {
		t.Fatalf("expected keychain token 'keychain-token-abc', got %q", got)
	}
}

// TestResolveAuthToken_KeychainMissFallsThrough proves a genuine NotFound is
// the ONLY Keychain result that falls through to the legacy `gh auth token`
// source.
func TestResolveAuthToken_KeychainMissFallsThrough(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	marker := installFakeGH(t, t.TempDir(), "gh-fallback-token")

	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	got, err := ResolveAuthToken(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "gh-fallback-token" {
		t.Fatalf("expected fall-through to gh token, got %q", got)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("expected gh fallback to run on NotFound, marker missing: %v", statErr)
	}
}

// TestResolveAuthToken_KeychainTerminalErrorStopsResolution proves that a
// non-NotFound Keychain failure stops resolution: it is surfaced as a typed,
// secret-free error and `gh auth token` is never invoked.
func TestResolveAuthToken_KeychainTerminalErrorStopsResolution(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	marker := installFakeGH(t, t.TempDir(), "must-not-be-used")

	restore := keychain.SetDefaultResolver(fakeResolver{
		err: keychainTerminal("KEYCHAIN_ERR_TIMEOUT", "keychain lookup exceeded deadline"),
	})
	t.Cleanup(restore)

	got, err := ResolveAuthToken(context.Background(), "")
	if err == nil {
		t.Fatalf("expected terminal keychain error to stop resolution, got token %q", got)
	}
	if got != "" {
		t.Fatalf("expected no token on terminal keychain error, got %q", got)
	}
	if !strings.Contains(err.Error(), "KEYCHAIN_ERR_TIMEOUT") {
		t.Fatalf("expected typed keychain code in error, got: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("gh must not be invoked after a non-NotFound keychain failure (marker exists)")
	}
}

// TestResolveAuthToken_NoSecretInDiagnostics verifies that neither a resolved
// secret nor a secret embedded alongside a resolver error leaks into the
// surfaced diagnostics.
func TestResolveAuthToken_NoSecretInDiagnostics(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	const secret = "ghp_super_secret_token_value"
	restore := keychain.SetDefaultResolver(fakeResolver{
		secret: secret,
		err:    keychainTerminal("KEYCHAIN_ERR_ACCESS", "keychain access denied"),
	})
	t.Cleanup(restore)

	got, err := ResolveAuthToken(context.Background(), "")
	if err == nil {
		t.Fatal("expected terminal keychain error")
	}
	if got != "" {
		t.Fatalf("secret must not be returned alongside an error, got %q", got)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("secret leaked into diagnostics: %v", err)
	}
}

// TestCheckPreflight_KeychainTerminalErrorSurfaced verifies the preflight
// status surfaces a non-NotFound Keychain failure instead of the generic
// missing-token guide.
func TestCheckPreflight_KeychainTerminalErrorSurfaced(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	restore := keychain.SetDefaultResolver(fakeResolver{
		err: keychainTerminal("KEYCHAIN_ERR_LOOKUP_FAILED", "security lookup failed for service"),
	})
	t.Cleanup(restore)

	status := CheckPreflight(context.Background(), "", []string{"owner/repo"})
	if status.Ready {
		t.Fatal("expected Ready=false on terminal keychain error")
	}
	if !strings.Contains(status.Error, "KEYCHAIN_ERR_LOOKUP_FAILED") {
		t.Fatalf("expected typed keychain error in status.Error, got: %q", status.Error)
	}
	if !strings.Contains(status.Guide, "KEYCHAIN_ERR_LOOKUP_FAILED") {
		t.Fatalf("expected keychain error in guide, got: %q", status.Guide)
	}
	if strings.Contains(status.Guide, "gh auth login") {
		t.Fatalf("terminal keychain failure must not present the missing-token fallback guide, got: %q", status.Guide)
	}
}

func TestCheckPreflight_MissingRepos(t *testing.T) {
	ctx := context.Background()
	status := CheckPreflight(ctx, "valid-token", []string{"", "  "})

	if status.Ready {
		t.Fatal("expected Ready=false when repos are empty, got true")
	}

	foundRepoKey := false
	for _, k := range status.MissingKeys {
		if k == "TRACKED_REPOS" {
			foundRepoKey = true
		}
	}
	if !foundRepoKey {
		t.Errorf("expected TRACKED_REPOS in MissingKeys, got %v", status.MissingKeys)
	}
	if !strings.Contains(status.Guide, "-repos") {
		t.Errorf("expected guide to mention -repos flag, got:\n%s", status.Guide)
	}
}
