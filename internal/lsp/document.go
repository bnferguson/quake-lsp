package lsp

import (
	"fmt"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/bnferguson/quake-lsp/analysis"
	"miren.dev/quake/parser"
)

// document is the server's view of one open Quakefile. It owns the
// text, its parse result, and every derived index a handler might
// need. Handlers receive a fresh document whenever the client sends
// didOpen or didChange; replacing rather than mutating keeps the
// read path lock-free after the swap.
type document struct {
	uri     string
	version int32
	text    string
	lines   *lineIndex

	// quakeFile is nil when the parser failed. diagnostics still
	// reports a single entry in that case so the client has
	// something to surface.
	quakeFile *parser.QuakeFile

	// symbols is nil when quakeFile is nil. Every handler that needs
	// to resolve a name guards on a nil document first.
	symbols *analysis.SymbolTable

	// parseErr and parseOk capture the peggysue return shape so
	// diagnostics() can distinguish "unknown parse failure" from a
	// wrapped I/O-style error.
	parseErr error
	parseOk  bool
}

// parse reads text and returns a fully-populated document. It never
// errors — parse failures become diagnostics.
func parse(uri, text string, version int32) *document {
	d := &document{
		uri:     uri,
		version: version,
		text:    text,
		lines:   newLineIndex(text),
	}

	qf, ok, err := parser.ParseQuakefileWithSource(text, uriToPath(uri))
	d.parseOk = ok
	d.parseErr = err
	if err == nil && ok {
		d.quakeFile = &qf
		d.symbols = analysis.BuildSymbolTable(&qf)
	}
	return d
}

// diagnosticSource is the value reported on every published
// Diagnostic. Clients show this as a prefix ("quake:") so users can
// tell our diagnostics apart from another server's.
const diagnosticSource = "quake"

// diagnostics returns the full LSP diagnostic set for the document:
// a synthetic entry for parse failure, plus every structural problem
// analysis.Diagnose finds when the parse succeeded.
func (d *document) diagnostics() []protocol.Diagnostic {
	source := diagnosticSource
	if d.parseErr != nil {
		return []protocol.Diagnostic{{
			Range:    protocol.Range{},
			Severity: severityPtr(protocol.DiagnosticSeverityError),
			Source:   &source,
			Message:  fmt.Sprintf("parse error: %v", d.parseErr),
		}}
	}
	if !d.parseOk {
		return []protocol.Diagnostic{{
			Range:    protocol.Range{},
			Severity: severityPtr(protocol.DiagnosticSeverityError),
			Source:   &source,
			Message:  "unknown parse failure",
		}}
	}

	analysisDiags := analysis.DiagnoseWith(d.quakeFile, d.symbols)
	if len(analysisDiags) == 0 {
		return nil
	}

	// Diagnostics that anchor on a task's Position carry an optional
	// hint identifying the token a consumer should narrow to. We
	// consume scan results front-to-front per (task, name, kind),
	// so repeated references in one task get distinct ranges.
	type narrowKey struct {
		taskStart int
		name      string
		kind      narrowKind
	}
	pending := map[narrowKey][]span{}

	out := make([]protocol.Diagnostic, 0, len(analysisDiags))
	for _, diag := range analysisDiags {
		rng := d.lines.rangeOf(diag.Position)
		if name, kind, ok := narrowHint(diag); ok {
			key := narrowKey{taskStart: diag.Position.Start, name: name, kind: kind}
			refs, cached := pending[key]
			if !cached {
				refs = scanForNarrow(d.text, diag.Position, name, kind)
			}
			if len(refs) > 0 {
				rng = d.rangeOfSpan(refs[0])
				pending[key] = refs[1:]
			} else {
				pending[key] = refs
			}
		}
		out = append(out, protocol.Diagnostic{
			Range:    rng,
			Severity: severityPtr(toLSPSeverity(diag.Severity)),
			Source:   &source,
			Message:  diag.Message,
		})
	}
	return out
}

// narrowKind discriminates the scan strategy for a diagnostic that
// asked to be narrowed.
type narrowKind int

const (
	narrowVariable narrowKind = iota + 1
	narrowDependency
)

