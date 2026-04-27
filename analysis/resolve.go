package analysis

import "miren.dev/quake/parser"

// Task returns the task declaration for the given fully-qualified
// name, or nil if no such task exists. The returned pointer aliases
// into the source QuakeFile.
func (s *SymbolTable) Task(fqn string) *parser.Task {
	return s.tasks[fqn]
}

// Variable returns the variable declaration for the given
// fully-qualified name, or nil if no such variable exists.
func (s *SymbolTable) Variable(fqn string) *parser.Variable {
	return s.variables[fqn]
}

// Namespace returns the namespace declaration for the given
// fully-qualified name, or nil if no such namespace exists.
func (s *SymbolTable) Namespace(fqn string) *parser.Namespace {
	return s.namespaces[fqn]
}

// Lookup returns the symbol (any kind) with the given fully-qualified
// name and reports whether it was found.
func (s *SymbolTable) Lookup(fqn string) (Symbol, bool) {
	sym, ok := s.all[fqn]
	return sym, ok
}
