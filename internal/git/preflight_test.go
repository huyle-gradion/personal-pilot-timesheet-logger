package git

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// withRunGH installs a hermetic fake for the `gh auth token` probe and restores
// the real implementation on cleanup. Tests use it instead of placing a fake
// `gh` on PATH, so they spawn no subprocess and never depend on host scheduling
// or the installed `gh`.
func withRunGH(t *testing.T, fn func(context.Context) ([]byte, error)) {
	t.Helper()
	previous := runGH
	runGH = fn
	t.Cleanup(func() { runGH = previous })
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
	// Make every credential source unavailable hermetically: empty env, an
	// absent Keychain item, and a probe that yields no token. No PATH
	// manipulation and no real `gh` subprocess.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)
	withRunGH(t, func(context.Context) ([]byte, error) { return nil, nil })

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

// mustNotProbe installs a fake runGH that fails the test if the probe is ever
// invoked, making "this higher-precedence source wins" assertions airtight.
func mustNotProbe(t *testing.T) {
	t.Helper()
	withRunGH(t, func(context.Context) ([]byte, error) {
		t.Fatalf("gh probe must not be invoked when a higher-precedence source resolves")
		return nil, nil
	})
}

// TestResolveAuthToken_Precedence_ExplicitBeatsAll proves an explicit token wins
// over both env vars and the Keychain, and the probe is never invoked.
func TestResolveAuthToken_Precedence_ExplicitBeatsAll(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-github")
	t.Setenv("GH_TOKEN", "env-gh")
	restore := keychain.SetDefaultResolver(fakeResolver{secret: "keychain-tok"})
	t.Cleanup(restore)
	mustNotProbe(t)

	got, err := ResolveAuthToken(context.Background(), "explicit-tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "explicit-tok" {
		t.Fatalf("explicit token must win over env/keychain/probe, got %q", got)
	}
}

// TestResolveAuthToken_Precedence_GitHubTokenBeatsGHTokenAndKeychain proves
// GITHUB_TOKEN wins over GH_TOKEN and the Keychain, and the probe is never
// invoked.
func TestResolveAuthToken_Precedence_GitHubTokenBeatsGHTokenAndKeychain(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-github")
	t.Setenv("GH_TOKEN", "env-gh")
	restore := keychain.SetDefaultResolver(fakeResolver{secret: "keychain-tok"})
	t.Cleanup(restore)
	mustNotProbe(t)

	got, err := ResolveAuthToken(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "env-github" {
		t.Fatalf("GITHUB_TOKEN must win over GH_TOKEN/keychain/probe, got %q", got)
	}
}

// TestResolveAuthToken_Precedence_GHTokenBeatsKeychain proves GH_TOKEN wins
// over the Keychain, and the probe is never invoked.
func TestResolveAuthToken_Precedence_GHTokenBeatsKeychain(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "env-gh")
	restore := keychain.SetDefaultResolver(fakeResolver{secret: "keychain-tok"})
	t.Cleanup(restore)
	mustNotProbe(t)

	got, err := ResolveAuthToken(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "env-gh" {
		t.Fatalf("GH_TOKEN must win over keychain/probe, got %q", got)
	}
}

// TestResolveAuthToken_Precedence_KeychainBeatsProbe proves a Keychain hit wins
// over the probe, and the probe is never invoked.
func TestResolveAuthToken_Precedence_KeychainBeatsProbe(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	restore := keychain.SetDefaultResolver(fakeResolver{secret: "keychain-tok"})
	t.Cleanup(restore)
	mustNotProbe(t)

	got, err := ResolveAuthToken(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "keychain-tok" {
		t.Fatalf("keychain hit must win over the probe, got %q", got)
	}
}

// TestResolveAuthToken_Precedence_KeychainNotFoundReachesProbe proves that ONLY
// a genuine Keychain NotFound reaches the probe: every higher source is empty,
// the Keychain reports NotFound, and the probe is invoked and its token wins.
func TestResolveAuthToken_Precedence_KeychainNotFoundReachesProbe(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	called := false
	withRunGH(t, func(context.Context) ([]byte, error) {
		called = true
		return []byte("probe-tok"), nil
	})

	got, err := ResolveAuthToken(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("a Keychain NotFound must reach the probe, but it was never invoked")
	}
	if got != "probe-tok" {
		t.Fatalf("probe token must win after a Keychain NotFound, got %q", got)
	}
}

// TestResolveAuthToken_KeychainMissFallsThrough proves a genuine NotFound is
// the ONLY Keychain result that falls through to the legacy `gh auth token`
// source.
func TestResolveAuthToken_KeychainMissFallsThrough(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	called := false
	withRunGH(t, func(context.Context) ([]byte, error) {
		called = true
		return []byte("gh-fallback-token"), nil
	})

	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	got, err := ResolveAuthToken(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "gh-fallback-token" {
		t.Fatalf("expected fall-through to gh token, got %q", got)
	}
	if !called {
		t.Fatalf("expected the gh probe to run on NotFound, but it was never invoked")
	}
}

// TestResolveAuthToken_KeychainTerminalErrorStopsResolution proves that a
// non-NotFound Keychain failure stops resolution: it is surfaced as a typed,
// secret-free error and `gh auth token` is never invoked.
func TestResolveAuthToken_KeychainTerminalErrorStopsResolution(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	called := false
	withRunGH(t, func(context.Context) ([]byte, error) {
		called = true
		return []byte("must-not-be-used"), nil
	})

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
	if called {
		t.Fatalf("gh must not be invoked after a non-NotFound keychain failure")
	}
}

// TestProbeGHAuthToken_AppliesProductionBound proves the production 2s bound is
// still applied to the probe: a fake records the deadline it receives. It
// asserts the bound without waiting for it to elapse.
func TestProbeGHAuthToken_AppliesProductionBound(t *testing.T) {
	var remaining time.Duration
	withRunGH(t, func(ctx context.Context) ([]byte, error) {
		if dl, ok := ctx.Deadline(); ok {
			remaining = time.Until(dl)
		}
		return []byte("tok"), nil
	})

	// No parent deadline: the probe must supply its own production bound.
	if _, err := probeGHAuthToken(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if remaining <= 0 || remaining > probeTimeoutDuration {
		t.Fatalf("probe must bound the subprocess by %v, got remaining deadline %v", probeTimeoutDuration, remaining)
	}
}

// TestProbeGHAuthToken_TimeoutPropagated proves a probe that blocks past a SHORT
// parent deadline returns a timeout error promptly and deterministically — the
// production bound is untouched; the parent context simply expires first.
func TestProbeGHAuthToken_TimeoutPropagated(t *testing.T) {
	withRunGH(t, func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	start := time.Now()
	tok, err := probeGHAuthToken(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a timeout error, got token %q", tok)
	}
	if tok != "" {
		t.Fatalf("expected no token on timeout, got %q", tok)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("probe must return promptly on the parent deadline, took %v", elapsed)
	}
}

// TestProbeGHAuthToken_CancellationPropagated proves a canceled parent context
// is surfaced as an error rather than an empty token.
func TestProbeGHAuthToken_CancellationPropagated(t *testing.T) {
	withRunGH(t, func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tok, err := probeGHAuthToken(ctx)
	if err == nil {
		t.Fatalf("expected a cancellation error, got token %q", tok)
	}
	if tok != "" {
		t.Fatalf("expected no token on cancellation, got %q", tok)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestProbeGHAuthToken_ErrorPropagated proves a controlled non-timeout probe
// failure is returned rather than swallowed.
func TestProbeGHAuthToken_ErrorPropagated(t *testing.T) {
	wantErr := errors.New("gh probe failed")
	withRunGH(t, func(context.Context) ([]byte, error) { return nil, wantErr })

	tok, err := probeGHAuthToken(context.Background())
	if tok != "" {
		t.Fatalf("expected no token on error, got %q", tok)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the probe error to propagate, got %v", err)
	}
}

// TestProbeGHAuthToken_EmptyOutput proves whitespace-only output yields no token
// and no error.
func TestProbeGHAuthToken_EmptyOutput(t *testing.T) {
	withRunGH(t, func(context.Context) ([]byte, error) { return []byte("  \n\t"), nil })

	tok, err := probeGHAuthToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "" {
		t.Fatalf("expected no token for empty output, got %q", tok)
	}
}

// TestProbeGHAuthToken_TrimsOutput proves surrounding whitespace is trimmed.
func TestProbeGHAuthToken_TrimsOutput(t *testing.T) {
	withRunGH(t, func(context.Context) ([]byte, error) { return []byte("  tok-123\n"), nil })

	tok, err := probeGHAuthToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "tok-123" {
		t.Fatalf("expected trimmed token 'tok-123', got %q", tok)
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
