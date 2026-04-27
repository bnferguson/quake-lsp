package analysis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildReferenceIndex_IndexesDependencies(t *testing.T) {
	qf := mustParse(t, `
task test {}

task build => test {
    echo building
}

task release => build, test {
    echo releasing
}
`)
	idx := BuildReferenceIndex(qf)

	testRefs := idx.ReferencesTo("test")
	require.Len(t, testRefs, 2, "test is a dep of both build and release")
	for _, r := range testRefs {
		require.Equal(t, RefTaskDependency, r.Kind)
	}

	fromNames := []string{testRefs[0].From, testRefs[1].From}
	require.ElementsMatch(t, []string{"build", "release"}, fromNames)

	require.Len(t, idx.ReferencesTo("build"), 1)
	require.Nil(t, idx.ReferencesTo("missing"))
}

func TestBuildReferenceIndex_IndexesVariableRefsInCommands(t *testing.T) {
	qf := mustParse(t, `
PROJECT = "quake"

task build {
    echo building $PROJECT
    go build -o $OUT
}
`)
	idx := BuildReferenceIndex(qf)

	projectRefs := idx.ReferencesTo("PROJECT")
	require.Len(t, projectRefs, 1)
	require.Equal(t, RefVariable, projectRefs[0].Kind)
	require.Equal(t, "build", projectRefs[0].From)

	outRefs := idx.ReferencesTo("OUT")
	require.Len(t, outRefs, 1, "refs are collected even when the target is not declared")
}

func TestBuildReferenceIndex_NamespacedTaskContext(t *testing.T) {
	qf := mustParse(t, `
namespace db {
    task migrate {
        echo $DB_URL
    }
}
`)
	idx := BuildReferenceIndex(qf)

	refs := idx.ReferencesTo("DB_URL")
	require.Len(t, refs, 1)
	require.Equal(t, "db:migrate", refs[0].From, "containing task carries namespace prefix")
}

func TestBuildReferenceIndex_ContainerPositionFallback(t *testing.T) {
	qf := mustParse(t, `
PROJECT = "q"

task build {
    echo $PROJECT
}
`)
	idx := BuildReferenceIndex(qf)

	refs := idx.ReferencesTo("PROJECT")
	require.Len(t, refs, 1)

	ref := refs[0]
	// VariableElement positions are zeroed by the parser today, so the
	// precise position is unavailable but the container (task) position
	// is always populated.
	require.False(t, ref.HasPrecisePosition(), "variable ref inside a command has no precise position today")
	require.NotEmpty(t, ref.Container.Filename)
	require.Greater(t, ref.Container.Line, 0)
}

func TestBuildReferenceIndex_DependencyReferenceHasPrecisePosition(t *testing.T) {
	qf := mustParse(t, `
task test {}
task build => test {}
`)
	idx := BuildReferenceIndex(qf)

	refs := idx.ReferencesTo("test")
	require.Len(t, refs, 1)
	require.True(t, refs[0].HasPrecisePosition(), "task-dependency refs borrow the task's populated position")
	require.Equal(t, refs[0].Container.Line, refs[0].Position.Line)
}

func TestBuildReferenceIndex_NilInputIsEmpty(t *testing.T) {
	idx := BuildReferenceIndex(nil)
	require.Empty(t, idx.All())
	require.Nil(t, idx.ReferencesTo("anything"))
}

func TestRefKind_String(t *testing.T) {
	require.Equal(t, "task-dependency", RefTaskDependency.String())
	require.Equal(t, "variable", RefVariable.String())
}
