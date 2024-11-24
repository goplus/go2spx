package class

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"

	xformat "github.com/goplus/gop/x/format"
	"github.com/goplus/mod/modfile"
)

type Class struct {
	Name  string
	Class string
	Ext   string
	Proj  bool
	Decls []*ast.GenDecl
	Funcs []*ast.FuncDecl
}

func (c *Class) FileName() string {
	if c.Proj {
		return "main" + c.Ext
	}
	return c.Name + c.Ext
}

type Context struct {
	FileSet   *token.FileSet
	Project   *modfile.Project
	PkgPath   string
	PkgName   string
	Classes   map[string]*Class
	imports   []*ast.GenDecl
	otherDecl []*ast.GenDecl
	otherFunc []*ast.FuncDecl
}

func NewContext(fset *token.FileSet, proj *modfile.Project) *Context {
	var pkgPath string
	var pkgName string
	if len(proj.PkgPaths) > 0 {
		pkgPath = proj.PkgPaths[0]
		pkgName = path.Base(pkgPath)
	}
	return &Context{FileSet: fset, Project: proj, PkgPath: pkgPath, PkgName: pkgName, Classes: make(map[string]*Class)}
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

func (c *Context) IsClass(name string) (proj bool, cls string, ext string) {
	if name == c.PkgName+"."+c.Project.Class {
		return true, c.Project.Class, c.Project.Ext
	}
	for _, work := range c.Project.Works {
		if name == c.PkgName+"."+work.Class {
			return false, work.Class, work.Ext
		}
	}
	return
}

// find class in type spec
func (c *Context) FindClass(d *ast.GenDecl) *Class {
	if len(d.Specs) != 1 {
		return nil
	}
	spec, ok := d.Specs[0].(*ast.TypeSpec)
	if !ok {
		return nil
	}
	st, ok := spec.Type.(*ast.StructType)
	if !ok {
		return nil
	}
	for i, fs := range st.Fields.List {
		if len(fs.Names) == 0 {
			if name, ok := typIdent(fs.Type); ok {
				proj, clsname, ext := c.IsClass(name)
				if i == 0 && clsname != "" {
					return &Class{Proj: proj, Name: spec.Name.Name, Class: clsname, Ext: ext,
						Decls: []*ast.GenDecl{d}}
				}
			}
		}
	}
	return nil
}

func (c *Context) ParseFile(f *ast.File) {
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
				if cls := c.FindClass(d); cls != nil {
					c.Classes[cls.Name] = cls
					continue
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
					if cls, ok := c.Classes[name.Name]; ok {
						cls.Funcs = append(cls.Funcs, d)
						continue
					}
				}
			}
			c.otherFunc = append(c.otherFunc, d)
		}
	}
}

func (c *Context) Output(dir string, out string) error {
	if out == "" {
		for _, cls := range c.Classes {
			code, err := c.Source(cls)
			if err != nil {
				return err
			}
			fmt.Println("====", "class:", cls.FileName(), "====")
			fmt.Println(code)
		}
	} else {
		if !filepath.IsAbs(out) {
			out = filepath.Join(dir, out)
		}
		if err := os.MkdirAll(out, 0755); err != nil {
			return err
		}
		for _, cls := range c.Classes {
			code, err := c.Source(cls)
			if err != nil {
				return err
			}
			fname := filepath.Join(out, cls.FileName())
			fmt.Println("export", fname)
			err = os.WriteFile(fname, []byte(code), 0644)
			if err != nil {
				return fmt.Errorf("write file %v error: %w", fname, err)
			}
		}
	}
	return nil
}

func (c *Context) Source(cls *Class) (string, error) {
	var buf bytes.Buffer
	for _, im := range c.imports {
		if err := format.Node(&buf, c.FileSet, im); err != nil {
			return "", err
		}
		buf.WriteByte('\n')
	}
	for _, decl := range cls.Decls {
		if err := format.Node(&buf, c.FileSet, decl); err != nil {
			return "", err
		}
		buf.WriteByte('\n')
	}
	// output others decls
	if cls.Proj {
		for _, decl := range c.otherDecl {
			if err := format.Node(&buf, c.FileSet, decl); err != nil {
				return "", err
			}
			buf.WriteByte('\n')
		}
		for _, decl := range c.otherFunc {
			if err := format.Node(&buf, c.FileSet, decl); err != nil {
				return "", err
			}
			buf.WriteByte('\n')
		}
	}
	for _, fn := range cls.Funcs {
		if err := format.Node(&buf, c.FileSet, fn); err != nil {
			return "", err
		}
		buf.WriteByte('\n')
	}
	cfg := &xformat.ClassConfig{
		PkgPath:   c.PkgPath,
		ClassName: cls.Name,
		Project:   cls.Proj,
	}
	code, err := xformat.GopClassSource(buf.Bytes(), cfg)
	if err != nil {
		return buf.String(), err
	}
	return strings.TrimSpace(string(code)), nil
}
