package git

import (
	"context"
	"strings"
	"testing"
)

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
