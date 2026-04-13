package heap_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestNativeBoundaryContractsStayConstrained(t *testing.T) {
	repoRoot := contractRepoRoot(t)
	assertSymbolFiles(t, collectQualifiedSelectorFiles(t, repoRoot, "runtime", "Pinner"), []string{
		"internal/runtime/state/thread.go",
	})
	assertSymbolFiles(t, collectNamedSymbolFiles(t, repoRoot, "ResolveNative"), nil)
	assertSymbolFiles(t, collectNamedSymbolFiles(t, repoRoot, "SyncNative"), nil)
	assertSymbolFiles(t, collectNamedSymbolFiles(t, repoRoot, "OffsetForNativeAddress"), []string{
		"internal/interp/execute.go",
		"internal/interp/host_bridge_test.go",
		"internal/runtime/closure/store_test.go",
		"internal/runtime/heap/heap.go",
		"internal/runtime/heap/heap_test.go",
	})
	assertSymbolFiles(t, collectNamedSymbolFiles(t, repoRoot, "NativeAddressForOffset"), []string{
		"internal/runtime/closure/store.go",
		"internal/runtime/closure/store_test.go",
		"internal/runtime/heap/heap.go",
		"internal/runtime/heap/heap_test.go",
		"internal/runtime/meta/registry.go",
		"internal/runtime/proto/store.go",
		"internal/runtime/proto/store_test.go",
		"internal/runtime/table/store_test.go",
		"internal/runtime/upvalue/manager_test.go",
	})
}

func contractRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func collectQualifiedSelectorFiles(t *testing.T, repoRoot string, qualifier string, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]struct{}{}
	err := filepath.WalkDir(filepath.Join(repoRoot, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		matched := false
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != name {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != qualifier {
				return true
			}
			matched = true
			return false
		})
		if !matched {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("relative path for %s: %v", path, err)
		}
		files[filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("scan qualified selector %s.%s: %v", qualifier, name, err)
	}
	return sortedKeys(files)
}

func collectNamedSymbolFiles(t *testing.T, repoRoot string, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]struct{}{}
	err := filepath.WalkDir(filepath.Join(repoRoot, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		matched := false
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch item := node.(type) {
			case *ast.FuncDecl:
				if item.Name.Name == name {
					matched = true
					return false
				}
			case *ast.SelectorExpr:
				if item.Sel.Name == name {
					matched = true
					return false
				}
			}
			return true
		})
		if !matched {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("relative path for %s: %v", path, err)
		}
		files[filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("scan named symbol %s: %v", name, err)
	}
	return sortedKeys(files)
}

func assertSymbolFiles(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("symbol files = %v, want %v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("symbol files = %v, want %v", got, want)
		}
	}
}

func sortedKeys(items map[string]struct{}) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
