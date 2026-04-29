package analysis

import (
	"fmt"
	"sort"
	"strings"

	"miren.dev/quake/parser"
)

// Severity categorizes a diagnostic.
type Severity int

const (
	// SeverityError is a problem that prevents correct execution.
	SeverityError Severity = iota + 1
	// SeverityWarning is a suspect pattern that may still run.
	SeverityWarning
)

// String returns a human-readable label for the severity.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	default:
		return "unknown"
	}
}

// Diagnostic is a single semantic problem detected in a QuakeFile.
// Diagnostics are intentionally distinct from workspace-level
// warnings: workspace.Warnings captures I/O and parse failures,
// while a Diagnostic is produced for syntactically-valid code whose
// semantics are suspect.
type Diagnostic struct {
	Severity Severity
	Message  string
	Position parser.Position

	// VarName, when non-empty, names the `$<name>` token a consumer
	// should narrow Position to. Diagnostics for the same task and
	// VarName are emitted in source order, so the Nth diagnostic
	// corresponds to the Nth occurrence in the task body.
	VarName string

	// DepName, when non-empty, names a task-dependency token a
	// consumer should narrow Position to. The token sits in the
	// `=> ...` list of the task at Position. Same Nth-occurrence
	// contract as VarName.
	DepName string
}

// Diagnose builds a SymbolTable and returns every structural problem
// found in qf:
//
//   - undefined task dependency (`task build => missing`)
//   - dependency cycle (`build -> test -> build`)
//   - duplicate declaration at the same fully-qualified name
//   - unresolved variable reference in a task command (`echo $UNKNOWN`
//     where UNKNOWN is not a declared variable or a task argument)
//
// Callers that need the SymbolTable afterward should use DiagnoseWith
// to avoid rebuilding it.
func Diagnose(qf *parser.QuakeFile) []Diagnostic {
	if qf == nil {
		return nil
	}
	return DiagnoseWith(qf, BuildSymbolTable(qf))
}

// DiagnoseWith is Diagnose with a pre-built SymbolTable. The symbol
// table must have been built from qf; passing a mismatched pair
// yields meaningless diagnostics.
func DiagnoseWith(qf *parser.QuakeFile, symbols *SymbolTable) []Diagnostic {
	if qf == nil || symbols == nil {
		return nil
	}

	var diagnostics []Diagnostic
	diagnostics = append(diagnostics, duplicateDeclarations(symbols)...)
	diagnostics = append(diagnostics, undefinedDependencies(qf, symbols)...)
	diagnostics = append(diagnostics, dependencyCycles(qf, symbols)...)
	diagnostics = append(diagnostics, unresolvedVariables(qf, symbols)...)

	sort.SliceStable(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i], diagnostics[j]
		if a.Position.Filename != b.Position.Filename {
			return a.Position.Filename < b.Position.Filename
		}
		if a.Position.Line != b.Position.Line {
			return a.Position.Line < b.Position.Line
		}
		return a.Message < b.Message
	})
	return diagnostics
}

// duplicateDeclarations reports a diagnostic for every collision
// recorded by the SymbolTable build.
func duplicateDeclarations(symbols *SymbolTable) []Diagnostic {
	out := make([]Diagnostic, 0, len(symbols.duplicates))
	for _, sym := range symbols.duplicates {
		out = append(out, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("duplicate %s declaration: %q", sym.Kind, sym.Name),
			Position: sym.Position,
		})
	}
	return out
}

// undefinedDependencies reports every task dependency that does not
// resolve to a declared task.
func undefinedDependencies(qf *parser.QuakeFile, symbols *SymbolTable) []Diagnostic {
	var out []Diagnostic
	forEachTask(qf, "", func(fqn string, t *parser.Task) {
		for _, dep := range t.Dependencies {
			if symbols.Task(dep) == nil {
				out = append(out, Diagnostic{
					Severity: SeverityError,
					Message:  fmt.Sprintf("task %q depends on undefined task %q", fqn, dep),
					Position: t.Position,
					DepName:  dep,
				})
			}
		}
	})
	return out
}

// dependencyCycles reports one diagnostic per distinct cycle. It
// walks the dependency graph via DFS with white/gray/black coloring,
// and uses rotation-canonical cycle keys so the same cycle found at
// different entry points collapses to a single report.
//
// Each diagnostic anchors on the task that *closes* the cycle (the
// last node in the discovered path) and names the back-edge target
// in DepName, so a consumer can narrow the squiggle to the exact
// dep token that creates the loop.
func dependencyCycles(qf *parser.QuakeFile, symbols *SymbolTable) []Diagnostic {
	graph := buildDepGraph(qf, symbols)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(graph))
	seen := make(map[string]bool)
	var out []Diagnostic

	var visit func(node string, stack []string)
	visit = func(node string, stack []string) {
		color[node] = gray
		stack = append(stack, node)
		for _, next := range graph[node] {
			switch color[next] {
			case white:
				visit(next, stack)
			case gray:
				cycle := extractCycle(stack, next)
				key := cycleKey(cycle)
				if seen[key] {
					continue
				}
				seen[key] = true
				closing := cycle[len(cycle)-1]
				out = append(out, Diagnostic{
					Severity: SeverityError,
					Message:  fmt.Sprintf("dependency cycle: %s", strings.Join(append(cycle, cycle[0]), " -> ")),
					Position: symbols.Task(closing).Position,
					DepName:  cycle[0],
				})
			}
		}
		color[node] = black
	}

	names := make([]string, 0, len(graph))
	for name := range graph {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if color[name] == white {
			visit(name, nil)
		}
	}
	return out
}

