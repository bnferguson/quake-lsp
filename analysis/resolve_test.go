package analysis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookup_FindsEachKind(t *testing.T) {
	qf := mustParse(t, `
VERSION = "1"

task build {}

namespace db {
    task migrate {}
    VERSION = "ns"
}
`)
	s := BuildSymbolTable(qf)

	sym, ok := s.Lookup("build")
	require.True(t, ok)
	require.Equal(t, KindTask, sym.Kind)

	sym, ok = s.Lookup("VERSION")
	require.True(t, ok)
	require.Equal(t, KindVariable, sym.Kind)

	sym, ok = s.Lookup("db")
	require.True(t, ok)
	require.Equal(t, KindNamespace, sym.Kind)

	sym, ok = s.Lookup("db:migrate")
	require.True(t, ok)
	require.Equal(t, KindTask, sym.Kind)
	require.Equal(t, "db:migrate", sym.Name)

	sym, ok = s.Lookup("db:VERSION")
	require.True(t, ok)
	require.Equal(t, KindVariable, sym.Kind)
}

func TestLookup_MissReturnsZero(t *testing.T) {
	qf := mustParse(t, `task build {}`)
	s := BuildSymbolTable(qf)

	sym, ok := s.Lookup("missing")
	require.False(t, ok)
	require.Equal(t, Symbol{}, sym, "miss returns the zero Symbol")
	require.Equal(t, KindUnknown, sym.Kind)
}

func TestResolve_ReturnsAliasIntoSourceAST(t *testing.T) {
	qf := mustParse(t, `task build { echo hi }`)
	s := BuildSymbolTable(qf)

	got := s.Task("build")
	require.NotNil(t, got)
	require.Same(t, &qf.Tasks[0], got, "returned pointer must alias into the source QuakeFile")
}
