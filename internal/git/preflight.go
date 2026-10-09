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

// probeGHAuthToken queries `gh auth token` with bounded timeout and safe subprocess execution.
func probeGHAuthToken(ctx context.Context) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "gh", "auth", "token")
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

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = nil // Suppress stderr output from gh

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var stdout bytes.Buffer
	_, _ = io.Copy(&stdout, io.LimitReader(stdoutPipe, 4096))
	_ = cmd.Wait()

	tok := strings.TrimSpace(stdout.String())
	return tok, nil
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
