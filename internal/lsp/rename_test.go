package lsp

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestPrepareRename_ReturnsSpanOfLocalName(t *testing.T) {
	src := "task build {\n    echo hi\n}\n" +
		"task ship => build {\n    echo shipping\n}\n"
	d := parse(testURI, src, 1)

	// Cursor on the dep-list "build".
	off := indexOf(src, "=> build") + len("=> ")
	r := d.prepareRename(posAt(src, off))
	require.NotNil(t, r)
	// The range should cover exactly the identifier "build".
	require.Equal(t, "build", sliceByRange(src, *r))
}

func TestPrepareRename_NilForUnknownSymbol(t *testing.T) {
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "echo")
	require.Nil(t, d.prepareRename(posAt(src, off)))
}

func TestPrepareRename_NilOnNamespacePrefix(t *testing.T) {
	// "db" in "db:migrate" is not a rename target: renaming a namespace
	// is a different, larger operation than renaming a task.
	src := "namespace db {\n" +
		"    task migrate {\n" +
		"        echo migrating\n" +
		"    }\n" +
		"}\n" +
		"task deploy => db:migrate {\n" +
		"    echo deploying\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "=> db:migrate") + len("=> ")
	require.Nil(t, d.prepareRename(posAt(src, off)), "cursor on namespace prefix should refuse rename")
}

func TestPrepareRename_AllowedOnLocalSegmentOfQualifiedName(t *testing.T) {
	src := "namespace db {\n" +
		"    task migrate {\n" +
		"        echo migrating\n" +
		"    }\n" +
		"}\n" +
		"task deploy => db:migrate {\n" +
		"    echo deploying\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "db:migrate") + len("db:")
	r := d.prepareRename(posAt(src, off))
	require.NotNil(t, r)
	require.Equal(t, "migrate", sliceByRange(src, *r))
}

func TestRename_RewritesTaskDeclAndRefs(t *testing.T) {
	src := "task build {\n    echo hi\n}\n" +
		"task ship => build {\n    echo shipping\n}\n" +
		"task deploy => build {\n    echo deploying\n}\n"
	d := parse(testURI, src, 1)

	// Cursor on the declaration.
	off := indexOf(src, "task build") + len("task ")
	edit, err := d.rename(posAt(src, off), "assemble")
	require.NoError(t, err)
	require.NotNil(t, edit)

	edits := edit.Changes[protocol.DocumentUri(testURI)]
	require.Len(t, edits, 3, "declaration plus two references")

	got := applyEdits(src, edits)
	require.Contains(t, got, "task assemble {")
	require.Contains(t, got, "task ship => assemble {")
	require.Contains(t, got, "task deploy => assemble {")
	require.NotContains(t, got, "build")
}

func TestRename_PreservesNamespacePrefixOnReferences(t *testing.T) {
	src := "namespace db {\n" +
		"    task migrate {\n" +
		"        echo migrating\n" +
		"    }\n" +
		"}\n" +
		"task deploy => db:migrate {\n" +
		"    echo deploying\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "db:migrate") + len("db:")
	edit, err := d.rename(posAt(src, off), "install")
	require.NoError(t, err)
	require.NotNil(t, edit)

	edits := edit.Changes[protocol.DocumentUri(testURI)]
	require.Len(t, edits, 2)
	got := applyEdits(src, edits)

	// Declaration rewrites the bare name; the reference keeps its
	// namespace prefix and only swaps the local segment.
	require.Contains(t, got, "task install {")
	require.Contains(t, got, "=> db:install {")
	require.NotContains(t, got, "migrate")
}

// TestBug_RenameNamespacedVariableNotSupported documents a known
// limitation: qualifiedNameAt resolves cursor text literally, so a
// cursor on "URL" inside `namespace db` looks up "URL" in the symbol
// table, while the table keyed the declaration as "db:URL". The
// lookup misses and rename returns nil. scanVariableRefs would also
// fail to cross the colon to match "$URL" against "db:URL" if the
// lookup succeeded. A proper fix needs namespace-context tracking in
// name resolution; this test freezes the current behavior so a
// future fix has a failing assertion to flip.
func TestBug_RenameNamespacedVariableNotSupported(t *testing.T) {
	src := "namespace db {\n" +
		"    URL = \"postgres://\"\n" +
		"    task connect {\n" +
		"        echo $URL\n" +
		"    }\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "URL = ")
	edit, err := d.rename(posAt(src, off), "ADDR")
	require.NoError(t, err)
	require.Nil(t, edit, "namespaced variables can't be renamed today; see document.findUses comment")
}

func TestRename_VariableAcrossUses(t *testing.T) {
	src := "PROJECT = \"quake\"\n" +
		"task show {\n" +
		"    echo $PROJECT\n" +
		"    echo done with $PROJECT\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "PROJECT =")
	edit, err := d.rename(posAt(src, off), "NAME")
	require.NoError(t, err)
	require.NotNil(t, edit)

	edits := edit.Changes[protocol.DocumentUri(testURI)]
	require.Len(t, edits, 3)
	got := applyEdits(src, edits)
	require.Contains(t, got, "NAME = \"quake\"")
	require.Contains(t, got, "echo $NAME")
	require.NotContains(t, got, "PROJECT")
}

func TestRename_RejectsInvalidNewName(t *testing.T) {
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "task build") + len("task ")

	cases := []struct {
		label, name string
	}{
		{"empty", ""},
		{"whitespace", "has space"},
		{"cross-namespace", "cross:namespace"},
		{"tab", "a\tb"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			edit, err := d.rename(posAt(src, off), tc.name)
			require.Error(t, err, "name %q should be rejected", tc.name)
			require.Nil(t, edit)
		})
	}
}

func TestRename_AcceptsWordRuleShapes(t *testing.T) {
	// Quake's grammar treats these characters as identifier chars.
	// Rename must accept them so existing tasks (e.g. "build-app",
	// "path/to/thing") remain renameable.
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "task build") + len("task ")
	for _, name := range []string{"build-app", "app.v2", "path/to/thing", "_leading", "2pm"} {
		edit, err := d.rename(posAt(src, off), name)
		require.NoError(t, err, "name %q should be accepted", name)
		require.NotNil(t, edit)
	}
}

func TestRename_NilForUnknownSymbol(t *testing.T) {
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "echo")
	edit, err := d.rename(posAt(src, off), "newname")
	require.NoError(t, err)
	require.Nil(t, edit)
}

func TestRename_NilForUnparsedBuffer(t *testing.T) {
	src := "task foo\n"
	d := parse(testURI, src, 1)
	edit, err := d.rename(posAt(src, 0), "other")
	require.NoError(t, err)
	require.Nil(t, edit)
}

// applyEdits returns src with the given edits applied. Splicing in
// descending order of Start keeps earlier offsets stable — the same
// strategy an LSP client uses on the real buffer.
func applyEdits(src string, edits []protocol.TextEdit) string {
	li := newLineIndex(src)
	type span struct {
		start, end int
		text       string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		spans = append(spans, span{
			start: offsetFor(li, e.Range.Start),
			end:   offsetFor(li, e.Range.End),
			text:  e.NewText,
		})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	out := src
	for _, s := range spans {
		out = out[:s.start] + s.text + out[s.end:]
	}
	return out
}
