package outbound

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestProductionCodeDoesNotCreateDirectHTTPClients(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve compliance test path")
	}

	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))

	var violations []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(path, filepath.Dir(filename)+string(filepath.Separator)) {
			return nil
		}

		found, err := directHTTPUses(path)
		if err != nil {
			return err
		}

		violations = append(violations, found...)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(violations)

	if len(violations) > 0 {
		t.Fatalf("production HTTP bypasses internal/outbound:\n%s",
			strings.Join(violations, "\n"))
	}
}

func directHTTPUses(path string) ([]string, error) {
	files := token.NewFileSet()

	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return nil, err
	}

	httpNames := make(map[string]struct{})

	for _, item := range parsed.Imports {
		importPath, err := strconv.Unquote(item.Path.Value)
		if err != nil || importPath != "net/http" {
			continue
		}

		name := "http"
		if item.Name != nil {
			name = item.Name.Name
		}

		httpNames[name] = struct{}{}
	}

	var violations []string

	ast.Inspect(parsed, func(node ast.Node) bool {
		if literal, ok := node.(*ast.CompositeLit); ok {
			selector, ok := literal.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Client" {
				return true
			}

			name, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}

			if _, ok := httpNames[name.Name]; ok {
				position := files.Position(selector.Pos())
				violations = append(violations,
					position.String()+": http.Client literal")
			}

			return true
		}

		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		name, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}

		if _, ok := httpNames[name.Name]; !ok {
			return true
		}

		switch selector.Sel.Name {
		case "DefaultClient", "Get", "Post", "PostForm":
			position := files.Position(selector.Pos())
			violations = append(violations, position.String()+": http."+
				selector.Sel.Name)
		}

		return true
	})

	return violations, nil
}