// unresolvedVariables reports every $VAR reference in a task command
// that does not resolve to a declared Quake variable, a task
// argument, or a shell-local assignment from an earlier command in
// the same task.
//
// Shell-locals are recognized heuristically: a leading
// `IDENT=value` token in any command's flat string is treated as
// `IDENT` going into scope for subsequent commands. This catches
// the common patterns `commit=$(git rev-parse HEAD)` and
// `GOOS=linux GOARCH=amd64 go build ...` without modeling the
// shell's full grammar.
//
// Environment lookups (${VAR} at shell level, {{env.X}} in
// expressions) are deliberately ignored — their values are runtime
// concerns. See the LSP_PLAN "Open questions" section.
//
// Because parser.VariableElement positions are currently zeroed
// inside task commands, diagnostics anchor on the containing task's
// position. When command parsing is inlined into the main grammar,
// this will tighten up automatically.
func unresolvedVariables(qf *parser.QuakeFile, symbols *SymbolTable) []Diagnostic {
	var out []Diagnostic
	forEachTask(qf, "", func(fqn string, t *parser.Task) {
		args := make(map[string]struct{}, len(t.Arguments))
		for _, a := range t.Arguments {
			args[a] = struct{}{}
		}

		shellLocals := make(map[string]struct{})

		for _, cmd := range t.Commands {
			// Record this command's shell-local assignments before
			// scanning its variable refs, so `name="$name"` (a
			// self-reference in an assignment) doesn't warn.
			for _, elem := range cmd.Elements {
				se, ok := elem.(parser.StringElement)
				if !ok {
					continue
				}
				for _, name := range shellBindings(se.Value) {
					shellLocals[name] = struct{}{}
				}
			}
			for _, elem := range cmd.Elements {
				ve, ok := elem.(parser.VariableElement)
				if !ok {
					continue
				}
				if _, isArg := args[ve.Name]; isArg {
					continue
				}
				if symbols.Variable(ve.Name) != nil {
					continue
				}
				if _, isShellLocal := shellLocals[ve.Name]; isShellLocal {
					continue
				}
				out = append(out, Diagnostic{
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("task %q references undefined variable %q", fqn, ve.Name),
					Position: t.Position,
					VarName:  ve.Name,
				})
			}
		}
	})
	return out
}

// shellBindings returns every variable name a shell command in s
// brings into scope:
//
//   - `name=value` env-prefix assignments (shellAssignments).
//   - `for IDENT [in ...]` loop variables.
//   - `read [-flags] X Y Z` builtin targets.
//
// The shell isn't actually parsed; each helper looks for its
// keyword as a whitespace-bounded word and reads the obvious tokens
// that follow. The heuristic biases toward binding a name when in
// doubt — accidentally silencing a warning is preferable to flagging
// a clearly-defined loop variable. A `read` invocation that splits
// across multiple StringElements (e.g. an interpolated prompt like
// `read -p "$prompt" name`) is only scanned in the segment that
// contains the keyword, so the trailing target name may be missed.
func shellBindings(s string) []string {
	out := shellAssignments(s)
	out = append(out, forLoopBindings(s)...)
	out = append(out, readBindings(s)...)
	return out
}

// shellAssignments returns every name assigned via `name=value` in s,
// reading tokens from the start of the string up to the first
// non-assignment token. This mirrors bash env-prefix semantics —
// `GOOS=linux GOARCH=amd64 go build` declares GOOS and GOARCH but not
// anything after `go`. Quoted values aren't parsed; the heuristic
// treats the LHS up to `=` as a name and skips the rest of the token.
func shellAssignments(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		if !isShellIdentStart(s[i]) {
			return out
		}
		start := i
		for i < len(s) && isShellIdentByte(s[i]) {
			i++
		}
		if i >= len(s) || s[i] != '=' {
			return out
		}
		out = append(out, s[start:i])
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
	}
	return out
}

func isShellIdentStart(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b == '_':
		return true
	}
	return false
}

func isShellIdentByte(b byte) bool {
	return isShellIdentStart(b) || (b >= '0' && b <= '9')
}

