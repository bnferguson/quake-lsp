package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindQuakefile_CustomPathReturnsAbsolute(t *testing.T) {
	dir := t.TempDir()
	rel := "project/Quakefile"
	abs := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("task default {}\n"), 0o644))

	t.Chdir(dir)
	got, err := FindQuakefile(rel)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(got), "returned path should be absolute")
	require.Equal(t, abs, got)
}

func TestFindQuakefile_CustomPathMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := FindQuakefile(filepath.Join(dir, "nope"))
	require.Error(t, err)
}

func TestFindQuakefile_WalksUpwardFromCwd(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	want := filepath.Join(root, "Quakefile")
	require.NoError(t, os.WriteFile(want, []byte(""), 0o644))

	t.Chdir(nested)
	got, err := FindQuakefile("")
	require.NoError(t, err)
	// On macOS /tmp is a symlink, so compare resolved paths.
	resolved, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	gotResolved, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	require.Equal(t, resolved, gotResolved)
}

func TestFindQuakefile_NotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, err := FindQuakefile("")
	require.Error(t, err)
}

func TestFindQuakefiles_AllStandardDirs(t *testing.T) {
	base := t.TempDir()
	// Create one file in each of the three recognized dirs.
	paths := []string{
		filepath.Join(base, "qtasks", "one.quake"),
		filepath.Join(base, "lib", "qtasks", "two.quake"),
		filepath.Join(base, "internal", "qtasks", "three.quake"),
	}
	// A non-matching file should be ignored.
	stray := filepath.Join(base, "qtasks", "ignore.txt")

	for _, p := range append(paths, stray) {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(""), 0o644))
	}

	got := FindQuakefiles(base)
	require.ElementsMatch(t, paths, got)
}

func TestFindQuakefiles_MissingDirsAreSkipped(t *testing.T) {
	base := t.TempDir()
	got := FindQuakefiles(base)
	require.Empty(t, got)
}
