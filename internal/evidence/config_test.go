package evidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfig(t *testing.T) {
	data := []byte(`{
		"id": "owner",
		"timezone": "Australia/Sydney",
		"sources": [
			{"id": "cal", "type": "calendar", "enabled": true, "config": {"feed_id": "primary", "url_ref": "keychain:ical"}},
			{"id": "git", "type": "local_git", "enabled": false}
		]
	}`)

	cfg, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if cfg.ID != "owner" || cfg.Timezone != "Australia/Sydney" {
		t.Errorf("unexpected config header: %+v", cfg)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(cfg.Sources))
	}
	if cfg.Sources[0].Type != SourceCalendar || !cfg.Sources[0].Enabled {
		t.Errorf("unexpected first source: %+v", cfg.Sources[0])
	}
	// The feed URL is a keychain reference, never a raw secret.
	if cfg.Sources[0].Config["url_ref"] != "keychain:ical" {
		t.Errorf("expected keychain reference, got %q", cfg.Sources[0].Config["url_ref"])
	}
	if cfg.Sources[1].Enabled {
		t.Error("expected second source disabled")
	}
}

func TestParseConfig_InvalidJSON(t *testing.T) {
	if _, err := ParseConfig([]byte("{not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"id":"owner","timezone":"UTC"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.ID != "owner" || cfg.Timezone != "UTC" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected error for a missing config file")
	}
}
