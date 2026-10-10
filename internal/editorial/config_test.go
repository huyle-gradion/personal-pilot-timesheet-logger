package editorial

import (
	"strings"
	"testing"
)

func TestConfig_NewEditor(t *testing.T) {
	if ed, err := (Config{Enabled: false}).NewEditor(); err != nil || ed != nil {
		t.Fatalf("a disabled config must yield a nil editor, got %v %v", ed, err)
	}
	if ed, err := (Config{Enabled: true, Adapter: "none"}).NewEditor(); err != nil || ed != nil {
		t.Fatalf("adapter none must yield a nil editor, got %v %v", ed, err)
	}

	ed, err := (Config{Enabled: true, Adapter: "command", Command: []string{"/bin/true"}}).NewEditor()
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	if _, ok := ed.(*CommandEditor); !ok {
		t.Fatalf("want a *CommandEditor, got %T", ed)
	}

	if _, err := (Config{Enabled: true, Adapter: "command"}).NewEditor(); err == nil {
		t.Fatalf("a command adapter with no command must error")
	}
	if _, err := (Config{Enabled: true, Adapter: "bogus"}).NewEditor(); err == nil {
		t.Fatalf("an unknown adapter must error")
	}
}

func TestConfig_ValidateRejectsRawSecrets(t *testing.T) {
	if err := (Config{CredentialRef: "keychain:gradion-token"}).Validate(); err != nil {
		t.Errorf("a keychain reference must validate: %v", err)
	}
	if err := (Config{CredentialRef: "sk-live-raw-secret"}).Validate(); err == nil {
		t.Errorf("a raw secret must be rejected")
	}
	if err := (Config{CredentialRef: "keychain:"}).Validate(); err == nil {
		t.Errorf("an empty keychain service must be rejected")
	}
	if err := (Config{SoftTarget: -1}).Validate(); err == nil {
		t.Errorf("a negative soft target must be rejected")
	}
	if got := (Config{}).SoftTargetOrDefault(); got != SoftDailyTokenTarget {
		t.Errorf("want the default soft target %d, got %d", SoftDailyTokenTarget, got)
	}
}

func TestConfig_ParseJSONNeverStoresASecret(t *testing.T) {
	raw := `{"enabled":true,"adapter":"command","command":["/bin/true"],"credential_ref":"keychain:editorial","model":"m"}`
	c, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.CredentialRef != "keychain:editorial" {
		t.Errorf("want the reference preserved, got %q", c.CredentialRef)
	}
	if strings.Contains(c.CredentialRef, "sk-") {
		t.Errorf("a config must never carry a raw secret")
	}
}
