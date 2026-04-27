package lsp

import (
	"sort"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/bnferguson/quake-lsp/analysis"
)

// workspaceSymbols returns every indexed symbol across the open
// document store whose fully-qualified name contains query as a
// substring (case-insensitive). An empty query returns every symbol,
// matching the Cmd-T "show me everything" UX.
//
// Scope is intentionally limited to documents the client has opened.
// Unopened `*_Quakefile` siblings still show once the client opens
// them; a richer cross-file implementation would plug the
// workspace.Workspace loader in here.
func (ds *documentStore) workspaceSymbols(query string) []protocol.SymbolInformation {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	q := strings.ToLower(query)
	var out []protocol.SymbolInformation
	for _, d := range ds.docs {
		if d.symbols == nil {
			continue
		}
		for _, sym := range d.symbols.All() {
			if q != "" && !strings.Contains(strings.ToLower(sym.Name), q) {
				continue
			}
			out = append(out, protocol.SymbolInformation{
				Name:     sym.Name,
				Kind:     analysisKindToLSP(sym.Kind),
				Location: protocol.Location{URI: d.uri, Range: d.lines.rangeOf(sym.Position)},
			})
		}
	}
	// Deterministic order: name-then-uri. Clients sort again by match
	// quality, but a stable baseline makes tests easy and shields
	// users from map-iteration jitter.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Location.URI < out[j].Location.URI
	})
	return out
}

// analysisKindToLSP maps an analysis.Kind to the LSP SymbolKind the
// workspace/symbol response expects. The default arm degrades to
// Null so an unrecognized kind is still findable in Cmd-T without
// being misclassified — extend the switch when analysis grows a new
// kind.
func analysisKindToLSP(k analysis.Kind) protocol.SymbolKind {
	switch k {
	case analysis.KindTask:
		return protocol.SymbolKindFunction
	case analysis.KindVariable:
		return protocol.SymbolKindVariable
	case analysis.KindNamespace:
		return protocol.SymbolKindNamespace
	default:
		return protocol.SymbolKindNull
	}
}
