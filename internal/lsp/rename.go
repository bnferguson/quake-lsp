package lsp

import (
	"fmt"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/bnferguson/quake-lsp/analysis"
)

// prepareRename tells the client whether a rename can start at pos and
// returns the range of the identifier that would be rewritten. Nil
// means "not renameable here" — the client should reject the action
// before prompting the user for a new name.
//
// Rename is only offered on the local-name segment of a task or
// variable. Standing on the namespace prefix of a qualified name
// ("db" in "db:migrate") is rejected because renaming a namespace
// is a different, larger operation.
func (d *document) prepareRename(pos protocol.Position) *protocol.Range {
	if d.symbols == nil {
		return nil
	}
	site := renameSiteAt(d, pos)
	if site == nil {
		return nil
	}
	r := d.rangeOfSpan(site.localSpan)
	return &r
}

// rename returns the set of edits that rewrite the symbol at pos to
// newName. Returns nil for positions that don't sit on a renameable
// symbol, and an error for a syntactically invalid new name so the
// client can surface the reason rather than silently accept a name
// that breaks the file.
//
// The site lookup runs before the name check: if the cursor isn't on
// a symbol, there's nothing to rename and the name is immaterial.
func (d *document) rename(pos protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	if d.symbols == nil {
		return nil, nil
	}
	site := renameSiteAt(d, pos)
	if site == nil {
		return nil, nil
	}
	if !isValidIdentifier(newName) {
		return nil, fmt.Errorf("invalid identifier %q: name must be non-empty and use only letters, digits, or '_', '.', '-', '/'", newName)
	}

	newFQN := withNewLocal(site.fqn, newName)

	edits := make([]protocol.TextEdit, 0, len(site.refs)+1)
	if site.decl != nil {
		edits = append(edits, protocol.TextEdit{
			Range:   d.rangeOfSpan(*site.decl),
			NewText: newName,
		})
	}
	for _, ref := range site.refs {
		edits = append(edits, protocol.TextEdit{
			Range:   d.rangeOfSpan(ref),
			NewText: newFQN,
		})
	}
	if len(edits) == 0 {
		return nil, nil
	}
	return &protocol.WorkspaceEdit{
		Changes: map[protocol.DocumentUri][]protocol.TextEdit{
			protocol.DocumentUri(d.uri): edits,
		},
	}, nil
}

// renameSite captures everything needed to rewrite a symbol: the
// fully-qualified name used to look it up, the byte span of the local
// name segment at the cursor, and the declaration + reference sites
// already present in the document.
type renameSite struct {
	fqn       string
	localSpan span
	decl      *span
	refs      []span
}

// renameSiteAt resolves the symbol under pos and returns the sites
// that have to move together, or nil if the cursor is not on a
// renameable identifier. Reuses findUses so rename stays consistent
// with references and documentHighlight.
func renameSiteAt(d *document, pos protocol.Position) *renameSite {
	offset := int(pos.IndexIn(d.text))
	start, end, ok := identSpan(d.text, offset)
	if !ok {
		return nil
	}
	fqn := qualifiedNameAt(d.text, offset)
	if fqn == "" {
		return nil
	}

	sym, ok := d.symbols.Lookup(fqn)
	if !ok {
		return nil
	}
	if sym.Kind != analysis.KindTask && sym.Kind != analysis.KindVariable {
		return nil
	}

	// Only rename when the cursor sits on the local-name segment of
	// the qualified name, not on a namespace prefix. Without this
	// check, renaming from "db" in "db:migrate" would silently mangle
	// the task name instead.
	if d.text[start:end] != localName(fqn) {
		return nil
	}

	uses := d.findUses(pos)
	if uses == nil {
		return nil
	}
	return &renameSite{
		fqn:       fqn,
		localSpan: span{start: start, end: end},
		decl:      uses.decl,
		refs:      uses.refs,
	}
}

// localName returns the last segment of a colon-qualified name. A
// bare name is returned unchanged.
func localName(fqn string) string {
	if i := strings.LastIndex(fqn, ":"); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// withNewLocal returns fqn with its last colon-segment replaced by
// newLocal. A bare name is replaced outright.
func withNewLocal(fqn, newLocal string) string {
	if i := strings.LastIndex(fqn, ":"); i >= 0 {
		return fqn[:i+1] + newLocal
	}
	return newLocal
}

// isValidIdentifier reports whether s is a legal local name for a
// Quake declaration. It matches the grammar's `word` rule minus ':':
// letters, digits, '_', '.', '-', '/'. Colons would be interpreted as
// a cross-namespace rewrite, which rename deliberately does not do.
func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isWordByte(s[i]) {
			return false
		}
	}
	return true
}

// isWordByte mirrors the character classes in the grammar's `word`
// rule minus the namespace separator ':'.
func isWordByte(b byte) bool {
	switch {
	case isIdentByte(b):
		return true
	case b == '.', b == '-', b == '/':
		return true
	}
	return false
}
