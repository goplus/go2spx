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
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goplus/mod/modfile"

	"github.com/goplus/gop"

	"go/parser"
	"go/token"
)

var (
	flagTest    = flag.Bool("t", false, "test if Go+ files are formatted or not.")
	flagNotExec = flag.Bool("n", false, "prints commands that would be executed.")
	flagOut     = flag.String("o", "", "set Go+ class output dir, empty to stdout.")
)

var (
	testErrCnt = 0
	procCnt    = 0
	walkSubDir = false
	rootDir    = ""
)

// func findModule(mod *gopmod.Module, filename string) (*modfile.Project, error) {
// 	f, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
// 	if err != nil {
// 		return nil, err
// 	}
// 	for _, im := range f.Imports {
// 		path, err := strconv.Unquote(im.Path.Value)
// 		if err != nil {
// 			return nil, err
// 		}
// 		for _, proj := range mod.Projects() {
// 			for _, pkg := range proj.PkgPaths {
// 				if pkg == path {
// 					return proj, nil
// 				}
// 			}
// 		}
// 	}
// 	return nil, nil
// }

func findProject(proj *modfile.Project, filename string) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
	if err != nil {
		return false, err
	}
	for _, im := range f.Imports {
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			return false, err
		}
		for _, pkg := range proj.PkgPaths {
			if pkg == path {
				return true, nil
			}
		}
	}
	return false, nil
}

type walker struct {
	dirMap map[string]func(filename, ext string) (proj *modfile.Project, ok bool)
}

func newWalker() *walker {
	return &walker{dirMap: make(map[string]func(filename, ext string) (proj *modfile.Project, ok bool))}
}

var (
	defaultSpxProj = &modfile.Project{
		Ext:   ".spx",
		Class: "Game",
		Works: []*modfile.Class{{
			Ext:   ".spx",
			Class: "Sprite",
		}},
		PkgPaths: []string{"github.com/goplus/spx"},
	}
)

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
			spxProj := defaultSpxProj
			if mod, err := gop.LoadMod(path); err == nil {
				if c, ok := mod.LookupClass(".spx"); ok {
					spxProj = c
				}
			}
			fn = func(filename string, ext string) (*modfile.Project, bool) {
				switch ext {
				case ".go":
					ok, err := findProject(spxProj, filename)
					if err != nil {
						log.Println("find in project error", err)
						break
					}
					if ok {
						return spxProj, true
					}
				}
				return nil, false
			}
			w.dirMap[dir] = fn
		}
		ext := filepath.Ext(path)
		if proj, ok := fn(path, ext); ok {
			procCnt++
			if *flagNotExec {
				fmt.Println("go2spx", path)
			} else {
				err = go2class(proj, dir, path)
				if err != nil {
					report(err)
				}
			}
		}
	}
	return err
}

func go2class(proj *modfile.Project, dir string, filename string) error {
	fmt.Println(filename)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.AllErrors)
	if err != nil {
		return err
	}
	ctx := newContext(fset, proj)
	ctx.parseFile(f)
	ctx.output(dir, *flagOut)
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
