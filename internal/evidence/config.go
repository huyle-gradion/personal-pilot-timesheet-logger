package evidence

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the top-level S3 config document. It is stdlib JSON only (no YAML,
// no new dependencies). Config holds references and endpoints only: a value
// ending in "_ref" is a "keychain:<service>" reference resolved elsewhere, and
// raw secrets are never present in this document.
type Config struct {
	ID       string         `json:"id"`
	Timezone string         `json:"timezone"`
	Sources  []SourceConfig `json:"sources"`
}

// SourceConfig configures one collection source. Config is a bag of
// non-secret endpoints, paths, and keychain references. Secret values are
// referenced as "<key>_ref": "keychain:<service>" and are resolved by the
// credential layer, never stored raw here.
type SourceConfig struct {
	ID      string            `json:"id"`
	Type    SourceKind        `json:"type"`
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config,omitempty"`
}

// ParseConfig decodes a JSON config document.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("evidence: failed to parse config JSON: %w", err)
	}
	return &cfg, nil
}

// LoadConfig reads and decodes a JSON config document from path. A read or parse
// failure is returned; no partial config is used.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("evidence: failed to read config file: %w", err)
	}
	return ParseConfig(data)
}
