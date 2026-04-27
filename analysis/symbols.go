// Package analysis performs semantic analysis over a parsed Quakefile:
// it indexes declarations, cross-references uses against them, and
// reports structural problems. It is the layer between the parser
// (which knows syntax) and a consumer such as the CLI's `quake check`
// subcommand or the LSP server.
//
// The package takes a *parser.QuakeFile as input rather than a full
// *workspace.Workspace so it can be exercised on any AST — including
// a single-file parse — and keeps the dependency graph one-way.
//
// Namespace handling matches the evaluator: a task declared inside
// `namespace db { ... }` is named "db:migrate" workspace-wide. Tasks
// declared in a file with a top-level `namespace api` directive are
// NOT implicitly namespaced; FileNamespace is parsed but the
// evaluator does not resolve against it, so analysis follows suit.
// See evaluator.Evaluator.findTask for the resolution source of
// truth.
//
// When two declarations share a fully-qualified name (for example,
// two files defining `task build`), the first occurrence wins for
// lookup. A DuplicateDeclaration diagnostic reports the collision so
// the consumer can surface it.
package analysis

import (
	"sort"

	"miren.dev/quake/parser"
)

// Kind identifies the category of a declared symbol.
type Kind int

const (
	// KindUnknown is the zero value; a Symbol with this kind has
	// not been populated. It is returned from lookup helpers as
	// part of the (Symbol, false) miss case.
	KindUnknown Kind = iota
	// KindTask is a task declaration.
	KindTask
	// KindVariable is a variable assignment.
	KindVariable
	// KindNamespace is a namespace block.
	KindNamespace
)

// String returns a human-readable label for the kind.
func (k Kind) String() string {
	switch k {
	case KindTask:
		return "task"
	case KindVariable:
		return "variable"
	case KindNamespace:
		return "namespace"
	default:
		return "unknown"
	}
}

// Symbol is the minimal descriptor for a declaration: what kind it is,
// its fully-qualified name, and where it was defined.
type Symbol struct {
	Kind     Kind
	Name     string // fully-qualified: "build", "db:migrate", "PROJECT"
	Position parser.Position
}

// SymbolTable indexes every declaration in a QuakeFile.
//
// The maps store pointers that alias into the source QuakeFile. A
// SymbolTable is only valid while the source QuakeFile is stable;
// callers that mutate the QuakeFile should rebuild the table.
type SymbolTable struct {
	tasks      map[string]*parser.Task
	variables  map[string]*parser.Variable
	namespaces map[string]*parser.Namespace
	all        map[string]Symbol
	order      []string

	// duplicates records second-and-later occurrences of names that
	// collide on a fully-qualified key, so Diagnose can report them.
	duplicates []Symbol
}

// BuildSymbolTable walks qf and indexes every declaration. A nil qf
// yields an empty table.
func BuildSymbolTable(qf *parser.QuakeFile) *SymbolTable {
	s := &SymbolTable{
		tasks:      make(map[string]*parser.Task),
		variables:  make(map[string]*parser.Variable),
		namespaces: make(map[string]*parser.Namespace),
		all:        make(map[string]Symbol),
	}
	if qf == nil {
		return s
	}

	for i := range qf.Tasks {
		s.addTask("", &qf.Tasks[i])
	}
	for i := range qf.Variables {
		s.addVariable("", &qf.Variables[i])
	}
	for i := range qf.Namespaces {
		s.addNamespace("", &qf.Namespaces[i])
	}

	sort.Strings(s.order)
	return s
}

// All returns every indexed symbol sorted by fully-qualified name.
// The returned slice aliases internal state; callers must not mutate
// it.
func (s *SymbolTable) All() []Symbol {
	out := make([]Symbol, len(s.order))
	for i, name := range s.order {
		out[i] = s.all[name]
	}
	return out
}

// Tasks returns every indexed task symbol.
func (s *SymbolTable) Tasks() []Symbol {
	return s.filter(KindTask)
}

// Variables returns every indexed variable symbol.
func (s *SymbolTable) Variables() []Symbol {
	return s.filter(KindVariable)
}

// Namespaces returns every indexed namespace symbol.
func (s *SymbolTable) Namespaces() []Symbol {
	return s.filter(KindNamespace)
}

func (s *SymbolTable) addTask(nsPrefix string, t *parser.Task) {
	name := qualify(nsPrefix, t.Name)
	sym := Symbol{Kind: KindTask, Name: name, Position: t.Position}
	if _, exists := s.all[name]; exists {
		s.duplicates = append(s.duplicates, sym)
		return
	}
	s.tasks[name] = t
	s.all[name] = sym
	s.order = append(s.order, name)
}

func (s *SymbolTable) addVariable(nsPrefix string, v *parser.Variable) {
	name := qualify(nsPrefix, v.Name)
	sym := Symbol{Kind: KindVariable, Name: name, Position: v.Position}
	if _, exists := s.all[name]; exists {
		s.duplicates = append(s.duplicates, sym)
		return
	}
	s.variables[name] = v
	s.all[name] = sym
	s.order = append(s.order, name)
}

func (s *SymbolTable) addNamespace(nsPrefix string, n *parser.Namespace) {
	name := qualify(nsPrefix, n.Name)
	sym := Symbol{Kind: KindNamespace, Name: name, Position: n.Position}
	if _, exists := s.all[name]; !exists {
		s.namespaces[name] = n
		s.all[name] = sym
		s.order = append(s.order, name)
	} else {
		s.duplicates = append(s.duplicates, sym)
	}
	// Recurse regardless of duplication: children of the later
	// namespace block are still declared and referenceable.
	for i := range n.Tasks {
		s.addTask(name, &n.Tasks[i])
	}
	for i := range n.Variables {
		s.addVariable(name, &n.Variables[i])
	}
	for i := range n.Namespaces {
		s.addNamespace(name, &n.Namespaces[i])
	}
}

// filter returns every indexed symbol of the given kind, in
// name-sorted order. Returns nil when no symbols match.
func (s *SymbolTable) filter(kind Kind) []Symbol {
	var out []Symbol
	for _, name := range s.order {
		if sym := s.all[name]; sym.Kind == kind {
			out = append(out, sym)
		}
	}
	return out
}

// qualify joins a namespace prefix and a local name into a
// fully-qualified name. An empty prefix returns name unchanged.
func qualify(nsPrefix, name string) string {
	if nsPrefix == "" {
		return name
	}
	return nsPrefix + ":" + name
}
