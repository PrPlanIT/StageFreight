package docsgen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// configFieldComments maps "TypeName.FieldName" → the field's doc comment, parsed from
// the config package source. Go reflection cannot see comments, so the config reference
// otherwise falls back to useless "<type> value" stubs even though every field is well
// documented in source. We read those comments here at generate time (the repo source is
// present when `stagefreight docs generate` runs) and feed them into the field table.
//
// It is populated lazily by GenerateConfigReference and is empty (graceful no-op — the
// generator falls back to type descriptions) if the source can't be located or parsed.
var configFieldComments map[string]string

// loadConfigFieldComments parses every non-test .go file under srcDir and records the
// leading (or trailing) doc comment of each struct field, keyed by "TypeName.FieldName".
func loadConfigFieldComments(srcDir string) map[string]string {
	out := map[string]string{}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, srcDir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return out
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok || st.Fields == nil {
						continue
					}
					for _, f := range st.Fields.List {
						doc := firstParagraph(f.Doc)
						if doc == "" {
							doc = firstParagraph(f.Comment)
						}
						if doc == "" {
							continue
						}
						for _, name := range f.Names {
							out[ts.Name.Name+"."+name.Name] = doc
						}
					}
				}
			}
		}
	}
	return out
}

// firstParagraph returns the first paragraph of a comment group, with markers stripped
// and internal whitespace/newlines collapsed to single spaces — a concise, table-safe
// one-liner. Pipes are escaped so they never break a markdown table cell.
func firstParagraph(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	text := cg.Text() // comment markers stripped; paragraphs separated by blank lines
	if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[:i]
	}
	return strings.ReplaceAll(strings.Join(strings.Fields(text), " "), "|", "\\|")
}

// configSourceDir locates the config package source relative to the working directory,
// so the generator works whether run from the repo root or the src tree.
func configSourceDir() string {
	for _, dir := range []string{"src/config", "config", filepath.Join("..", "config")} {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return "src/config"
}
