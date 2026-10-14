package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ModelCatalog is the resolved (provider, model) -> TokenPrice lookup from a
// models-store.json catalog (~/.pi/agent/models-store.json). The lookup key is
// (provider, model): the same model id can carry different prices under different
// providers, so a model-only lookup is deliberately NOT offered.
type ModelCatalog struct {
	prices map[string]map[string]TokenPrice
}

// catalogFile is the minimal decoded shape of models-store.json: a top-level map
// of provider -> {models: [...]}. The per-model `provider` field is ignored: the
// lookup key is the top-level provider key, which is authoritative.
type catalogFile map[string]struct {
	Models []catalogModel `json:"models"`
}

type catalogModel struct {
	ID   string      `json:"id"`
	Cost catalogCost `json:"cost"`
}

type catalogCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cacheRead"`
	CacheWrite *float64 `json:"cacheWrite"`
}

// ParseModelCatalog parses a models-store.json document. A malformed document is
// an error. A syntactically valid document that lists no usable models yields an
// empty catalog whose every Lookup misses.
func ParseModelCatalog(data []byte) (*ModelCatalog, error) {
	var file catalogFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("metrics: parse model catalog: %w", err)
	}
	cat := &ModelCatalog{prices: make(map[string]map[string]TokenPrice, len(file))}
	for provider, entry := range file {
		p := strings.TrimSpace(provider)
		if p == "" {
			continue
		}
		byModel := make(map[string]TokenPrice, len(entry.Models))
		for _, m := range entry.Models {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				continue
			}
			if _, ok := byModel[id]; ok {
				// First entry wins, so the result is deterministic regardless of a
				// later duplicate id under the same provider.
				continue
			}
			byModel[id] = TokenPrice{
				Input:      m.Cost.Input,
				Output:     m.Cost.Output,
				CacheRead:  m.Cost.CacheRead,
				CacheWrite: m.Cost.CacheWrite,
			}
		}
		cat.prices[p] = byModel
	}
	return cat, nil
}

// LoadModelCatalog reads and parses a catalog file. A missing or unreadable file
// is an ERROR: the caller may treat it as a warning and proceed with an empty
// catalog (every price then unknown), but this package never silently assumes the
// file is present.
func LoadModelCatalog(path string) (*ModelCatalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("metrics: read model catalog: %w", err)
	}
	return ParseModelCatalog(data)
}

// Lookup resolves the price for (provider, model). An unknown provider or model —
// or a nil catalog — returns an all-nil TokenPrice and false: the price is
// UNKNOWN, never zero.
func (c *ModelCatalog) Lookup(provider, model string) (TokenPrice, bool) {
	if c == nil {
		return TokenPrice{}, false
	}
	byModel, ok := c.prices[strings.TrimSpace(provider)]
	if !ok {
		return TokenPrice{}, false
	}
	price, ok := byModel[strings.TrimSpace(model)]
	if !ok {
		return TokenPrice{}, false
	}
	return price, true
}
