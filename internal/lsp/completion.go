package lsp

import (
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/bnferguson/quake-lsp/analysis"
)

// completion returns the candidates valid at pos, or nil when the
// cursor is not in a context that offers any. The full list is
// returned on every request; the client filters by the prefix under
// the cursor.
func (d *document) completion(pos protocol.Position) []protocol.CompletionItem {
	if d.symbols == nil {
		return nil
	}
	offset := int(pos.IndexIn(d.text))
	switch classifyCompletion(d.text, offset) {
	case completionTaskName:
		return taskCompletions(d.symbols)
	case completionVariableName, completionExpressionIdent:
		return variableCompletions(d.symbols)
	}
	return nil
}

// completionKind names the surface a completion request is offering.
type completionKind int

const (
	completionNone completionKind = iota
	// completionTaskName is a task reference in a dep list ("task foo => bar, |").
	completionTaskName
	// completionVariableName is a variable reference in a command ("echo $|").
	completionVariableName
	// completionExpressionIdent is an identifier inside a {{ ... }} expression.
	// Variables are the only identifiers the evaluator resolves there today.
	completionExpressionIdent
)

// classifyCompletion decides which completion surface (if any) applies
// at offset. The scan is intentionally text-only so completion works
// even while the user is mid-edit and the buffer doesn't parse.
func classifyCompletion(text string, offset int) completionKind {
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}

	// Identifier run that contains the cursor, if any. A cursor in
	// whitespace still classifies — we only need the start of the prefix
	// to look at what precedes it.
	start := offset
	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}

	// "$name" takes precedence: it's the tightest anchor and trumps any
	// outer context.
	if start > 0 && text[start-1] == '$' {
		return completionVariableName
	}

	// Inside "{{ ... }}" — variables (and, in principle, any identifier
	// the evaluator knows about) belong here. Checked before dep-list
	// detection because an expression can contain "=>" in ways that
	// aren't a task header (e.g. a future map literal). Today Quake
	// doesn't use "=>" inside expressions, but the precedence still
	// matches the grammar's nesting.
	if insideExpression(text, start) {
		return completionExpressionIdent
	}

	// Dep-list context: the grammar constrains a dep list to a single
	// line of identifiers, ':', and ',' after "=>". Scan left through
	// that alphabet; landing on "=>" means the cursor is still in the
	// list, anything else means we've crossed a boundary.
	i := start
	for i > 0 {
		c := text[i-1]
		if isIdentByte(c) || c == ':' || c == ',' || c == ' ' || c == '\t' {
			i--
			continue
		}
		break
	}
	if i >= 2 && text[i-1] == '>' && text[i-2] == '=' {
		return completionTaskName
	}
	return completionNone
}

// insideExpression reports whether offset sits inside an unclosed
// "{{ ... }}" span. The scan walks backward: a "}}" before a "{{"
// means we're past the expression, a "{{" without an intervening "}}"
// means we're inside one.
func insideExpression(text string, offset int) bool {
	if offset > len(text) {
		offset = len(text)
	}
	for i := offset - 1; i >= 1; i-- {
		if text[i] == '}' && text[i-1] == '}' {
			return false
		}
		if text[i] == '{' && text[i-1] == '{' {
			return true
		}
	}
	return false
}

// taskCompletions returns a CompletionItem for every indexed task,
// keyed by fully-qualified name so dep lists can reach into
// namespaces. Detail carries the argument signature; Documentation
// reuses the hover card so the completion popup and hover match.
func taskCompletions(s *analysis.SymbolTable) []protocol.CompletionItem {
	tasks := s.Tasks()
	if len(tasks) == 0 {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(tasks))
	for _, sym := range tasks {
		t := s.Task(sym.Name)
		item := protocol.CompletionItem{
			Label: sym.Name,
			Kind:  completionKindPtr(protocol.CompletionItemKindFunction),
		}
		if detail := taskDetail(t); detail != nil {
			item.Detail = detail
		}
		item.Documentation = markdown(taskHoverMarkdown(sym.Name, t))
		out = append(out, item)
	}
	return out
}

// variableCompletions returns a CompletionItem for every indexed
// variable. Variables inside namespaces surface under their
// fully-qualified name; a "$db:url" reference resolves the same way
// the evaluator does.
func variableCompletions(s *analysis.SymbolTable) []protocol.CompletionItem {
	vars := s.Variables()
	if len(vars) == 0 {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(vars))
	for _, sym := range vars {
		item := protocol.CompletionItem{
			Label: sym.Name,
			Kind:  completionKindPtr(protocol.CompletionItemKindVariable),
		}
		if v := s.Variable(sym.Name); v != nil {
			if str, ok := v.Value.(string); ok {
				detail := `"` + str + `"`
				item.Detail = &detail
			}
		}
		out = append(out, item)
	}
	return out
}

func completionKindPtr(k protocol.CompletionItemKind) *protocol.CompletionItemKind {
	return &k
}