// narrowHint extracts the token and scan kind a diagnostic asks to
// be narrowed against, or reports ok=false when the diagnostic
// should keep its original Position.
func narrowHint(d analysis.Diagnostic) (name string, kind narrowKind, ok bool) {
	switch {
	case d.VarName != "":
		return d.VarName, narrowVariable, true
	case d.DepName != "":
		return d.DepName, narrowDependency, true
	}
	return "", 0, false
}

func scanForNarrow(src string, pos parser.Position, name string, kind narrowKind) []span {
	switch kind {
	case narrowVariable:
		return scanVariableRefs(src, pos.Start, pos.End, name)
	case narrowDependency:
		return scanDependencyRefs(src, pos, name)
	}
	return nil
}

// documentSymbols returns a hierarchical outline for the current
// file: top-level tasks and variables, then a nested entry per
// namespace with its own children.
func (d *document) documentSymbols() []protocol.DocumentSymbol {
	if d.quakeFile == nil {
		return nil
	}

	var out []protocol.DocumentSymbol
	for i := range d.quakeFile.Tasks {
		out = append(out, d.taskSymbol(&d.quakeFile.Tasks[i]))
	}
	for i := range d.quakeFile.Variables {
		out = append(out, d.variableSymbol(&d.quakeFile.Variables[i]))
	}
	for i := range d.quakeFile.Namespaces {
		out = append(out, d.namespaceSymbol(&d.quakeFile.Namespaces[i]))
	}
	return out
}

// definition returns the location of the symbol referenced at pos, or
// nil if no identifier sits under the cursor or the name is
// undefined. The lookup is intentionally forgiving: it resolves a
// qualified identifier (like `db:migrate`) regardless of which side
// of the colon the cursor is on, and tries both task and variable
// tables so the handler does not need to know which context it is in.
func (d *document) definition(pos protocol.Position) *protocol.Location {
	if d.symbols == nil {
		return nil
	}
	offset := int(pos.IndexIn(d.text))
	name := qualifiedNameAt(d.text, offset)
	if name == "" {
		return nil
	}
	if task := d.symbols.Task(name); task != nil {
		return d.locationOf(task.Position)
	}
	if v := d.symbols.Variable(name); v != nil {
		return d.locationOf(v.Position)
	}
	if ns := d.symbols.Namespace(name); ns != nil {
		return d.locationOf(ns.Position)
	}
	return nil
}

