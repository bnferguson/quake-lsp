package analysis

import "miren.dev/quake/parser"

// RefKind identifies what kind of use-site a Reference is.
type RefKind int

const (
	// RefTaskDependency is a `=> taskname` dependency reference.
	RefTaskDependency RefKind = iota + 1
	// RefVariable is a `$VAR` reference inside a command.
	RefVariable
)

// String returns a human-readable label for the reference kind.
func (k RefKind) String() string {
	switch k {
	case RefTaskDependency:
		return "task-dependency"
	case RefVariable:
		return "variable"
	default:
		return "unknown"
	}
}

// Reference records a single use-site of a symbol.
//
// Position is populated from the parser whenever available. Inside
// task commands, parser.VariableElement positions are currently
// zeroed (see parser.Position docs); in that case Position retains
// the zero value, and Container points at the containing task so the
// consumer can widen to a task-level range.
type Reference struct {
	Kind      RefKind
	Target    string // name as written (e.g. "build", "db:migrate", "PROJECT")
	From      string // fully-qualified name of the containing task
	Position  parser.Position
	Container parser.Position // position of the containing task
}

// HasPrecisePosition reports whether the Position field identifies
// the reference token exactly, or whether the consumer should fall
// back to Container. This is a workaround for the parser's
// command-element positions being zeroed today.
func (r Reference) HasPrecisePosition() bool {
	return r.Position.End > r.Position.Start || r.Position.Line > 0
}

// ReferenceIndex maps each referenced name to every use-site.
type ReferenceIndex struct {
	byTarget map[string][]Reference
	all      []Reference
}

// BuildReferenceIndex walks qf and indexes every dependency and
// variable reference. A nil qf yields an empty index.
//
// Targets are stored as written: a `=> migrate` inside `namespace db`
// is keyed under "migrate", not "db:migrate", because the Quake
// evaluator does not resolve unqualified names against an enclosing
// namespace. Consumers looking for "references to db:migrate" should
// query both the fully-qualified name and any in-namespace short
// form they care about.
func BuildReferenceIndex(qf *parser.QuakeFile) *ReferenceIndex {
	idx := &ReferenceIndex{
		byTarget: make(map[string][]Reference),
	}
	if qf == nil {
		return idx
	}

	for i := range qf.Tasks {
		idx.collectFromTask("", &qf.Tasks[i])
	}
	for i := range qf.Namespaces {
		idx.collectFromNamespace("", &qf.Namespaces[i])
	}
	return idx
}

// ReferencesTo returns every reference targeting name, in walk order.
// The returned slice aliases internal state; callers must not mutate
// it. Returns nil when no references exist.
func (r *ReferenceIndex) ReferencesTo(name string) []Reference {
	return r.byTarget[name]
}

// All returns every indexed reference in walk order. The returned
// slice aliases internal state; callers must not mutate it.
func (r *ReferenceIndex) All() []Reference {
	return r.all
}

func (r *ReferenceIndex) collectFromTask(nsPrefix string, t *parser.Task) {
	from := qualify(nsPrefix, t.Name)

	for _, dep := range t.Dependencies {
		r.add(Reference{
			Kind:      RefTaskDependency,
			Target:    dep,
			From:      from,
			Position:  t.Position,
			Container: t.Position,
		})
	}

	for _, cmd := range t.Commands {
		for _, elem := range cmd.Elements {
			if ve, ok := elem.(parser.VariableElement); ok {
				r.add(Reference{
					Kind:      RefVariable,
					Target:    ve.Name,
					From:      from,
					Position:  ve.Position,
					Container: t.Position,
				})
			}
		}
	}
}

func (r *ReferenceIndex) collectFromNamespace(nsPrefix string, n *parser.Namespace) {
	name := qualify(nsPrefix, n.Name)
	for i := range n.Tasks {
		r.collectFromTask(name, &n.Tasks[i])
	}
	for i := range n.Namespaces {
		r.collectFromNamespace(name, &n.Namespaces[i])
	}
}

func (r *ReferenceIndex) add(ref Reference) {
	r.byTarget[ref.Target] = append(r.byTarget[ref.Target], ref)
	r.all = append(r.all, ref)
}
