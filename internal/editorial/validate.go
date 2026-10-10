package editorial

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Stable, secret-free validation codes.
const (
	CodeRequestTooLarge   = "EDITORIAL_ERR_REQUEST_TOO_LARGE"
	CodeResponseTooLarge  = "EDITORIAL_ERR_RESPONSE_TOO_LARGE"
	CodeResponseMalformed = "EDITORIAL_ERR_RESPONSE_MALFORMED"
	CodeResponseNotObject = "EDITORIAL_ERR_RESPONSE_NOT_OBJECT"
	CodeUnknownTarget     = "EDITORIAL_ERR_UNKNOWN_TARGET"
	CodeTargetNotObject   = "EDITORIAL_ERR_TARGET_NOT_OBJECT"
	CodeUnexpectedKeys    = "EDITORIAL_ERR_UNEXPECTED_KEYS"
	CodeValueNotString    = "EDITORIAL_ERR_VALUE_NOT_STRING"
	CodeValueTooLong      = "EDITORIAL_ERR_VALUE_TOO_LONG"
	CodeValueEmpty        = "EDITORIAL_ERR_VALUE_EMPTY"
)

// Error is a typed, secret-free validation error. Excerpt is a bounded,
// control-stripped STRUCTURAL diagnostic (key/field names only): it never echoes
// raw output values, so a secret can never leak through a validation error.
type Error struct {
	Code    string
	Excerpt string
}

func (e *Error) Error() string {
	if e.Excerpt == "" {
		return "[" + e.Code + "]"
	}
	return "[" + e.Code + "] " + e.Excerpt
}

// CodeOf returns the stable code of an *Error, or "".
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Bounds tunes ParseResponse. A zero field means the named default.
type Bounds struct {
	MaxResponseBytes    int64
	MaxTitleBytes       int
	MaxDescriptionBytes int
}

func (b Bounds) responseBytes() int64 {
	if b.MaxResponseBytes > 0 {
		return b.MaxResponseBytes
	}
	return MaxResponseBytes
}

func (b Bounds) titleBytes() int {
	if b.MaxTitleBytes > 0 {
		return b.MaxTitleBytes
	}
	return MaxTitleBytes
}

func (b Bounds) descriptionBytes() int {
	if b.MaxDescriptionBytes > 0 {
		return b.MaxDescriptionBytes
	}
	return MaxDescriptionBytes
}

// targetObject is the EXACT allowed key set of a target object.
type targetObject struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ParseResponse parses and validates an editorial response FAIL-CLOSED. The whole
// response is rejected on any violation: an oversized body (capped with
// io.LimitReader BEFORE parse), a non-object top level, a target id not in the
// allowlist, a target object whose key set is not exactly {title, description}, a
// non-string value, an over-long value, or an empty-after-trim value. Validation
// errors carry stable codes and bounded STRUCTURAL excerpts, never raw output.
func ParseResponse(r io.Reader, allowed map[string]struct{}, bounds Bounds) (Response, error) {
	if r == nil {
		return Response{}, &Error{Code: CodeResponseMalformed, Excerpt: "nil_reader"}
	}
	limit := bounds.responseBytes()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return Response{}, &Error{Code: CodeResponseMalformed, Excerpt: "read_error"}
	}
	if int64(len(data)) > limit {
		return Response{}, &Error{Code: CodeResponseTooLarge, Excerpt: fmt.Sprintf("limit_bytes=%d", limit)}
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Response{}, &Error{Code: CodeResponseMalformed, Excerpt: "empty_body"}
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		if trimmed[0] != '{' {
			return Response{}, &Error{Code: CodeResponseNotObject, Excerpt: "top_level_not_object"}
		}
		return Response{}, &Error{Code: CodeResponseMalformed, Excerpt: "invalid_json"}
	}

	out := Response{Suggestions: make(map[string]Text, len(top))}
	for id, raw := range top {
		if _, ok := allowed[id]; !ok {
			return Response{}, &Error{Code: CodeUnknownTarget, Excerpt: "unknown_target"}
		}
		text, err := parseTargetObject(raw, bounds)
		if err != nil {
			return Response{}, err
		}
		out.Suggestions[id] = text
	}
	return out, nil
}

