/*
 * Copyright (c) 2024 The GoPlus Authors (goplus.org). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goplus/mod/gopmod"
	"github.com/goplus/mod/modfile"

	"github.com/goplus/gop"
	"github.com/goplus/gop/format"

	goformat "go/format"
	"go/parser"
	"go/token"

	xformat "github.com/goplus/gop/x/format"
)

var (
	flagTest    = flag.Bool("t", false, "test if Go+ files are formatted or not.")
	flagNotExec = flag.Bool("n", false, "prints commands that would be executed.")
)

var (
	testErrCnt = 0
	procCnt    = 0
	walkSubDir = false
	rootDir    = ""
)

func gopfmt(path string, class, smart, mvgo bool) (err error) {
	return nil
	src, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var target []byte
	if smart {
		target, err = xformat.GopstyleSource(src, path)
	} else {
		if !mvgo && filepath.Ext(path) == ".go" {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			err = goformat.Node(&buf, fset, f)
			if err != nil {
				return err
			}
			target = buf.Bytes()
		} else {
			target, err = format.Source(src, class, path)
		}
	}
	if err != nil {
		return
	}
	if bytes.Equal(src, target) {
		return
	}
	fmt.Println(path)
	if *flagTest {
		testErrCnt++
		return nil
	}
	if mvgo {
		newPath := strings.TrimSuffix(path, ".go") + ".gop"
		if err = os.WriteFile(newPath, target, 0666); err != nil {
			return
		}
		return os.Remove(path)
	}
	return writeFileWithBackup(path, target)
}

func writeFileWithBackup(path string, target []byte) (err error) {
	dir, file := filepath.Split(path)
	f, err := os.CreateTemp(dir, file)
	if err != nil {
		return
	}
	tmpfile := f.Name()
	_, err = f.Write(target)
	f.Close()
	if err != nil {
		return
	}
	err = os.Remove(path)
	if err != nil {
		return
	}
	return os.Rename(tmpfile, path)
}

type walker struct {
	dirMap map[string]func(filename, ext string) (proj *modfile.Project, ok bool)
}

func newWalker() *walker {
	return &walker{dirMap: make(map[string]func(filename, ext string) (proj *modfile.Project, ok bool))}
}

func findProject(mod *gopmod.Module, filename string) (*modfile.Project, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	for _, im := range f.Imports {
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			return nil, err
		}
		for _, proj := range mod.Projects() {
			for _, pkg := range proj.PkgPaths {
				if pkg == path {
					return proj, nil
				}
			}
		}
	}
	return nil, nil
}

func (w *walker) walk(path string, d fs.DirEntry, err error) error {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else if d.IsDir() {
		if !walkSubDir && path != rootDir {
			return filepath.SkipDir
		}
	} else {
		dir, _ := filepath.Split(path)
		fn, ok := w.dirMap[dir]
		if !ok {
			if mod, err := gop.LoadMod(path); err == nil {
				fn = func(filename string, ext string) (proj *modfile.Project, ok bool) {
					switch ext {
					case ".go":
						proj, err := findProject(mod, filename)
						if err != nil {
							log.Println("parser import error", err)
							break
						}
						if proj != nil {
							return proj, true
						}
					case ".gop":
					}
					return
				}
			} else {
				fn = func(filename string, ext string) (*modfile.Project, bool) {
					return nil, false
				}
			}
			w.dirMap[dir] = fn
		}
		ext := filepath.Ext(path)
		if proj, ok := fn(path, ext); ok {
			procCnt++
			if *flagNotExec {
				fmt.Println("gop fmt", path)
			} else {
				err = go2class(proj, path)
				if err != nil {
					report(err)
				}
			}
		}
	}
	return err
}

func go2class(proj *modfile.Project, filename string) error {
	fmt.Println(filename)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.AllErrors)
	if err != nil {
		return err
	}
	ctx := newContext(fset, proj)
	ctx.parseFile(f)
	ctx.simply()
	ctx.dump()
	// var buf bytes.Buffer
	// err = goformat.Node(&buf, fset, f)
	// if err != nil {
	// 	return err
	// }
	// fmt.Println(buf.String())
	return nil
}

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
			if d.Tok == token.IMPORT {
				c.imports = append(c.imports, d)
			}
			if d.Tok == token.TYPE {
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
						cls.fns = append(cls.fns, d)
						continue
					}
				}
			}
			c.otherFunc = append(c.otherFunc, d)
		}
	}
}

func (c *context) simply() {
	for _, cls := range c.classes {
		for _, fn := range cls.fns {
			fn.Recv = nil
		}
	}
}

func (c *context) dump() {
	for _, cls := range c.classes {
		code := c.code(cls)
		fmt.Println("====", "class:", cls.name+cls.ext, "====")
		fmt.Println(code)
	}
}

func (c *context) code(cls *class) string {
	var buf bytes.Buffer
	if cls.vars != nil {
		goformat.Node(&buf, c.fset, cls.vars)
		buf.Write([]byte{'\n'})
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
		buf.Write([]byte{'\n'})
	}
	if body != nil {
		goformat.Node(&buf, c.fset, body.List)
		buf.Write([]byte{'\n'})
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

func report(err error) {
	fmt.Println(err)
	os.Exit(2)
}

func main() {
	flag.Parse()
	narg := flag.NArg()
	if narg < 1 {
		flag.PrintDefaults()
	}
	if *flagTest {
		defer func() {
			if testErrCnt > 0 {
				fmt.Printf("total %d files are not formatted.\n", testErrCnt)
				os.Exit(1)
			}
		}()
	}
	walker := newWalker()
	for i := 0; i < narg; i++ {
		path := flag.Arg(i)
		walkSubDir = strings.HasSuffix(path, "/...")
		if walkSubDir {
			path = path[:len(path)-4]
		}
		procCnt = 0
		rootDir = path
		filepath.WalkDir(path, walker.walk)
		if procCnt == 0 {
			fmt.Println("no Go+ files in", path)
		}
	}
}

// -----------------------------------------------------------------------------
