package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"

	goformat "go/format"

	xformat "github.com/goplus/gop/x/format"

	"github.com/goplus/mod/modfile"
)

type class struct {
	name string
	cls  string
	ext  string
	vars *ast.GenDecl
	fns  []*ast.FuncDecl
	proj bool
}

type context struct {
	fset      *token.FileSet
	proj      *modfile.Project
	pkg       string
	classes   map[string]*class
	imports   []*ast.GenDecl
	otherDecl []*ast.GenDecl
	otherFunc []*ast.FuncDecl
}

func newContext(fset *token.FileSet, proj *modfile.Project) *context {
	var pkg string
	if ar := strings.Split(proj.PkgPaths[0], "/"); len(ar) > 0 {
		pkg = ar[len(ar)-1]
	}
	return &context{fset: fset, proj: proj, pkg: pkg, classes: make(map[string]*class)}
}

func typIdent(typ ast.Expr) (string, bool) {
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if sel, ok := typ.(*ast.SelectorExpr); ok {
		if id, ok := typIdent(sel.X); ok {
			return id + "." + sel.Sel.Name, true
		}
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name, true
	}
	return "", false
}

func (c *context) isClass(name string) (proj bool, cls string, ext string) {
	if name == c.pkg+"."+c.proj.Class {
		proj = true
		cls = c.proj.Class
		ext = c.proj.Ext
		return
	}
	for _, work := range c.proj.Works {
		if name == c.pkg+"."+work.Class {
			cls = work.Class
			ext = work.Ext
			return
		}
	}
	return
}

// find class in type spec
func (c *context) findClass(spec *ast.TypeSpec) (cls *class) {
	st, ok := spec.Type.(*ast.StructType)
	if !ok {
		return
	}
	for i, fs := range st.Fields.List {
		if len(fs.Names) == 0 {
			if name, ok := typIdent(fs.Type); ok {
				proj, clsname, ext := c.isClass(name)
				if i == 0 && clsname != "" {
					cls = &class{proj: proj, cls: clsname, ext: ext}
					if proj {
						cls.name = "main"
					} else {
						cls.name = spec.Name.Name
					}
				}
				continue
			}
		} else if cls != nil {
			if cls.vars == nil {
				cls.vars = &ast.GenDecl{Tok: token.VAR}
			}
			cls.vars.Specs = append(cls.vars.Specs, &ast.ValueSpec{Names: fs.Names, Type: fs.Type})
		}
	}
	return
}

func (c *context) parseFile(f *ast.File) {
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.GenDecl); ok {
			switch d.Tok {
			case token.IMPORT:
				c.imports = append(c.imports, d)
				continue
			case token.CONST:
				// skip const _ = true
				if spec, ok := d.Specs[0].(*ast.ValueSpec); ok && spec.Names[0].Name == "_" {
					continue
				}
			case token.TYPE:
				if spec, ok := d.Specs[0].(*ast.TypeSpec); ok {
					if cls := c.findClass(spec); cls != nil {
						c.classes[spec.Name.Name] = cls
						continue
					}
				}
			}
			c.otherDecl = append(c.otherDecl, d)
		}
	}
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok {
			if d.Name.Name == "main" {
				continue
			}
			if d.Recv != nil && len(d.Recv.List) == 1 {
				typ := d.Recv.List[0].Type
				if star, ok := typ.(*ast.StarExpr); ok {
					typ = star.X
				}
				if name, ok := typ.(*ast.Ident); ok {
					if cls, ok := c.classes[name.Name]; ok {
						d.Recv = nil
						cls.fns = append(cls.fns, d)
						continue
					}
				}
			}
			c.otherFunc = append(c.otherFunc, d)
		}
	}
}

func (c *context) output(dir string, out string) error {
	if out == "" {
		for _, cls := range c.classes {
			code := c.code(cls)
			fmt.Println("====", "class:", cls.name+cls.ext, "====")
			fmt.Println(code)
		}
	} else {
		if !filepath.IsAbs(out) {
			out = filepath.Join(dir, out)
		}
		if err := os.MkdirAll(out, 0755); err != nil {
			return err
		}
		for _, cls := range c.classes {
			code := c.code(cls)
			fname := filepath.Join(out, cls.name+cls.ext)
			fmt.Println("export", fname)
			err := os.WriteFile(fname, []byte(code), 0644)
			if err != nil {
				return fmt.Errorf("write file %v error: %w", fname, err)
			}
		}
	}
	return nil
}

func (c *context) code(cls *class) string {
	var buf bytes.Buffer
	if cls.vars != nil {
		goformat.Node(&buf, c.fset, cls.vars)
		buf.Write([]byte{'\n'})
	}
	// output others decls
	if cls.proj {
		for _, decl := range c.otherDecl {
			goformat.Node(&buf, c.fset, decl)
			buf.WriteByte('\n')
		}
		for _, decl := range c.otherFunc {
			goformat.Node(&buf, c.fset, decl)
			buf.WriteByte('\n')
		}
	}
	var body *ast.BlockStmt
	for _, fn := range cls.fns {
		switch fn.Name.Name {
		case "Main":
			if !cls.proj {
				body = fn.Body
			}
			continue
		case "MainEntry":
			if cls.proj {
				body = fn.Body
			}
			continue
		case "Classfname":
			continue
		}
		goformat.Node(&buf, c.fset, fn)
		buf.WriteByte('\n')
	}
	if body != nil {
		goformat.Node(&buf, c.fset, body.List)
		buf.WriteByte('\n')
	}
	// remove sched
	data := strings.ReplaceAll(buf.String(), c.pkg+".Sched()", "")
	code, err := xformat.GopstyleSource([]byte(data), cls.name+cls.ext)
	if err != nil {
		log.Panicln(err)
	}
	r := strings.NewReplacer("this.", "", c.pkg+".", "", "\n\n", "\n")
	src := r.Replace(string(code))
	return src
}
