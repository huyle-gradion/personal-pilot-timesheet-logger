package editorial

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// keychainRefPrefix marks a credential REFERENCE. A raw secret is never stored in
// a Config.
const keychainRefPrefix = "keychain:"

// Config configures the optional editorial layer. It is plain stdlib JSON. Secrets
// are keychain:<service> references ONLY.
type Config struct {
	Enabled       bool     `json:"enabled"`
	SoftTarget    int64    `json:"soft_target"`
	MaxAttempts   int      `json:"max_attempts"`
	Adapter       string   `json:"adapter"`
	Command       []string `json:"command,omitempty"`
	Endpoint      string   `json:"endpoint,omitempty"`
	Model         string   `json:"model,omitempty"`
	CredentialRef string   `json:"credential_ref,omitempty"`
}

// ParseConfig decodes a Config from JSON.
func ParseConfig(data []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("editorial: invalid config: %w", err)
	}
	return c, nil
}

// Validate checks the config invariants. A credential reference, when present,
// must be a keychain:<service> reference and never a raw secret.
func (c Config) Validate() error {
	if ref := strings.TrimSpace(c.CredentialRef); ref != "" {
		if !strings.HasPrefix(ref, keychainRefPrefix) || strings.TrimSpace(strings.TrimPrefix(ref, keychainRefPrefix)) == "" {
			return fmt.Errorf("editorial: credential_ref must be a %s<service> reference, never a raw secret", keychainRefPrefix)
		}
	}
	if c.SoftTarget < 0 {
		return fmt.Errorf("editorial: soft_target must not be negative")
	}
	if c.MaxAttempts < 0 {
		return fmt.Errorf("editorial: max_attempts must not be negative")
	}
	return nil
}

// SoftTargetOrDefault returns the configured soft target, or the owner-confirmed
// default when unset.
func (c Config) SoftTargetOrDefault() int64 {
	if c.SoftTarget > 0 {
		return c.SoftTarget
	}
	return SoftDailyTokenTarget
}

// NewEditor returns the configured Editor, or (nil, nil) when the editorial layer
// is disabled or unconfigured (deterministic-only). The concrete command is wired
// at S8.
func (c Config) NewEditor() (Editor, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(c.Adapter)) {
	case "", "none":
		return nil, nil
	case "command":
		if len(c.Command) == 0 || strings.TrimSpace(c.Command[0]) == "" {
			return nil, ErrNoCommand
		}
		return &CommandEditor{
			Command: append([]string(nil), c.Command...),
			Timeout: DefaultCommandTimeout * time.Second,
		}, nil
	default:
		return nil, fmt.Errorf("editorial: unknown adapter %q", c.Adapter)
	}
}
