package editorial

import (
	"strings"
	"testing"
)

func allowedIDs(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func TestParseResponse_AdversarialRejections(t *testing.T) {
	allowed := allowedIDs("c1")
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"extra_start_time", `{"c1":{"title":"t","description":"d","start_time":"09:00"}}`, CodeUnexpectedKeys},
		{"extra_project", `{"c1":{"title":"t","description":"d","project":"p"}}`, CodeUnexpectedKeys},
		{"extra_attendance", `{"c1":{"title":"t","description":"d","attendance":true}}`, CodeUnexpectedKeys},
		{"extra_evidence_ids", `{"c1":{"title":"t","description":"d","evidence_ids":["e"]}}`, CodeUnexpectedKeys},
		{"extra_classification_id", `{"c1":{"title":"t","description":"d","classification_id":"x"}}`, CodeUnexpectedKeys},
		{"extra_approval_id", `{"c1":{"title":"t","description":"d","approval_id":"a"}}`, CodeUnexpectedKeys},
		{"missing_description", `{"c1":{"title":"t"}}`, CodeUnexpectedKeys},
		{"unknown_target", `{"other":{"title":"t","description":"d"}}`, CodeUnknownTarget},
		{"top_level_array", `[{"title":"t","description":"d"}]`, CodeResponseNotObject},
		{"top_level_string", `"nope"`, CodeResponseNotObject},
		{"non_string_title", `{"c1":{"title":5,"description":"d"}}`, CodeValueNotString},
		{"non_string_desc", `{"c1":{"title":"t","description":{"x":1}}}`, CodeValueNotString},
		{"null_title", `{"c1":{"title":null,"description":"d"}}`, CodeValueNotString},
		{"target_not_object", `{"c1":"just text"}`, CodeTargetNotObject},
		{"empty_title", `{"c1":{"title":"   ","description":"d"}}`, CodeValueEmpty},
		{"empty_body", ``, CodeResponseMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseResponse(strings.NewReader(tc.in), allowed, Bounds{})
			if err == nil {
				t.Fatalf("want rejection, got nil")
			}
			if got := CodeOf(err); got != tc.want {
				t.Fatalf("want code %s, got %s (%v)", tc.want, got, err)
			}
		})
	}
}

func TestParseResponse_AcceptsValidFlatObject(t *testing.T) {
	resp, err := ParseResponse(strings.NewReader(`{"c1":{"title":"Fix parser","description":"tidy"}}`), allowedIDs("c1"), Bounds{})
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if got := resp.Suggestions["c1"]; got.Title != "Fix parser" || got.Description != "tidy" {
		t.Fatalf("unexpected text: %+v", got)
	}
}

func TestParseResponse_OversizedPayloadRejected(t *testing.T) {
	big := `{"c1":{"title":"t","description":"` + strings.Repeat("x", 4096) + `"}}`
	_, err := ParseResponse(strings.NewReader(big), allowedIDs("c1"), Bounds{MaxResponseBytes: 128})
	if CodeOf(err) != CodeResponseTooLarge {
		t.Fatalf("want too large, got %v", err)
	}
}

func TestParseResponse_ValueTooLongRejected(t *testing.T) {
	in := `{"c1":{"title":"t","description":"` + strings.Repeat("x", 300) + `"}}`
	_, err := ParseResponse(strings.NewReader(in), allowedIDs("c1"), Bounds{MaxDescriptionBytes: 100})
	if CodeOf(err) != CodeValueTooLong {
		t.Fatalf("want value too long, got %v", err)
	}
}

func TestParseResponse_StripsControlCharacters(t *testing.T) {
	resp, err := ParseResponse(strings.NewReader(`{"c1":{"title":"a\u001b[31mb","description":"d\u0000e"}}`), allowedIDs("c1"), Bounds{})
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if got := resp.Suggestions["c1"].Title; got != "a[31mb" {
		t.Errorf("want control chars stripped, got %q", got)
	}
	if got := resp.Suggestions["c1"].Description; got != "de" {
		t.Errorf("want control chars stripped, got %q", got)
	}
}

// A validation error must never echo the raw response output.
func TestParseResponse_ErrorDoesNotEchoRawOutput(t *testing.T) {
	secret := "SUPER-SECRET-VALUE"
	in := `{"c1":{"title":"t","description":"` + secret + `","start_time":"09:00"}}`
	_, err := ParseResponse(strings.NewReader(in), allowedIDs("c1"), Bounds{})
	if err == nil {
		t.Fatal("want rejection")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("validation error must not echo raw output, got %q", err.Error())
	}
	// Also assert the whole raw body is not echoed.
	if strings.Contains(err.Error(), in) {
		t.Fatalf("validation error must not contain the raw body")
	}
}

func TestFlagUnsupportedImpact(t *testing.T) {
	facts := []Fact{{Kind: FactCommitSubject, Text: "refactor parser"}}

	flagged := flagUnsupportedImpact("Shipped the feature, improved by 30%", facts)
	if len(flagged) == 0 {
		t.Fatalf("want unsupported-impact warnings")
	}
	if !hasWarning(flagged, "unsupported_impact:shipped") {
		t.Errorf("want shipped flagged, got %v", flagged)
	}
	if !hasWarning(flagged, "unsupported_impact:quantified") {
		t.Errorf("want a quantified claim flagged, got %v", flagged)
	}

	// A claim supported by a fact is not flagged.
	supported := flagUnsupportedImpact("refactor parser", []Fact{{Kind: FactCommitSubject, Text: "refactor parser"}})
	if len(supported) != 0 {
		t.Errorf("a fact-supported text must not be flagged, got %v", supported)
	}

	// A supported impact verb (present in a fact) is not flagged.
	ok := flagUnsupportedImpact("shipped the parser", []Fact{{Kind: FactCommitSubject, Text: "shipped the parser"}})
	if hasWarning(ok, "unsupported_impact:shipped") {
		t.Errorf("a fact-supported impact verb must not be flagged, got %v", ok)
	}
}

func hasWarning(warnings []string, want string) bool {
	for _, w := range warnings {
		if w == want {
			return true
		}
	}
	return false
}
