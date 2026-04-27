package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bnferguson/quake-lsp/internal/gotasks"
	"miren.dev/quake/parser"
)

// Workspace holds every Quakefile that contributes to a project:
// the main file, any *.quake files in qtasks directories, and any
// Go tasks discovered under those directories. Merged is the combined
// AST, ready for the evaluator.
//
// A Workspace owns a Go task cache and must be closed to remove the
// generated dispatcher files.
//
// Workspace is not safe for concurrent use. Callers that need to
// reload on file change should build a fresh Workspace and swap it
// in atomically.
type Workspace struct {
	// MainPath is the absolute path of the main Quakefile.
	MainPath string

	// BaseDir is filepath.Dir(MainPath).
	BaseDir string

	// Sources lists the absolute paths of every Quake source file
	// that contributed to Merged — the main Quakefile followed by
	// each auxiliary .quake file. Go task source files are not
	// included; those live under qtasks directories and are
	// discovered dynamically.
	Sources []string

	// Merged is the concatenation of the main Quakefile, every
	// loaded .quake file, and the discovered Go tasks.
	Merged parser.QuakeFile

	// Warnings records non-fatal problems encountered during Load:
	// unreadable or unparseable .quake files and Go task discovery
	// errors. Fatal errors are returned from Load directly.
	Warnings []error

	taskCache *gotasks.TaskCache
}

// Load reads mainPath and its associated Quake sources into a
// Workspace. Fatal errors (main file missing, main file parse failure)
// are returned directly. Per-file warnings for auxiliary .quake files
// and Go task discovery are collected on Workspace.Warnings.
//
// The ctx is checked between file reads and Go task discovery; if it
// is canceled, Load returns ctx.Err().
//
// The returned Workspace must be closed to release compiled Go task
// dispatchers.
func Load(ctx context.Context, mainPath string) (*Workspace, error) {
	main, err := parseFile(mainPath)
	if err != nil {
		return nil, err
	}

	ws := &Workspace{
		MainPath: mainPath,
		BaseDir:  filepath.Dir(mainPath),
		Sources:  []string{mainPath},
	}

	parts := []parser.QuakeFile{main}
	for _, path := range FindQuakefiles(ws.BaseDir) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		qf, err := parseFile(path)
		if err != nil {
			ws.Warnings = append(ws.Warnings, err)
			continue
		}
		ws.Sources = append(ws.Sources, path)
		parts = append(parts, qf)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	goTasks, goWarns := ws.discoverGoTasks()
	ws.Warnings = append(ws.Warnings, goWarns...)
	if len(goTasks) > 0 {
		parts = append(parts, parser.QuakeFile{Tasks: goTasks})
	}

	ws.Merged = mergeQuakefiles(parts)
	return ws, nil
}

// Close releases resources held by the workspace. It is safe to call
// Close more than once.
func (w *Workspace) Close() error {
	if w.taskCache == nil {
		return nil
	}
	return w.taskCache.Cleanup()
}

// parseFile reads and parses a single Quake source file. It returns
// a descriptive error on either I/O or parse failure.
func parseFile(path string) (parser.QuakeFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return parser.QuakeFile{}, fmt.Errorf("read %s: %w", path, err)
	}

	qf, ok, err := parser.ParseQuakefileWithSource(string(data), path)
	if err != nil {
		return parser.QuakeFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if !ok {
		return parser.QuakeFile{}, fmt.Errorf("parse %s: unknown parse failure", path)
	}
	return qf, nil
}

// discoverGoTasks finds Go-based tasks under every qtasks directory
// and returns them alongside any non-fatal warnings.
func (w *Workspace) discoverGoTasks() ([]parser.Task, []error) {
	var (
		tasks    []parser.Task
		warnings []error
	)

	for _, rel := range taskDirs {
		dir := filepath.Join(w.BaseDir, rel)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		funcs, err := gotasks.DiscoverTasks(dir)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("discover Go tasks in %s: %w", dir, err))
			continue
		}
		if len(funcs) == 0 {
			continue
		}

		if w.taskCache == nil {
			cache, err := gotasks.NewTaskCache()
			if err != nil {
				warnings = append(warnings, fmt.Errorf("create task cache: %w", err))
				return tasks, warnings
			}
			w.taskCache = cache
		}

		dispatcher, err := w.taskCache.GetDispatcherPath(funcs, dir)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("generate dispatcher for %s: %w", dir, err))
			continue
		}

		for _, fn := range funcs {
			name := fn.Name
			if fn.Namespace != "" {
				name = fn.Namespace + ":" + name
			}

			description := fn.Description
			if description == "" {
				description = fmt.Sprintf("Go task from %s", filepath.Base(fn.SourceFile))
			}

			tasks = append(tasks, parser.Task{
				Name:         name,
				Description:  description,
				Arguments:    fn.Params,
				IsGoTask:     true,
				GoDispatcher: dispatcher,
				GoSourceDir:  dir,
				SourceFile:   fn.SourceFile,
				// Explicit empty slice so JSON-marshaling
				// produces `[]` rather than `null`, matching
				// the previous behavior and matching tasks
				// parsed from .quake sources.
				Commands: []parser.Command{},
			})
		}
	}

	return tasks, warnings
}

// mergeQuakefiles concatenates the Tasks, Variables, and Namespaces
// of every file into one. Source positions on each element are
// preserved, so callers can still trace a node back to its origin
// file.
func mergeQuakefiles(files []parser.QuakeFile) parser.QuakeFile {
	var merged parser.QuakeFile
	for _, f := range files {
		merged.Tasks = append(merged.Tasks, f.Tasks...)
		merged.Variables = append(merged.Variables, f.Variables...)
		merged.Namespaces = append(merged.Namespaces, f.Namespaces...)
	}
	return merged
}
