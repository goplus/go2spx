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
	return nil
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
