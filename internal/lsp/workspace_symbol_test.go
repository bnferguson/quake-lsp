package lsp

import (
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestWorkspaceSymbols_ReturnsEveryOpenSymbol(t *testing.T) {
	ds := newDocumentStore()
	ds.put(parse("file:///tmp/Quakefile", "VERSION = \"1.0\"\n"+
		"task build {\n    echo hi\n}\n"+
		"namespace db {\n    task migrate {\n        echo m\n    }\n}\n", 1))

	syms := ds.workspaceSymbols("")
	names := symbolNames(syms)
	require.Contains(t, names, "VERSION")
	require.Contains(t, names, "build")
	require.Contains(t, names, "db")
	require.Contains(t, names, "db:migrate")
}

func TestWorkspaceSymbols_FiltersByQueryCaseInsensitively(t *testing.T) {
	ds := newDocumentStore()
	ds.put(parse("file:///tmp/Quakefile", "task BuildAll {\n    echo hi\n}\n"+
		"task test {\n    echo t\n}\n", 1))

	syms := ds.workspaceSymbols("build")
	require.Len(t, syms, 1)
	require.Equal(t, "BuildAll", syms[0].Name)
	require.Equal(t, protocol.SymbolKindFunction, syms[0].Kind)
}

func TestWorkspaceSymbols_SubstringMatch(t *testing.T) {
	ds := newDocumentStore()
	ds.put(parse("file:///tmp/Quakefile", "task migrate {\n    echo m\n}\n"+
		"task integrate {\n    echo i\n}\n"+
		"task test {\n    echo t\n}\n", 1))

	syms := ds.workspaceSymbols("grate")
	names := symbolNames(syms)
	require.ElementsMatch(t, []string{"migrate", "integrate"}, names)
}

func TestWorkspaceSymbols_SpansMultipleOpenDocuments(t *testing.T) {
	ds := newDocumentStore()
	ds.put(parse("file:///tmp/Quakefile", "task build {\n    echo hi\n}\n", 1))
	ds.put(parse("file:///tmp/test_Quakefile", "task test {\n    echo t\n}\n", 1))

	syms := ds.workspaceSymbols("")
	require.Len(t, syms, 2)
	// Each symbol's Location.URI points back at the file that owns it,
	// so the client can open the right editor tab.
	byURI := map[string]string{}
	for _, s := range syms {
		byURI[string(s.Location.URI)] = s.Name
	}
	require.Equal(t, "build", byURI["file:///tmp/Quakefile"])
	require.Equal(t, "test", byURI["file:///tmp/test_Quakefile"])
}

func TestWorkspaceSymbols_SkipsUnparsedDocuments(t *testing.T) {
	ds := newDocumentStore()
	// "task foo" without a body is a parse failure; the document
	// lives in the store but contributes no symbols.
	ds.put(parse("file:///tmp/Quakefile", "task foo\n", 1))

	require.Empty(t, ds.workspaceSymbols(""))
}

func TestWorkspaceSymbols_DeterministicOrder(t *testing.T) {
	// Go map iteration is unspecified; the sort in workspaceSymbols
	// must produce the same order across runs so clients (and tests)
	// can rely on it.
	ds := newDocumentStore()
	ds.put(parse("file:///tmp/a_Quakefile", "task alpha {\n    echo a\n}\n", 1))
	ds.put(parse("file:///tmp/b_Quakefile", "task beta {\n    echo b\n}\n", 1))

	for range 10 {
		syms := ds.workspaceSymbols("")
		names := symbolNames(syms)
		require.Equal(t, []string{"alpha", "beta"}, names)
	}
}

func symbolNames(syms []protocol.SymbolInformation) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.Name)
	}
	return out
}
