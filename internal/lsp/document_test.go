package lsp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const testURI = "file:///tmp/Quakefile"

func TestDocument_DiagnosticsFromParseFailure(t *testing.T) {
	// "task foo" with no body — peggysue reports a parse failure.
	d := parse(testURI, "task foo\n", 1)

	diags := d.diagnostics()
	require.Len(t, diags, 1)
	require.Equal(t, severityPtr(protocol.DiagnosticSeverityError), diags[0].Severity)
	require.NotEmpty(t, diags[0].Message)
}

func TestDocument_DiagnosticsFromAnalysis(t *testing.T) {
	// Dependency on an undefined task — analysis should catch it,
	// the parser will not.
	src := `
task build => missing {
    echo hi
}
`
	d := parse(testURI, src, 1)

	diags := d.diagnostics()
	require.NotEmpty(t, diags, "undefined dependency should produce a diagnostic")

	var found bool
	for _, diag := range diags {
		if diag.Message == `task "build" depends on undefined task "missing"` {
			found = true
			require.Equal(t, severityPtr(protocol.DiagnosticSeverityError), diag.Severity)
			break
		}
	}
	require.True(t, found, "undefined-dependency diagnostic is reported")
}

func TestDocument_DiagnosticsUndefinedDepNarrowsToToken(t *testing.T) {
	// Undefined task-dependency diagnostics anchor on the containing
	// task's Position, so the editor underlines the whole task body.
	// The LSP layer should narrow to the dep token inside the
	// `=> ...` list so only "missing" gets the squiggle.
	src := "task build {\n    echo building\n}\n" +
		"task ship => build, missing {\n    echo shipping\n}\n"
	d := parse(testURI, src, 1)

	diags := d.diagnostics()
	var diag *protocol.Diagnostic
	for i := range diags {
		if strings.Contains(diags[i].Message, `"missing"`) {
			diag = &diags[i]
			break
		}
	}
	require.NotNil(t, diag, "expected a diagnostic for undefined dep \"missing\"")
	require.Equal(t, "missing", sliceByRange(src, diag.Range), "diagnostic range covers just the dep token")
}

func TestDocument_DiagnosticsUndefinedDepHandlesMultipleInOneTask(t *testing.T) {
	// Two undefined deps in the same task should each get their own
	// narrow range — not two diagnostics overlapping the whole task.
	src := "task ship => alpha, beta {\n    echo shipping\n}\n"
	d := parse(testURI, src, 1)

	var ranges []protocol.Range
	for _, diag := range d.diagnostics() {
		if strings.Contains(diag.Message, "undefined task") {
			ranges = append(ranges, diag.Range)
		}
	}
	require.Len(t, ranges, 2)
	require.NotEqual(t, ranges[0], ranges[1])
	got := []string{sliceByRange(src, ranges[0]), sliceByRange(src, ranges[1])}
	require.ElementsMatch(t, []string{"alpha", "beta"}, got)
}

func TestDocument_DiagnosticsDepCycleNarrowsToBackEdge(t *testing.T) {
	// Cycle a -> b -> c -> a. The dep-cycle diagnostic should narrow
	// to the back-edge dep token — the `a` in `task c => a` — instead
	// of underlining whichever task DFS happened to enter first.
	src := "task a => b {\n    echo a\n}\n" +
		"task b => c {\n    echo b\n}\n" +
		"task c => a {\n    echo c\n}\n"
	d := parse(testURI, src, 1)

	diags := d.diagnostics()
	var diag *protocol.Diagnostic
	for i := range diags {
		if strings.Contains(diags[i].Message, "dependency cycle") {
			diag = &diags[i]
			break
		}
	}
	require.NotNil(t, diag, "expected a dependency-cycle diagnostic")
	require.Equal(t, "a", sliceByRange(src, diag.Range), "diagnostic range covers just the back-edge dep token")

	// The back-edge lives in `task c => a`, line 6 (0-indexed) of src.
	require.Equal(t, protocol.UInteger(6), diag.Range.Start.Line)
}