// forLoopBindings finds every `for IDENT` clause in s and returns
// the loop-variable names. Handles `for X`, `for X in ...`, and
// `for X; do ...`. Multiple loops in one string accumulate, so
// `for i in a b; do for j in c d` binds both i and j.
func forLoopBindings(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		end := nextWord(s, "for", i)
		if end < 0 {
			return out
		}
		j := skipBlanks(s, end)
		if j < len(s) && isShellIdentStart(s[j]) {
			start := j
			for j < len(s) && isShellIdentByte(s[j]) {
				j++
			}
			out = append(out, s[start:j])
		}
		i = j
		if i == end {
			i = end + 1
		}
	}
	return out
}

// readBindings finds every `read` builtin invocation in s and
// returns the names it binds. Stops at separators that end the
// statement (`<`, `>`, `;`, `|`, `&`). Skips `-flag` tokens whether
// or not they take an argument; `read -p PROMPT name` therefore
// binds both PROMPT and name. That's a deliberate over-bind — the
// canonical bash idiom for prompts is exactly this shape, so
// silencing a spurious warning beats flagging a real binding.
func readBindings(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		end := nextWord(s, "read", i)
		if end < 0 {
			return out
		}
		j := end
		for {
			j = skipBlanks(s, j)
			if j >= len(s) {
				break
			}
			c := s[j]
			if c == ';' || c == '|' || c == '&' || c == '<' || c == '>' {
				break
			}
			if c == '-' {
				for j < len(s) && s[j] != ' ' && s[j] != '\t' {
					j++
				}
				continue
			}
			if !isShellIdentStart(c) {
				break
			}
			start := j
			for j < len(s) && isShellIdentByte(s[j]) {
				j++
			}
			out = append(out, s[start:j])
		}
		i = j
		if i == end {
			i = end + 1
		}
	}
	return out
}

// nextWord returns the byte index just past the next occurrence of
// word in s[start:] where word appears as a whitespace/control-
// bounded shell token. Returns -1 when no such occurrence exists.
func nextWord(s, word string, start int) int {
	for i := start; i+len(word) <= len(s); i++ {
		if s[i:i+len(word)] != word {
			continue
		}
		if i > 0 && !isShellWordSep(s[i-1]) {
			continue
		}
		end := i + len(word)
		if end < len(s) && !isShellWordSep(s[end]) {
			continue
		}
		return end
	}
	return -1
}

func skipBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// isShellWordSep reports whether b ends a shell word inside a
// single command segment. Newlines aren't included because the
// parser splits commands on them — a per-command string never
// contains one.
func isShellWordSep(b byte) bool {
	switch b {
	case ' ', '\t', ';', '|', '&':
		return true
	}
	return false
}

// forEachTask walks every task in qf, yielding its fully-qualified
// name and a pointer into the QuakeFile.
func forEachTask(qf *parser.QuakeFile, nsPrefix string, fn func(fqn string, t *parser.Task)) {
	for i := range qf.Tasks {
		t := &qf.Tasks[i]
		fn(qualify(nsPrefix, t.Name), t)
	}
	for i := range qf.Namespaces {
		forEachTaskInNamespace(&qf.Namespaces[i], nsPrefix, fn)
	}
}

func forEachTaskInNamespace(n *parser.Namespace, nsPrefix string, fn func(fqn string, t *parser.Task)) {
	name := qualify(nsPrefix, n.Name)
	for i := range n.Tasks {
		t := &n.Tasks[i]
		fn(qualify(name, t.Name), t)
	}
	for i := range n.Namespaces {
		forEachTaskInNamespace(&n.Namespaces[i], name, fn)
	}
}

// buildDepGraph returns an adjacency list keyed by fully-qualified
// task name. Only edges to defined tasks are kept, so cycle
// detection doesn't traverse into nonexistent nodes.
func buildDepGraph(qf *parser.QuakeFile, symbols *SymbolTable) map[string][]string {
	graph := make(map[string][]string)
	forEachTask(qf, "", func(fqn string, t *parser.Task) {
		var edges []string
		for _, dep := range t.Dependencies {
			if symbols.Task(dep) != nil {
				edges = append(edges, dep)
			}
		}
		graph[fqn] = edges
	})
	return graph
}

// extractCycle walks back through stack from the tail until it finds
// target, returning the cycle members in order.
func extractCycle(stack []string, target string) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == target {
			cycle := make([]string, len(stack)-i)
			copy(cycle, stack[i:])
			return cycle
		}
	}
	return []string{target}
}

// cycleKey returns a canonical identifier for a cycle, rotation- and
// order-independent so the same cycle discovered at different entry
// points collapses to one diagnostic.
func cycleKey(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}
	start := 0
	for i := 1; i < len(cycle); i++ {
		if cycle[i] < cycle[start] {
			start = i
		}
	}
	rotated := make([]string, 0, len(cycle))
	rotated = append(rotated, cycle[start:]...)
	rotated = append(rotated, cycle[:start]...)
	return strings.Join(rotated, "|")
}
