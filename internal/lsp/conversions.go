// Package lsp implements the Quakefile language server. The package
// is internal because the LSP protocol layer has no reusable surface;
// the analysis work it relies on lives in public packages (parser,
// workspace, analysis).
package lsp

import (
	"net/url"
	"sort"
	"unicode/utf16"
	"unicode/utf8"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"miren.dev/quake/parser"
)

// lineIndex maps byte offsets in a source document to the LSP
// coordinates (0-based line, 0-based UTF-16 code unit) that clients
// expect. A single instance is built per text revision and reused by
// every conversion.
//
// lineStarts[i] is the byte offset of the first rune on line i; the
// sentinel len(text) is appended so a final-line offset can be
// resolved without a special case.
type lineIndex struct {
	text       string
	lineStarts []int
}

// newLineIndex precomputes line boundaries for text.
func newLineIndex(text string) *lineIndex {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	starts = append(starts, len(text))
	return &lineIndex{text: text, lineStarts: starts}
}

// position converts a byte offset into an LSP Position. Offsets that
// fall outside the text are clamped to the end-of-document sentinel
// rather than returning an error — a stale position from a
// previous revision should degrade gracefully.
func (li *lineIndex) position(offset int) protocol.Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(li.text) {
		offset = len(li.text)
	}

	// lineStarts has a trailing len(text) sentinel, so the last real
	// line index is len(lineStarts)-2. Search returns the first entry
	// strictly greater than offset; the line we want is one before.
	hi := sort.Search(len(li.lineStarts), func(i int) bool {
		return li.lineStarts[i] > offset
	})
	line := min(max(hi-1, 0), len(li.lineStarts)-2)

	lineStart := li.lineStarts[line]
	return protocol.Position{
		Line:      protocol.UInteger(line),
		Character: protocol.UInteger(utf16Units(li.text[lineStart:offset])),
	}
}

// rangeOf converts a parser.Position's byte range into an LSP Range.
// A zero Position (End == 0) collapses to a single point at the start
// of the document so callers that emit diagnostics on a node without a
// populated Position still get a pointable location.
func (li *lineIndex) rangeOf(pos parser.Position) protocol.Range {
	if pos.End == 0 && pos.Start == 0 {
		return protocol.Range{}
	}
	return protocol.Range{
		Start: li.position(pos.Start),
		End:   li.position(pos.End),
	}
}

// utf16Units returns the number of UTF-16 code units in s. Pure ASCII
// — the common case for Quakefiles — is a tight byte-count loop; the
// branch into utf16.RuneLen only runs when a multi-byte rune appears.
func utf16Units(s string) int {
	n := 0
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			n++
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		n += utf16.RuneLen(r)
		i += size
	}
	return n
}

// uriToPath converts an LSP document URI into a filesystem path.
// `file://` URIs are decoded via url.URL.Path so percent-encoded
// bytes (spaces, non-ASCII) round-trip correctly. Input without a
// scheme is returned unchanged so a caller that already has a path
// can pass it through unconditionally.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" {
		return uri
	}
	return u.Path
}

// pathToURI converts a filesystem path to a `file://` URI.
func pathToURI(path string) string {
	u := &url.URL{Scheme: "file", Path: path}
	return u.String()
}

// wordAt returns the identifier that surrounds offset in text, or
// the empty string if the offset does not sit on (or immediately
// after) an identifier character. Identifier characters are letters,
// digits, and underscores — the same set the parser treats as
// `word`.
//
// offset is clamped into [0, len(text)]; callers can safely pass a
// position derived from a stale document revision.
func wordAt(text string, offset int) string {
	start, end, ok := identSpan(text, offset)
	if !ok {
		return ""
	}
	return text[start:end]
}

// qualifiedNameAt returns the identifier at offset, extended across
// any `:` separators on either side. This lets `db:migrate` resolve
// as a single symbol regardless of which half the cursor is on — or
// even when the cursor lands on the colon itself.
func qualifiedNameAt(text string, offset int) string {
	start, end, ok := identSpan(text, offset)
	if !ok {
		return ""
	}
	// Extend across `:`-separated segments. Both loops require a
	// leading/trailing identifier character so we never swallow a
	// stray colon.
	for start >= 2 && text[start-1] == ':' && isIdentByte(text[start-2]) {
		start -= 2
		for start > 0 && isIdentByte(text[start-1]) {
			start--
		}
	}
	for end+1 < len(text) && text[end] == ':' && isIdentByte(text[end+1]) {
		end += 2
		for end < len(text) && isIdentByte(text[end]) {
			end++
		}
	}
	return text[start:end]
}

// identSpan returns [start, end) bounding the identifier that covers
// offset, after clamping offset into [0, len(text)]. It reports ok
// false when neither the byte at offset nor the byte just before it
// is part of an identifier — a cursor in whitespace, for example.
//
// The "just before" allowance is the common LSP case where a client
// sends position one past the last character of a double-clicked
// word.
func identSpan(text string, offset int) (start, end int, ok bool) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}

	switch {
	case offset < len(text) && isIdentByte(text[offset]):
		// Cursor sits on the identifier.
	case offset > 0 && isIdentByte(text[offset-1]):
		// Cursor sits immediately after the identifier.
		offset--
	default:
		return 0, 0, false
	}

	start = offset
	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}
	end = offset + 1
	for end < len(text) && isIdentByte(text[end]) {
		end++
	}
	return start, end, true
}

func isIdentByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_':
		return true
	}
	return false
}