func TestDocument_DiagnosticsUnresolvedVariableNarrowsToReference(t *testing.T) {
	// The parser leaves command-element positions zeroed, so the
	// analysis layer anchors unresolved-variable diagnostics on the
	// containing task. The LSP layer is expected to narrow back to the
	// `$NAME` token so the editor underlines just the reference, not
	// the whole task body.
	src := "task build {\n" +
		"    echo building $UNKNOWN\n" +
		"}\n"
	d := parse(testURI, src, 1)

	diags := d.diagnostics()
	var diag *protocol.Diagnostic
	for i := range diags {
		if strings.Contains(diags[i].Message, `"UNKNOWN"`) {
			diag = &diags[i]
			break
		}
	}
	require.NotNil(t, diag, "expected a diagnostic for undefined $UNKNOWN")
	require.Equal(t, "UNKNOWN", sliceByRange(src, diag.Range), "diagnostic range covers just the variable name")
}

func TestDocument_DiagnosticsUnresolvedVariableHandlesRepeatedReferences(t *testing.T) {
	// Two `$missing` references inside the same task should produce
	// two distinct, narrowly-scoped diagnostics — not two diagnostics
	// that overlap on the same span.
	src := "task build {\n" +
		"    echo first $missing\n" +
		"    echo second $missing\n" +
		"}\n"
	d := parse(testURI, src, 1)

	var ranges []protocol.Range
	for _, diag := range d.diagnostics() {
		if strings.Contains(diag.Message, `"missing"`) {
			ranges = append(ranges, diag.Range)
		}
	}
	require.Len(t, ranges, 2)
	require.NotEqual(t, ranges[0], ranges[1], "each occurrence gets its own range")
	for _, r := range ranges {
		require.Equal(t, "missing", sliceByRange(src, r))
	}
}

func TestDocument_DiagnosticsEmptyForCleanFile(t *testing.T) {
	src := `
VERSION = "1.0.0"

task build {
    echo $VERSION
}
`
	d := parse(testURI, src, 1)
	require.Empty(t, d.diagnostics())
}

func TestDocument_DocumentSymbolsMirrorTopLevelDeclarations(t *testing.T) {
	src := `
VERSION = "1.0.0"

task build {
    echo building
}

namespace db {
    task migrate {
        echo migrating
    }
}
`
	d := parse(testURI, src, 1)

	symbols := d.documentSymbols()
	require.Len(t, symbols, 3)

	// Grouped by declaration kind: tasks first, then variables, then
	// namespaces. This matches QuakeFile field order, not the order
	// declarations appear in the source file.
	require.Equal(t, "build", symbols[0].Name)
	require.Equal(t, protocol.SymbolKindFunction, symbols[0].Kind)

	require.Equal(t, "VERSION", symbols[1].Name)
	require.Equal(t, protocol.SymbolKindVariable, symbols[1].Kind)

	require.Equal(t, "db", symbols[2].Name)
	require.Equal(t, protocol.SymbolKindNamespace, symbols[2].Kind)
	require.Len(t, symbols[2].Children, 1)
	require.Equal(t, "migrate", symbols[2].Children[0].Name)
}

func TestDocument_DefinitionResolvesTaskDependency(t *testing.T) {
	src := "task build {\n    echo hi\n}\n\ntask ship => build {\n    echo shipping\n}\n"
	d := parse(testURI, src, 1)

	// Point at "build" in the dependency list of `ship`.
	depOffset := indexOf(src, "=> build") + len("=> ")
	require.Equal(t, 'b', rune(src[depOffset]))

	pos := posAt(src, depOffset)
	loc := d.definition(pos)
	require.NotNil(t, loc, "definition should resolve to task build")
	require.Equal(t, testURI, loc.URI)

	// Definition should point to the line of `task build`.
	require.Equal(t, protocol.UInteger(0), loc.Range.Start.Line)
}

func TestDocument_DefinitionResolvesVariable(t *testing.T) {
	src := "VERSION = \"1.0\"\n\ntask show {\n    echo $VERSION\n}\n"
	d := parse(testURI, src, 1)

	// Point at "VERSION" after the `$`.
	off := indexOf(src, "echo $VERSION") + len("echo $")
	require.Equal(t, 'V', rune(src[off]))

	pos := posAt(src, off)
	loc := d.definition(pos)
	require.NotNil(t, loc)
	require.Equal(t, protocol.UInteger(0), loc.Range.Start.Line, "definition jumps to the top-level VERSION assignment")
}

