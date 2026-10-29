package timesheet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func syntheticDefault() *ConfiguredDefaultClassification {
	return &ConfiguredDefaultClassification{CatalogID: "cat-training", ClassificationID: "cls-training", Code: "TRAIN", Name: "Synthetic Training", ActivityType: "Learning"}
}

func TestConfiguredDefaultConfigShapes(t *testing.T) {
	valid, err := json.Marshal(syntheticDefault())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, shape string
		accepted    bool
	}{
		{"absent", "", true}, {"valid", string(valid), true},
		{"string", `"TRAIN"`, false}, {"array", `[]`, false}, {"empty", `{}`, false},
		{"unknown", `{"catalog_id":"cat","classification_id":"cls","code":"C","name":"N","activity_type":"T","topic":"wrong"}`, false},
	}
	for _, field := range []string{"catalog_id", "classification_id", "code", "name", "activity_type"} {
		var shape map[string]string
		if err := json.Unmarshal(valid, &shape); err != nil {
			t.Fatal(err)
		}
		shape[field] = "  "
		data, err := json.Marshal(shape)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct {
			name, shape string
			accepted    bool
		}{field, string(data), false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			gradion := ""
			if tc.shape != "" {
				gradion = `,"gradion":{"default_classification":` + tc.shape + `}`
			}
			body := fmt.Sprintf(`{"timezone":"UTC","state_path":%q,"projects":[{"id":"WORK"}]%s}`, filepath.Join(dir, "state.json"), gradion)
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted = %v, err = %v", tc.accepted, err)
			}
		})
	}
}

func TestConfiguredDefaultRequiresExactCachedTuple(t *testing.T) {
	for _, field := range []string{"valid", "missing catalog", "missing option", "duplicate option", "catalog identity", "id", "code", "name", "activity type", "disabled", "absent config"} {
		t.Run(field, func(t *testing.T) {
			d := syntheticDefault()
			cfg := &Config{Gradion: &GradionConfig{Enabled: true, DefaultClassification: d}}
			option := ClassificationOption{ID: d.ClassificationID, Code: d.Code, Name: d.Name, ActivityType: d.ActivityType}
			cat := &CatalogRecord{CatalogID: d.CatalogID, Options: []ClassificationOption{option}}
			state := &State{Remote: &RemoteState{Catalogs: map[string]*CatalogRecord{d.CatalogID: cat}}}
			switch field {
			case "missing catalog":
				state.Remote = nil
			case "missing option":
				cat.Options = nil
			case "duplicate option":
				cat.Options = append(cat.Options, option)
			case "catalog identity":
				cat.CatalogID = "wrong"
			case "id":
				cat.Options[0].ID = "wrong"
			case "code":
				cat.Options[0].Code = "wrong"
			case "name":
				cat.Options[0].Name = "wrong"
			case "activity type":
				cat.Options[0].ActivityType = "wrong"
			case "disabled":
				cfg.Gradion.Enabled = false
			case "absent config":
				cfg.Gradion.DefaultClassification = nil
			}
			choice, err := resolveConfiguredDefault(cfg, state)
			if field != "valid" {
				if err == nil {
					t.Fatal("expected fail-closed binding")
				}
				return
			}
			if err != nil || choice.CatalogID != d.CatalogID || choice.ClassificationID != d.ClassificationID {
				t.Fatalf("choice = %+v, err = %v", choice, err)
			}
		})
	}
}

func TestConfiguredDefaultCatalogIsPinnedWithinCacheBound(t *testing.T) {
	catalogs := map[string]*CatalogRecord{"configured": {CatalogID: "configured", ObservedAt: "0000"}}
	for i := 0; i < 25; i++ {
		id := fmt.Sprint(i)
		catalogs[id] = &CatalogRecord{CatalogID: id, ObservedAt: fmt.Sprintf("%04d", i+1)}
	}
	pruneCatalogs(catalogs, "configured")
	if len(catalogs) != maxStoredCatalogs || catalogs["configured"] == nil {
		t.Fatalf("pinned cache has %d records, pinned = %v", len(catalogs), catalogs["configured"])
	}
}

func TestConfiguredDefaultConfigTrimsBinding(t *testing.T) {
	d := syntheticDefault()
	d.CatalogID = "  cat-training "
	d.ClassificationID = " cls-training\t"
	d.Code = " TRAIN "
	d.Name = " Synthetic Training "
	d.ActivityType = " Learning "
	if err := d.normalize(); err != nil {
		t.Fatal(err)
	}
	if *d != *syntheticDefault() {
		t.Fatalf("binding = %+v", d)
	}
}

func TestConfiguredDefaultSurvivesCatalogDiscoveryEviction(t *testing.T) {
	s, _, store, _ := planFixture(t)
	d := syntheticDefault()
	s.cfg.Gradion.DefaultClassification = d
	state := store.State().cloneState()
	state.Remote = &RemoteState{Catalogs: map[string]*CatalogRecord{
		d.CatalogID: {CatalogID: d.CatalogID, ObservedAt: "2025-01-01T00:00:00Z", Options: []ClassificationOption{{ID: d.ClassificationID, Code: d.Code, Name: d.Name, ActivityType: d.ActivityType}}},
	}}
	for i := 0; i < 19; i++ {
		id := fmt.Sprintf("catalog-%d", i)
		state.Remote.Catalogs[id] = &CatalogRecord{CatalogID: id, ObservedAt: "2026-01-01T00:00:00Z"}
	}
	if err := store.CommitState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := s.classifications(Request{Query: "synthetic separate topic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfiguredDefault(); err != nil {
		t.Fatalf("configured catalog evicted: %v", err)
	}
	if len(store.State().Remote.Catalogs) != maxStoredCatalogs {
		t.Fatal("catalog cache bound changed")
	}
}