// parseTargetObject validates one target object's EXACT key set and values.
func parseTargetObject(raw json.RawMessage, bounds Bounds) (Text, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Text{}, &Error{Code: CodeTargetNotObject, Excerpt: "target_not_object"}
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &keys); err != nil {
		return Text{}, &Error{Code: CodeTargetNotObject, Excerpt: "target_not_object"}
	}
	if len(keys) != 2 {
		return Text{}, &Error{Code: CodeUnexpectedKeys, Excerpt: fmt.Sprintf("key_count=%d", len(keys))}
	}
	for _, k := range []string{"title", "description"} {
		if _, ok := keys[k]; !ok {
			return Text{}, &Error{Code: CodeUnexpectedKeys, Excerpt: fmt.Sprintf("key_count=%d", len(keys))}
		}
	}

	// Values must be JSON strings.
	if _, err := decodeJSONString(keys["title"]); err != nil {
		return Text{}, &Error{Code: CodeValueNotString, Excerpt: "title"}
	}
	if _, err := decodeJSONString(keys["description"]); err != nil {
		return Text{}, &Error{Code: CodeValueNotString, Excerpt: "description"}
	}

	// DisallowUnknownFields belt: redundant with the explicit key-set check, but it
	// fails closed if the two ever diverge.
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var obj targetObject
	if err := dec.Decode(&obj); err != nil {
		return Text{}, &Error{Code: CodeUnexpectedKeys, Excerpt: "unexpected_keys"}
	}

	title, err := cleanValue(obj.Title, bounds.titleBytes(), "title")
	if err != nil {
		return Text{}, err
	}
	description, err := cleanValue(obj.Description, bounds.descriptionBytes(), "description")
	if err != nil {
		return Text{}, err
	}
	return Text{Title: title, Description: description}, nil
}

// decodeJSONString requires the raw value to be a JSON string (null/number/object/
// array are rejected).
func decodeJSONString(raw json.RawMessage) (string, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '"' {
		return "", errors.New("not a JSON string")
	}
	var s string
	if err := json.Unmarshal(t, &s); err != nil {
		return "", err
	}
	return s, nil
}

// cleanValue strips control characters, enforces the byte cap, and requires a
// non-empty value after trimming.
func cleanValue(s string, maxBytes int, field string) (string, error) {
	cleaned := sanitizeText(s)
	if len(cleaned) > maxBytes {
		return "", &Error{Code: CodeValueTooLong, Excerpt: field}
	}
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", &Error{Code: CodeValueEmpty, Excerpt: field}
	}
	return cleaned, nil
}

// sanitizeText strips C0 control characters (except tab/newline) and DEL so an
// editorial value can never smuggle terminal control sequences into durable text.
func sanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func dedupSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// unsupportedImpactPhrases are lexical impact claims a commit or PR title does not
// prove. They are advisory only.
var unsupportedImpactPhrases = []string{
	"shipped", "released", "launched", "deployed", "production-ready",
	"improved", "optimized", "increased", "reduced", "boosted", "faster", "slower",
	"significantly", "sped up", "cut by",
}

var percentPattern = regexp.MustCompile(`\b\d+(?:\.\d+)?\s?%`)

// flagUnsupportedImpact returns bounded warnings for claims the supplied facts do
// not support (e.g. "shipped", "released", "improved by N%"). It is a lexical
// heuristic and NEVER hard-blocks: the text is still surfaced for human review.
func flagUnsupportedImpact(text string, facts []Fact) []string {
	lower := strings.ToLower(text)
	factBlob := factBlobLower(facts)
	var warnings []string
	for _, p := range unsupportedImpactPhrases {
		if strings.Contains(lower, p) && !strings.Contains(factBlob, p) {
			warnings = append(warnings, "unsupported_impact:"+strings.ReplaceAll(p, " ", "_"))
		}
	}
	for _, m := range percentPattern.FindAllString(text, -1) {
		if !strings.Contains(factBlob, strings.ToLower(strings.TrimSpace(m))) {
			warnings = append(warnings, "unsupported_impact:quantified")
			break
		}
	}
	return dedupSorted(warnings)
}

func factBlobLower(facts []Fact) string {
	var sb strings.Builder
	for _, f := range facts {
		sb.WriteString(strings.ToLower(f.Text))
		sb.WriteByte('\n')
	}
	return sb.String()
}