func TestDocument_DefinitionNilForUnknownSymbol(t *testing.T) {
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)

	// Cursor inside "echo" — not a defined task or variable.
	off := indexOf(src, "echo")
	pos := posAt(src, off)
	require.Nil(t, d.definition(pos))
}

func TestDocument_ReferencesFindsTaskDependencies(t *testing.T) {
	src := "task build {\n    echo building\n}\n" +
		"task ship => build {\n    echo shipping\n}\n" +
		"task deploy => build, test {\n    echo deploying\n}\n" +
		"task test {\n    echo testing\n}\n"
	d := parse(testURI, src, 1)

	// Cursor on "build" in the ship task's dep list.
	off := indexOf(src, "=> build") + len("=> ")
	refs := d.references(posAt(src, off), false)
	require.Len(t, refs, 2, "both ship and deploy reference build")

	// Each returned range should cover exactly the word "build".
	for _, loc := range refs {
		require.Equal(t, testURI, loc.URI)
		require.Equal(t, "build", sliceByRange(src, loc.Range), "range should cover just the identifier")
	}
}

func TestDocument_ReferencesCanIncludeDeclaration(t *testing.T) {
	src := "task build {\n    echo building\n}\n" +
		"task ship => build {\n    echo shipping\n}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "=> build") + len("=> ")
	refs := d.references(posAt(src, off), true)
	require.Len(t, refs, 2, "declaration plus one use")

	// Declaration should come first (lower line).
	require.Less(t, refs[0].Range.Start.Line, refs[1].Range.Start.Line)
}

func TestDocument_ReferencesFindsVariableUses(t *testing.T) {
	src := "PROJECT = \"quake\"\n" +
		"\n" +
		"task build {\n" +
		"    echo building $PROJECT\n" +
		"    echo done with $PROJECT\n" +
		"}\n"
	d := parse(testURI, src, 1)

	// Cursor on the declaration.
	off := indexOf(src, "PROJECT = ")
	refs := d.references(posAt(src, off), false)
	require.Len(t, refs, 2)
	for _, loc := range refs {
		require.Equal(t, "PROJECT", sliceByRange(src, loc.Range))
	}
}

func TestDocument_ReferencesIgnoresSimilarNames(t *testing.T) {
	// PROJECT and PROJECT_ROOT must not alias.
	src := "PROJECT = \"q\"\n" +
		"PROJECT_ROOT = \"/tmp\"\n" +
		"\n" +
		"task show {\n" +
		"    echo $PROJECT $PROJECT_ROOT\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "PROJECT =")
	refs := d.references(posAt(src, off), false)
	require.Len(t, refs, 1, "only bare $PROJECT, not $PROJECT_ROOT")
}

func TestDocument_ReferencesNilForNonSymbol(t *testing.T) {
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)
	off := indexOf(src, "echo")
	require.Nil(t, d.references(posAt(src, off), true))
}

func TestDocument_HoverOnTask(t *testing.T) {
	src := "# Build the binary for a target.\n" +
		"task build(target) => test {\n" +
		"    echo building $target\n" +
		"}\n" +
		"\n" +
		"task test {\n" +
		"    echo testing\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "task build") + len("task ")
	h := d.hover(posAt(src, off))
	require.NotNil(t, h)

	content, ok := h.Contents.(protocol.MarkupContent)
	require.True(t, ok)
	require.Equal(t, protocol.MarkupKindMarkdown, content.Kind)
	require.Contains(t, content.Value, "build")
	require.Contains(t, content.Value, "target", "arguments are surfaced")
	require.Contains(t, content.Value, "test", "dependencies are surfaced")
	require.Contains(t, content.Value, "Build the binary", "description is surfaced")
}

func TestDocument_HoverOnVariable(t *testing.T) {
	src := "VERSION = \"1.0.0\"\n" +
		"\n" +
		"task show {\n" +
		"    echo $VERSION\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "echo $VERSION") + len("echo $")
	h := d.hover(posAt(src, off))
	require.NotNil(t, h)

	content := h.Contents.(protocol.MarkupContent)
	require.Contains(t, content.Value, "VERSION")
	require.Contains(t, content.Value, "1.0.0")
}

