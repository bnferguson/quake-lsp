package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"miren.dev/quake/parser"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

func TestLoad_MergesMainAndQtasks(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Quakefile")
	writeFile(t, main, "task build {\n  echo building\n}\n")
	writeFile(t, filepath.Join(base, "qtasks", "extra.quake"), "task deploy {\n  echo deploying\n}\n")
	writeFile(t, filepath.Join(base, "lib", "qtasks", "ns.quake"), "namespace db {\n  task migrate {\n    echo m\n  }\n}\n")

	ws, err := Load(context.Background(), main)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ws.Close()) })

	require.Empty(t, ws.Warnings)
	require.Equal(t, main, ws.MainPath)
	require.Equal(t, base, ws.BaseDir)

	names := taskNames(ws.Merged.Tasks)
	require.ElementsMatch(t, []string{"build", "deploy"}, names)

	require.Len(t, ws.Merged.Namespaces, 1)
	require.Equal(t, "db", ws.Merged.Namespaces[0].Name)
}

func TestLoad_SourcesListsEveryContributingFile(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Quakefile")
	aux1 := filepath.Join(base, "qtasks", "a.quake")
	aux2 := filepath.Join(base, "lib", "qtasks", "b.quake")
	writeFile(t, main, "task t1 {}\n")
	writeFile(t, aux1, "task t2 {}\n")
	writeFile(t, aux2, "task t3 {}\n")

	ws, err := Load(context.Background(), main)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ws.Close()) })

	require.ElementsMatch(t, []string{main, aux1, aux2}, ws.Sources)
}

func TestLoad_MissingMainFileIsFatal(t *testing.T) {
	_, err := Load(context.Background(), filepath.Join(t.TempDir(), "Quakefile"))
	require.Error(t, err)
}

func TestLoad_ParseErrorInAuxFileYieldsWarning(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Quakefile")
	writeFile(t, main, "task build {\n  echo ok\n}\n")
	writeFile(t, filepath.Join(base, "qtasks", "broken.quake"), "task {\n")

	ws, err := Load(context.Background(), main)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ws.Close()) })

	require.NotEmpty(t, ws.Warnings, "a parse failure should surface as a warning")
	require.Len(t, ws.Merged.Tasks, 1, "the main Quakefile should still load")
	require.Equal(t, "build", ws.Merged.Tasks[0].Name)
}

func TestLoad_PreservesPerFileSource(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Quakefile")
	aux := filepath.Join(base, "qtasks", "extra.quake")
	writeFile(t, main, "task build {}\n")
	writeFile(t, aux, "task deploy {}\n")

	ws, err := Load(context.Background(), main)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ws.Close()) })

	byName := map[string]string{}
	for _, task := range ws.Merged.Tasks {
		byName[task.Name] = task.SourceFile
	}
	require.Equal(t, main, byName["build"])
	require.Equal(t, aux, byName["deploy"])
}

func TestLoad_CanceledContextAborts(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Quakefile")
	writeFile(t, main, "task build {}\n")
	// Populate an aux file so the ctx check between files has something to guard.
	writeFile(t, filepath.Join(base, "qtasks", "extra.quake"), "task deploy {}\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Load(ctx, main)
	require.ErrorIs(t, err, context.Canceled)
}

func TestLoad_DiscoversGoTasks(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Quakefile")
	writeFile(t, main, "task build {}\n")
	writeFile(t, filepath.Join(base, "qtasks", "hello.go"), `package main

// Greet prints a friendly hello.
func Greet() {
	println("hi")
}
`)

	ws, err := Load(context.Background(), main)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ws.Close()) })

	var greet *parser.Task
	for i := range ws.Merged.Tasks {
		if ws.Merged.Tasks[i].Name == "greet" {
			greet = &ws.Merged.Tasks[i]
			break
		}
	}
	require.NotNil(t, greet, "Go task 'greet' should be merged into the workspace")
	require.True(t, greet.IsGoTask)
	require.NotEmpty(t, greet.GoDispatcher)
}

func TestClose_IsIdempotent(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "Quakefile")
	writeFile(t, main, "task build {}\n")

	ws, err := Load(context.Background(), main)
	require.NoError(t, err)
	require.NoError(t, ws.Close())
	require.NoError(t, ws.Close())
}

func TestErrQuakefileNotFound_IsExported(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, err := FindQuakefile("")
	require.True(t, errors.Is(err, ErrQuakefileNotFound))
}

func taskNames(tasks []parser.Task) []string {
	names := make([]string, len(tasks))
	for i, t := range tasks {
		names[i] = t.Name
	}
	return names
}
