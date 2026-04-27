// Package workspace loads a Quakefile and its associated .quake files
// and Go tasks into a single merged AST. It is consumed by the CLI
// (to run tasks) and by future tooling such as the LSP.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrQuakefileNotFound is returned by FindQuakefile when walking
// upward from the current directory does not turn up a Quakefile.
// Callers can use errors.Is to distinguish "no workspace here" from
// other lookup failures such as I/O errors.
var ErrQuakefileNotFound = errors.New("no Quakefile found in current or any parent directory")

// taskDirs lists the directories under a project root that may contain
// additional .quake files or Go tasks.
var taskDirs = []string{
	"qtasks",
	filepath.Join("lib", "qtasks"),
	filepath.Join("internal", "qtasks"),
}

// FindQuakefile returns the absolute path to a Quakefile.
//
// If customPath is non-empty, FindQuakefile validates that the file
// exists and returns its absolute path. Otherwise it walks upward
// from the current working directory, returning the first Quakefile
// it encounters. If no Quakefile is found, it returns
// ErrQuakefileNotFound.
func FindQuakefile(customPath string) (string, error) {
	if customPath != "" {
		abs, err := filepath.Abs(customPath)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", customPath, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("stat %s: %w", abs, err)
		}
		return abs, nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	for {
		candidate := filepath.Join(dir, "Quakefile")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", ErrQuakefileNotFound
}

// FindQuakefiles returns the absolute paths of every *.quake file in
// the standard qtasks directories (qtasks, lib/qtasks, internal/qtasks)
// under baseDir. Missing directories are silently skipped.
func FindQuakefiles(baseDir string) []string {
	var files []string
	for _, rel := range taskDirs {
		dir := filepath.Join(baseDir, rel)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		matches, err := filepath.Glob(filepath.Join(dir, "*.quake"))
		if err != nil {
			continue
		}
		files = append(files, matches...)
	}
	return files
}
