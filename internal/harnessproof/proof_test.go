package harnessproof

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAProofCarriesExactlyTheMintedDigest(t *testing.T) {
	if Approved(context.Background()) != "" {
		t.Fatal("an unapproved context carries a proof")
	}
	ctx := Mint(context.Background(), "sha256:abc")
	if Approved(ctx) != "sha256:abc" {
		t.Fatal("the minted digest was lost")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if Approved(child) != "sha256:abc" {
		t.Fatal("a derived context lost the proof")
	}
	if Approved(Mint(ctx, "sha256:def")) != "sha256:def" {
		t.Fatal("a newer proof did not replace the older one")
	}
	if Approved(context.WithValue(context.Background(), "key", "sha256:abc")) != "" {
		t.Fatal("a foreign context value was accepted as a proof")
	}
}

// Only the run manager may mint a proof. This walks every non-test source file and fails if
// another package references Mint, so a new caller has to be a deliberate, reviewed change.
func TestOnlyTheRunManagerMintsProofs(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := filepath.ToSlash(filepath.Join("internal", "harnessrun")) + "/"
	self := filepath.ToSlash(filepath.Join("internal", "harnessproof")) + "/"
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(rel, allowed) || strings.HasPrefix(rel, self) {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		checked++
		for _, spec := range file.Imports {
			if !strings.HasSuffix(strings.Trim(spec.Path.Value, `"`), "/internal/harnessproof") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Mint" {
					if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "harnessproof" || (spec.Name != nil && id.Name == spec.Name.Name)) {
						t.Errorf("%s mints an approval proof; only the run manager may", rel)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("the walk saw only %d source files; the guard is not looking at the repository", checked)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("not at the repository root: %v", err)
	}
}