// references returns every in-document occurrence of the symbol at
// pos. When includeDecl is true the declaration itself is listed
// first. Returns nil when the cursor is not on a known symbol name.
func (d *document) references(pos protocol.Position, includeDecl bool) []protocol.Location {
	uses := d.findUses(pos)
	if uses == nil {
		return nil
	}
	out := make([]protocol.Location, 0, len(uses.refs)+1)
	if includeDecl && uses.decl != nil {
		out = append(out, protocol.Location{URI: d.uri, Range: d.rangeOfSpan(*uses.decl)})
	}
	for _, s := range uses.refs {
		out = append(out, protocol.Location{URI: d.uri, Range: d.rangeOfSpan(s)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hover renders a Markdown summary of the symbol at pos: a task's
// description, argument list, and dependencies, or a variable's
// value. Returns nil when the cursor is not on a known symbol.
func (d *document) hover(pos protocol.Position) *protocol.Hover {
	if d.symbols == nil {
		return nil
	}
	offset := int(pos.IndexIn(d.text))
	name := qualifiedNameAt(d.text, offset)
	if name == "" {
		return nil
	}

	if task := d.symbols.Task(name); task != nil {
		return &protocol.Hover{Contents: markdown(taskHoverMarkdown(name, task))}
	}
	if v := d.symbols.Variable(name); v != nil {
		return &protocol.Hover{Contents: markdown(variableHoverMarkdown(name, v))}
	}
	return nil
}

// documentHighlight returns every in-document occurrence of the
// symbol at pos. The declaration (if in this file) is marked with
// DocumentHighlightKindWrite; every use is marked Read. Returns nil
// when the cursor is not on a known symbol name.
func (d *document) documentHighlight(pos protocol.Position) []protocol.DocumentHighlight {
	uses := d.findUses(pos)
	if uses == nil {
		return nil
	}
	out := make([]protocol.DocumentHighlight, 0, len(uses.refs)+1)
	if uses.decl != nil {
		out = append(out, protocol.DocumentHighlight{
			Range: d.rangeOfSpan(*uses.decl),
			Kind:  highlightKindPtr(protocol.DocumentHighlightKindWrite),
		})
	}
	for _, s := range uses.refs {
		out = append(out, protocol.DocumentHighlight{
			Range: d.rangeOfSpan(s),
			Kind:  highlightKindPtr(protocol.DocumentHighlightKindRead),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// symbolUses captures every in-document site of a single symbol —
// its declaration (if present in this file) and every use. Both
// references() and documentHighlight() are thin projections over it.
type symbolUses struct {
	decl *span
	refs []span
}

// findUses resolves the symbol under pos and collects its
// in-document sites. An unknown identifier at the cursor still
// yields a result if it appears as a dep or variable reference —
// that's the "find references to undeclared X" case, useful for
// spotting typos.
func (d *document) findUses(pos protocol.Position) *symbolUses {
	if d.quakeFile == nil || d.symbols == nil {
		return nil
	}
	offset := int(pos.IndexIn(d.text))
	name := qualifiedNameAt(d.text, offset)
	if name == "" {
		return nil
	}

	uses := &symbolUses{}
	if task := d.symbols.Task(name); task != nil {
		if s, ok := taskNameSpan(d.text, task); ok {
			uses.decl = &s
		}
	} else if v := d.symbols.Variable(name); v != nil {
		if s, ok := variableNameSpan(d.text, v); ok {
			uses.decl = &s
		}
	}

	// Tasks and variables inhabit different namespaces in the source
	// (task deps after "=>", variables after "$"), so scanning both
	// kinds is both safe and cheap. Lets an undeclared name still
	// surface as a reference list.
	//
	// Known gap: qualifiedNameAt resolves the cursor literally, and
	// scanVariableRefs searches for the exact FQN. A variable declared
	// inside `namespace db` is keyed "db:URL" but written as "$URL" at
	// the use site, and the analysis/symbols resolution doesn't carry
	// namespace context. Rename inherits this; see
	// TestBug_RenameNamespacedVariableNotSupported.
	d.walkTasks(func(t *parser.Task) {
		uses.refs = append(uses.refs, scanDependencyRefs(d.text, t.Position, name)...)
		uses.refs = append(uses.refs, scanVariableRefs(d.text, t.Position.Start, t.Position.End, name)...)
	})

	if uses.decl == nil && len(uses.refs) == 0 {
		return nil
	}
	return uses
}

// walkTasks invokes f for every task in the document, including
// those nested inside namespaces at any depth.
func (d *document) walkTasks(f func(*parser.Task)) {
	if d.quakeFile == nil {
		return
	}
	for i := range d.quakeFile.Tasks {
		f(&d.quakeFile.Tasks[i])
	}
	for i := range d.quakeFile.Namespaces {
		walkNamespaceTasks(&d.quakeFile.Namespaces[i], f)
	}
}

func walkNamespaceTasks(n *parser.Namespace, f func(*parser.Task)) {
	for i := range n.Tasks {
		f(&n.Tasks[i])
	}
	for i := range n.Namespaces {
		walkNamespaceTasks(&n.Namespaces[i], f)
	}
}

// rangeOfSpan converts an internal byte span to an LSP Range using
// the document's cached line index.
func (d *document) rangeOfSpan(s span) protocol.Range {
	return protocol.Range{
		Start: d.lines.position(s.start),
		End:   d.lines.position(s.end),
	}
}

func highlightKindPtr(k protocol.DocumentHighlightKind) *protocol.DocumentHighlightKind {
	return &k
}

func markdown(value string) protocol.MarkupContent {
	return protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: value}
}

// taskHoverMarkdown renders a Markdown card for task t. Args are
// rendered as a parenthesized signature; dependencies are listed as
// a comma-separated tail so `build(target) => test, lint` reads
// naturally.
func taskHoverMarkdown(name string, t *parser.Task) string {
	var b strings.Builder
	b.WriteString("```quake\ntask ")
	b.WriteString(name)
	if len(t.Arguments) > 0 {
		b.WriteByte('(')
		b.WriteString(strings.Join(t.Arguments, ", "))
		b.WriteByte(')')
	}
	if len(t.Dependencies) > 0 {
		b.WriteString(" => ")
		b.WriteString(strings.Join(t.Dependencies, ", "))
	}
	b.WriteString("\n```")
	if t.Description != "" {
		b.WriteString("\n\n")
		b.WriteString(t.Description)
	}
	return b.String()
}

// variableHoverMarkdown renders a Markdown card for variable v. A
// plain string value is shown verbatim; expressions and backticks
// fall back to a generic label rather than trying to render the
// underlying AST.
func variableHoverMarkdown(name string, v *parser.Variable) string {
	var b strings.Builder
	b.WriteString("```quake\n")
	b.WriteString(name)
	b.WriteString(" = ")
	if s, ok := v.Value.(string); ok {
		b.WriteByte('"')
		b.WriteString(s)
		b.WriteByte('"')
	} else {
		b.WriteString("<expression>")
	}
	b.WriteString("\n```")
	return b.String()
}

func (d *document) taskSymbol(t *parser.Task) protocol.DocumentSymbol {
	r := d.lines.rangeOf(t.Position)
	return protocol.DocumentSymbol{
		Name:           t.Name,
		Detail:         taskDetail(t),
		Kind:           protocol.SymbolKindFunction,
		Range:          r,
		SelectionRange: r,
	}
}

func (d *document) variableSymbol(v *parser.Variable) protocol.DocumentSymbol {
	r := d.lines.rangeOf(v.Position)
	return protocol.DocumentSymbol{
		Name:           v.Name,
		Kind:           protocol.SymbolKindVariable,
		Range:          r,
		SelectionRange: r,
	}
}

func (d *document) namespaceSymbol(n *parser.Namespace) protocol.DocumentSymbol {
	r := d.lines.rangeOf(n.Position)

	var children []protocol.DocumentSymbol
	for i := range n.Tasks {
		children = append(children, d.taskSymbol(&n.Tasks[i]))
	}
	for i := range n.Variables {
		children = append(children, d.variableSymbol(&n.Variables[i]))
	}
	for i := range n.Namespaces {
		children = append(children, d.namespaceSymbol(&n.Namespaces[i]))
	}

	return protocol.DocumentSymbol{
		Name:           n.Name,
		Kind:           protocol.SymbolKindNamespace,
		Range:          r,
		SelectionRange: r,
		Children:       children,
	}
}

// locationOf converts a parser.Position to an LSP Location. A node
// from the same file reuses d.uri unchanged; only a foreign filename
// triggers a pathToURI conversion. Comparison is done in URI space
// so percent-encoded paths (spaces, non-ASCII) match canonically.
func (d *document) locationOf(pos parser.Position) *protocol.Location {
	uri := d.uri
	if pos.Filename != "" {
		if other := pathToURI(pos.Filename); other != d.uri {
			uri = other
		}
	}
	return &protocol.Location{
		URI:   uri,
		Range: d.lines.rangeOf(pos),
	}
}

// taskDetail renders the task's argument list as a short signature
// string, suitable for the Detail field of a DocumentSymbol. Returns
// nil when there are no arguments so clients render a bare name.
func taskDetail(t *parser.Task) *string {
	if len(t.Arguments) == 0 {
		return nil
	}
	s := "("
	for i, a := range t.Arguments {
		if i > 0 {
			s += ", "
		}
		s += a
	}
	s += ")"
	return &s
}

// toLSPSeverity maps an analysis severity into its LSP counterpart.
// The default arm degrades unknown values to Information rather than
// dropping the diagnostic — extend the switch when analysis grows a
// new level.
func toLSPSeverity(s analysis.Severity) protocol.DiagnosticSeverity {
	switch s {
	case analysis.SeverityError:
		return protocol.DiagnosticSeverityError
	case analysis.SeverityWarning:
		return protocol.DiagnosticSeverityWarning
	default:
		return protocol.DiagnosticSeverityInformation
	}
}

func severityPtr(s protocol.DiagnosticSeverity) *protocol.DiagnosticSeverity {
	return &s
}
