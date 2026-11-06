// checkfmt verifies Go formatting on every supported development platform.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dirty := false
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".jj" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(b)
		if err != nil {
			return err
		}
		if !bytes.Equal(b, formatted) {
			fmt.Println(path)
			dirty = true
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if dirty {
		fmt.Fprintln(os.Stderr, "Run gofmt on the files above.")
		os.Exit(1)
	}
}
