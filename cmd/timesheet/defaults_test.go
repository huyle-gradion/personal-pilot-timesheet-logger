package main

// The defaulting rules of the store-path flags: --config falls back to
// $HOME/.timesheet/config.json (a missing default is an error naming the fix)
// and --env-file to $HOME/.timesheet/.env.local (used only when it exists;
// a missing default means no file values, never an error). An explicit flag
// always wins verbatim. os.UserHomeDir reads $HOME on the platforms this
// targets (Linux, macOS), so t.Setenv("HOME", ...) pins every case.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Explicit wins verbatim, even when a default also exists.
	explicit := filepath.Join(t.TempDir(), "elsewhere.json")
	got, err := resolveConfigPath("run", explicit)
	if err != nil || got != explicit {
		t.Fatalf("explicit config: got %q, err %v", got, err)
	}

	// A missing default is an error naming the file and the fix.
	_, err = resolveConfigPath("run", "")
	if err == nil || !strings.Contains(err.Error(), "no configuration at") ||
		!strings.Contains(err.Error(), filepath.Join(home, ".timesheet", "config.json")) ||
		!strings.Contains(err.Error(), "pass --config") {
		t.Fatalf("missing default config: err = %v", err)
	}

	// An existing default is used as-is.
	def := filepath.Join(home, ".timesheet", "config.json")
	if err := os.MkdirAll(filepath.Dir(def), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = resolveConfigPath("run", "")
	if err != nil || got != def {
		t.Fatalf("default config: got %q, err %v", got, err)
	}
}

func TestResolveEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Explicit wins verbatim; an unreadable explicit file fails later, in
	// LoadEnvFile, exactly as before.
	explicit := filepath.Join(t.TempDir(), "other.env")
	got, err := resolveEnvFile("run", explicit)
	if err != nil || got != explicit {
		t.Fatalf("explicit env file: got %q, err %v", got, err)
	}

	// A missing default means no file values, not an error.
	got, err = resolveEnvFile("run", "")
	if err != nil || got != "" {
		t.Fatalf("missing default env file: got %q, err %v", got, err)
	}

	// An existing default is used.
	def := filepath.Join(home, ".timesheet", ".env.local")
	if err := os.MkdirAll(filepath.Dir(def), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def, []byte("A=b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = resolveEnvFile("run", "")
	if err != nil || got != def {
		t.Fatalf("default env file: got %q, err %v", got, err)
	}
}

func TestResolveStoreFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".timesheet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Config resolves from the default; env stays empty when its default is
	// absent — one call, both rules.
	cfg, env, err := resolveStoreFlags("plan", "", "")
	if err != nil || cfg != filepath.Join(dir, "config.json") || env != "" {
		t.Fatalf("store flags: cfg=%q env=%q err=%v", cfg, env, err)
	}
}

func TestResolveStoreFlagsMissingConfigNamesCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, _, err := resolveStoreFlags("reconcile", "", "")
	if err == nil || !strings.HasPrefix(err.Error(), "reconcile: no configuration at") {
		t.Fatalf("config error should name the command, got %v", err)
	}
}
