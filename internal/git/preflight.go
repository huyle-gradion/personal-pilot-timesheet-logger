package git

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

// PreflightStatus conveys whether prerequisites are met to collect Git activity.
type PreflightStatus struct {
	Ready       bool     `json:"ready"`
	MissingKeys []string `json:"missing_keys"`
	Guide       string   `json:"guide"`
	Token       string   `json:"token,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// probeTimeoutDuration bounds a single `gh auth token` probe. It is the
// production deadline and is deliberately unchanged.
const probeTimeoutDuration = 2 * time.Second

// runGH executes the `gh auth token` probe under a bounded, cancelable context
// and returns its raw stdout. It is an unexported, in-package test seam:
// package tests swap it for a hermetic fake via withRunGH so they spawn no
// subprocess and never depend on the installed `gh`. Unlike the exported
// keychain.SetDefaultResolver, it has no external setter and must only be
// swapped by tests in this package. Not safe for concurrent mutation.
var runGH = defaultRunGH

// defaultRunGH runs `gh auth token` with safe subprocess execution and returns
// its stdout bytes.
//
// Unlike the previous implementation it PROPAGATES the subprocess failure — a
// start error, non-zero exit, or context deadline/cancellation — instead of
// discarding it. A killed or timed-out probe must never masquerade as a
// successful empty token.
func defaultRunGH(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
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
	cmd.Stderr = nil // Suppress stderr output from gh

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var stdout bytes.Buffer
	_, copyErr := io.Copy(&stdout, io.LimitReader(stdoutPipe, 4096))
	waitErr := cmd.Wait()

	// Deadline/cancellation takes precedence: a killed probe is a failure, not
	// an empty-but-successful token. ctx.Err() is secret-free.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, waitErr
	}
	if copyErr != nil {
		return nil, copyErr
	}
	return stdout.Bytes(), nil
}

// probeGHAuthToken queries `gh auth token` with a bounded timeout. It applies
// the production bound (probeTimeoutDuration) to the caller's context and
// routes through runGH, trimming the output and propagating any error.
func probeGHAuthToken(ctx context.Context) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeoutDuration)
	defer cancel()

	out, err := runGH(probeCtx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveAuthToken resolves a GitHub authentication token from arguments, env
// vars, the macOS Keychain, or GitHub CLI.
//
// Only a genuine Keychain NotFound (absent item, or unavailable security
// tooling) falls through to the legacy `gh auth token` source. A timeout,
// access failure, malformed output, or any other non-NotFound failure stops
// resolution and is returned as a typed, secret-free error, so a real Keychain
// failure can never be silently downgraded to a weaker credential source.
func ResolveAuthToken(ctx context.Context, explicitToken string) (string, error) {
	if tok := strings.TrimSpace(explicitToken); tok != "" {
		return tok, nil
	}
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		return tok, nil
	}
	if tok := strings.TrimSpace(os.Getenv("GH_TOKEN")); tok != "" {
		return tok, nil
	}
	// Keychain is the authoritative secret store.
	if tok, err := keychain.Lookup(ctx, keychain.ServiceGitHubToken); err != nil {
		if !keychain.IsNotFound(err) {
			return "", err
		}
	} else if tok != "" {
		return tok, nil
	}
	if tok, err := probeGHAuthToken(ctx); err == nil && tok != "" {
		return tok, nil
	}
	return "", nil
}

// CheckPreflight validates prerequisites for Git collection (authentication and target repositories).
func CheckPreflight(ctx context.Context, token string, repos []string) PreflightStatus {
	var missing []string
	var guideParts []string

	resolvedToken, resolveErr := ResolveAuthToken(ctx, token)
	if resolveErr != nil {
		// Surface the typed, secret-free Keychain failure instead of masking it
		// as a missing token or silently degrading to another source.
		guideParts = append(guideParts,
			"GitHub credential lookup failed:\n"+
				"  "+resolveErr.Error()+"\n"+
				"Resolve the macOS Keychain error (or pass -token for tests/CI) before retrying. "+
				"Resolution does not fall back to `gh auth token` after a non-NotFound Keychain failure.",
		)
		return PreflightStatus{
			Ready:       false,
			MissingKeys: []string{"GITHUB_TOKEN"},
			Guide:       strings.Join(guideParts, "\n\n"),
			Error:       resolveErr.Error(),
		}
	}
	if resolvedToken == "" {
		missing = append(missing, "GITHUB_TOKEN")
		guideParts = append(guideParts,
			"GitHub authentication token is missing or not configured.\n"+
				"To resolve this, please do one of the following:\n"+
				"  1. Store the token in the macOS Keychain (recommended):\n"+
				"     security add-generic-password -s gh_auth_token -a \"$USER\" -l gh_auth_token -w\n"+
				"     (security prompts for the secret; never place it on the command line)\n\n"+
				"  2. Or log in with GitHub CLI:\n"+
				"     gh auth login\n\n"+
				"  3. Or generate a Personal Access Token (classic) with 'repo' and 'read:user' scopes:\n"+
				"     Visit: https://github.com/settings/tokens\n"+
				"     Then export the token:\n"+
				"     export GITHUB_TOKEN=\"ghp_your_personal_access_token_here\"",
		)
	}

	// Filter and validate repos
	var validRepos []string
	for _, r := range repos {
		r = strings.TrimSpace(r)
		if r != "" {
			validRepos = append(validRepos, r)
		}
	}

	if len(validRepos) == 0 {
		missing = append(missing, "TRACKED_REPOS")
		guideParts = append(guideParts,
			"No tracked repositories specified.\n"+
				"To resolve this, please specify target repositories:\n"+
				"  1. Via CLI flag:\n"+
				"     -repos=owner/repo1,owner/repo2 or -repos=.\n\n"+
				"  2. Or export the TRACKED_REPOS environment variable:\n"+
				"     export TRACKED_REPOS=\"owner/repo1,owner/repo2\"\n\n"+
				"  3. Or run inside a local git repository with default:\n"+
				"     -repos=.",
		)
	}

	ready := len(missing) == 0
	guide := strings.Join(guideParts, "\n\n")

	return PreflightStatus{
		Ready:       ready,
		MissingKeys: missing,
		Guide:       guide,
		Token:       resolvedToken,
	}
}
