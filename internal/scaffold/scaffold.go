// Package scaffold は設定のディレクトリの雛形を作る。
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:template
var files embed.FS

// Init は dir に雛形を書く。すでにあるファイルは上書きせず、1 つでもあればエラーにする（何も書かない）。
// workflows/ の中の *.sample は、拡張子を .md にするまで読まれない。
func Init(dir string) error {
	var paths []string
	err := fs.WalkDir(files, "template", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p[len("template/"):])
		}
		return err
	})
	if err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			return fmt.Errorf("%s already exists; not overwriting", filepath.Join(dir, p))
		}
	}
	for _, p := range paths {
		src, err := files.ReadFile("template/" + p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, src, 0o644); err != nil {
			return err
		}
	}
	return nil
}
