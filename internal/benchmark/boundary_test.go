package benchmark

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// providerSymbol matches an identifier that NAMES a harness provider. It is
// applied to identifiers only (never to comments or strings), so ordinary words
// such as "append" or "api" cannot trip it.
var providerSymbol = regexp.MustCompile(`(?i)^(pi|claude|antigravity)([A-Z].*)?$`)

// TestPackageBoundary_StdlibOnlyNoProvider (R3) asserts internal/benchmark is a
// stdlib-only formatter package and that no symbol in it names a provider: the
// harness shapes live in internal/metrics' adapter registry.
func TestPackageBoundary_StdlibOnlyNoProvider(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found")
	}

	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import: %v", name, err)
			}
			first := strings.SplitN(path, "/", 2)[0]
			if strings.Contains(first, ".") {
				t.Errorf("%s imports %q: internal/benchmark must be stdlib-only", name, path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && providerSymbol.MatchString(id.Name) {
				t.Errorf("%s declares/uses the provider symbol %q", name, id.Name)
			}
			return true
		})
	}
}
