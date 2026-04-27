package lsp

import (
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"miren.dev/quake/parser"
)

func TestLineIndex_PositionAtKnownOffsets(t *testing.T) {
	text := "alpha\nbeta\ngamma"
	li := newLineIndex(text)

	cases := []struct {
		label    string
		offset   int
		wantLine protocol.UInteger
		wantChar protocol.UInteger
	}{
		{"start of document", 0, 0, 0},
		{"mid first line", 3, 0, 3},
		{"start of second line", 6, 1, 0},
		{"start of third line", 11, 2, 0},
		{"end of document", 16, 2, 5},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := li.position(tc.offset)
			require.Equal(t, tc.wantLine, got.Line)
			require.Equal(t, tc.wantChar, got.Character)
		})
	}
}

func TestLineIndex_PositionClampsOutOfBoundsOffsets(t *testing.T) {
	li := newLineIndex("abc")

	// A stale offset from a previous revision should not panic; it
	// clamps to the end of the current document.
	got := li.position(999)
	require.Equal(t, protocol.UInteger(0), got.Line)
	require.Equal(t, protocol.UInteger(3), got.Character)

	got = li.position(-5)
	require.Equal(t, protocol.UInteger(0), got.Line)
	require.Equal(t, protocol.UInteger(0), got.Character)
}

func TestLineIndex_PositionCountsUTF16ForMultiByteRunes(t *testing.T) {
	// An emoji outside the BMP costs two UTF-16 code units even
	// though it's one rune and four UTF-8 bytes.
	text := "a😀b"
	li := newLineIndex(text)

	// Offset 5 is right before 'b': one ASCII byte + four bytes of
	// emoji. The emoji contributes two UTF-16 code units.
	got := li.position(5)
	require.Equal(t, protocol.UInteger(0), got.Line)
	require.Equal(t, protocol.UInteger(3), got.Character)
}

func TestLineIndex_RangeOfConvertsParserPosition(t *testing.T) {
	text := "one\ntwo\nthree"
	li := newLineIndex(text)

	// Span "two" → bytes [4, 7).
	r := li.rangeOf(parser.Position{Start: 4, End: 7, Line: 2})
	require.Equal(t, protocol.UInteger(1), r.Start.Line)
	require.Equal(t, protocol.UInteger(0), r.Start.Character)
	require.Equal(t, protocol.UInteger(1), r.End.Line)
	require.Equal(t, protocol.UInteger(3), r.End.Character)
}

func TestLineIndex_RangeOfCollapsesZeroPosition(t *testing.T) {
	// Command-element positions are deliberately zeroed today; the
	// conversion should hand back a zero range rather than a bogus
	// pointer into some other part of the document.
	li := newLineIndex("anything")
	r := li.rangeOf(parser.Position{})
	require.Equal(t, protocol.Range{}, r)
}

func TestWordAt_AtIdentifier(t *testing.T) {
	text := "task build => test"

	require.Equal(t, "task", wordAt(text, 0))
	require.Equal(t, "task", wordAt(text, 3))
	require.Equal(t, "build", wordAt(text, 5))
	require.Equal(t, "build", wordAt(text, 9))
	require.Equal(t, "test", wordAt(text, 15))
}

func TestWordAt_CursorJustPastWord(t *testing.T) {
	// Clients often send the position one past the last character of
	// the word the user double-clicked. Returning empty here would
	// break go-to-definition in practice.
	text := "task build"
	require.Equal(t, "build", wordAt(text, 10))
}

func TestWordAt_CursorInWhitespaceGap(t *testing.T) {
	// Two consecutive spaces: the cursor has no adjacent word.
	text := "task  build"
	require.Equal(t, "", wordAt(text, 5))
}

func TestQualifiedNameAt_ResolvesEitherSide(t *testing.T) {
	text := "  => db:migrate"
	// Cursor on 'db'.
	require.Equal(t, "db:migrate", qualifiedNameAt(text, 5))
	// Cursor on ':' between segments.
	require.Equal(t, "db:migrate", qualifiedNameAt(text, 7))
	// Cursor on 'migrate'.
	require.Equal(t, "db:migrate", qualifiedNameAt(text, 10))
	// Cursor one past the end of the qualified name.
	require.Equal(t, "db:migrate", qualifiedNameAt(text, len(text)))
}

func TestWordAt_StaleOffsets(t *testing.T) {
	// A stale offset from a previous, longer revision should clamp
	// rather than crash.
	require.Equal(t, "", wordAt("", 0))
	require.Equal(t, "", wordAt("", 99))
	require.Equal(t, "build", wordAt("build", 99))
	require.Equal(t, "build", wordAt("build", -5))
}

func TestQualifiedNameAt_StaleOffsets(t *testing.T) {
	require.Equal(t, "", qualifiedNameAt("", 0))
	require.Equal(t, "db:migrate", qualifiedNameAt("db:migrate", 999))
	require.Equal(t, "db:migrate", qualifiedNameAt("db:migrate", -1))
}

func TestURIPathRoundTrip(t *testing.T) {
	path := "/some/project/Quakefile"
	uri := pathToURI(path)
	require.Equal(t, "file:///some/project/Quakefile", uri)
	require.Equal(t, path, uriToPath(uri))
}
