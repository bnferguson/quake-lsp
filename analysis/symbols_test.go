package analysis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"miren.dev/quake/parser"
)

func mustParse(t *testing.T, src string) *parser.QuakeFile {
	t.Helper()
	qf, ok, err := parser.ParseQuakefileWithSource(src, "Quakefile")
	require.True(t, ok, "parsing should succeed")
	require.NoError(t, err)
	return &qf
}

func TestBuildSymbolTable_IndexesTopLevelDeclarations(t *testing.T) {
	qf := mustParse(t, `
VERSION = "1.0.0"

task build {
    echo building
}

task test {
    echo testing
}
`)

	s := BuildSymbolTable(qf)

	require.NotNil(t, s.Task("build"))
	require.NotNil(t, s.Task("test"))
	require.NotNil(t, s.Variable("VERSION"))
	require.Nil(t, s.Task("missing"))
}

func TestBuildSymbolTable_QualifiesNamespacedNames(t *testing.T) {
	qf := mustParse(t, `
namespace db {
    task migrate {
        echo migrating
    }

    DB_URL = "postgres://localhost"
}
`)

	s := BuildSymbolTable(qf)

	require.NotNil(t, s.Namespace("db"), "db namespace is indexed")
	require.NotNil(t, s.Task("db:migrate"), "nested task carries its namespace prefix")
	require.NotNil(t, s.Variable("db:DB_URL"), "nested variable carries its namespace prefix")

	// Short names alone must not resolve.
	require.Nil(t, s.Task("migrate"))
	require.Nil(t, s.Variable("DB_URL"))
}

func TestBuildSymbolTable_AllIsSorted(t *testing.T) {
	qf := mustParse(t, `
task zeta {}
task alpha {}
`)
	s := BuildSymbolTable(qf)

	all := s.All()
	require.Len(t, all, 2)
	require.Equal(t, "alpha", all[0].Name)
	require.Equal(t, "zeta", all[1].Name)
}

func TestBuildSymbolTable_SymbolPositionsMatchSource(t *testing.T) {
	qf := mustParse(t, `task build {}

task deploy {}
`)
	s := BuildSymbolTable(qf)

	build, ok := s.Lookup("build")
	require.True(t, ok)
	require.Equal(t, "Quakefile", build.Position.Filename)
	require.Equal(t, 1, build.Position.Line)

	deploy, ok := s.Lookup("deploy")
	require.True(t, ok)
	require.Equal(t, 3, deploy.Position.Line, "deploy starts two lines after build")
}

func TestBuildSymbolTable_DuplicatesAreRecorded(t *testing.T) {
	qf := mustParse(t, `
task build {
    echo first
}

task build {
    echo second
}
`)
	s := BuildSymbolTable(qf)

	first := s.Task("build")
	require.NotNil(t, first)
	require.Len(t, first.Commands, 1, "first declaration wins")

	diags := Diagnose(qf)
	var dup *Diagnostic
	for i := range diags {
		if strings.Contains(diags[i].Message, "duplicate") {
			dup = &diags[i]
			break
		}
	}
	require.NotNil(t, dup, "duplicate declaration surfaces as a diagnostic")
}

func TestBuildSymbolTable_FileNamespaceNotApplied(t *testing.T) {
	// Mirrors evaluator.Evaluator.findTask: FileNamespace parses
	// onto QuakeFile.FileNamespace but is not used for resolution.
	// Analysis follows the evaluator.
	qf := mustParse(t, `namespace api

task start {
    echo hi
}`)

	s := BuildSymbolTable(qf)
	require.NotNil(t, s.Task("start"), "task resolves under its short name")
	require.Nil(t, s.Task("api:start"), "FileNamespace does not prefix the FQN")
}

func TestBuildSymbolTable_NilInputIsEmpty(t *testing.T) {
	s := BuildSymbolTable(nil)
	require.Empty(t, s.All())
	require.Nil(t, s.Task("anything"))
}

func TestBuildSymbolTable_FiltersByKind(t *testing.T) {
	qf := mustParse(t, `
X = "1"
task build {}
namespace db {
    task migrate {}
}
`)
	s := BuildSymbolTable(qf)

	tasks := s.Tasks()
	variables := s.Variables()
	namespaces := s.Namespaces()

	require.Len(t, tasks, 2, "build and db:migrate")
	require.Len(t, variables, 1)
	require.Len(t, namespaces, 1)
}

func TestKind_String(t *testing.T) {
	require.Equal(t, "task", KindTask.String())
	require.Equal(t, "variable", KindVariable.String())
	require.Equal(t, "namespace", KindNamespace.String())
}