func TestDocument_HoverNilForNonSymbol(t *testing.T) {
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)
	off := indexOf(src, "echo")
	require.Nil(t, d.hover(posAt(src, off)))
}

func TestDocument_DocumentHighlightTaskReturnsDeclAndUses(t *testing.T) {
	src := "task build {\n    echo hi\n}\n" +
		"task ship => build {\n    echo shipping\n}\n" +
		"task deploy => build {\n    echo deploying\n}\n"
	d := parse(testURI, src, 1)

	// Cursor on the first use.
	off := indexOf(src, "ship => build") + len("ship => ")
	hl := d.documentHighlight(posAt(src, off))
	require.Len(t, hl, 3, "declaration plus two uses")

	// First entry is the declaration — Write.
	require.NotNil(t, hl[0].Kind)
	require.Equal(t, protocol.DocumentHighlightKind(protocol.DocumentHighlightKindWrite), *hl[0].Kind)

	// Subsequent entries are reads.
	for _, h := range hl[1:] {
		require.NotNil(t, h.Kind)
		require.Equal(t, protocol.DocumentHighlightKind(protocol.DocumentHighlightKindRead), *h.Kind)
	}
}

func TestDocument_ReferencesIgnoresArrowInsideCommandString(t *testing.T) {
	// "=>" inside a command body must not be read as a dep-list header.
	src := "task ship {\n    echo \"=> shipping\"\n}\n" +
		"task other => ship {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "task ship") + len("task ")
	refs := d.references(posAt(src, off), false)
	require.Len(t, refs, 1, "only the real dep-list reference counts")
	require.Equal(t, "ship", sliceByRange(src, refs[0].Range))
}

func TestDocument_ReferencesInsideNamespace(t *testing.T) {
	// The ref inside `namespace db` is written as "migrate" and the
	// ref outside is written as "db:migrate" — these are distinct keys
	// under the analysis model, so cursor on the declaration resolves
	// through the fully-qualified name.
	src := "namespace db {\n" +
		"    task migrate {\n" +
		"        echo migrating\n" +
		"    }\n" +
		"    task backup => migrate {\n" +
		"        echo backing up\n" +
		"    }\n" +
		"}\n" +
		"task deploy => db:migrate {\n" +
		"    echo deploying\n" +
		"}\n"
	d := parse(testURI, src, 1)

	// Cursor on "db:migrate" in deploy's dep list — qualifiedNameAt
	// extends across the colon so we look up by the fully-qualified key.
	off := indexOf(src, "=> db:migrate") + len("=> ")
	refs := d.references(posAt(src, off), false)
	require.Len(t, refs, 1, "only the qualified form appears outside the namespace")
	require.Equal(t, "db:migrate", sliceByRange(src, refs[0].Range))
}

func TestDocument_DocumentHighlightVariableOnUseSite(t *testing.T) {
	src := "PROJECT = \"q\"\n" +
		"\n" +
		"task build {\n" +
		"    echo $PROJECT\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "$PROJECT") + 1
	hl := d.documentHighlight(posAt(src, off))
	require.Len(t, hl, 2, "declaration plus one use")
}

// sliceByRange returns the text covered by an LSP Range in src.
// Handy for asserting a scan found a precise token rather than an
// over-wide range.
func sliceByRange(src string, r protocol.Range) string {
	li := newLineIndex(src)
	start := offsetFor(li, r.Start)
	end := offsetFor(li, r.End)
	return src[start:end]
}

// offsetFor is the inverse of lineIndex.position, adequate for the
// ASCII-only test fixtures used in this file.
func offsetFor(li *lineIndex, p protocol.Position) int {
	return li.lineStarts[p.Line] + int(p.Character)
}

// posAt converts a byte offset in text to an LSP Position for test
// readability. Production code goes the other way.
func posAt(text string, offset int) protocol.Position {
	li := newLineIndex(text)
	return li.position(offset)
}

// indexOf is strings.Index wrapped with a panic on not-found, so
// test setup fails loudly rather than silently passing -1 into the
// subsequent assertions.
func indexOf(haystack, needle string) int {
	i := strings.Index(haystack, needle)
	if i < 0 {
		panic("indexOf: needle not found: " + needle)
	}
	return i
}
