package lsp

import "miren.dev/quake/parser"

// span is a half-open byte range into the source document. The zero
// value is not a valid span.
type span struct {
	start, end int
}

// scanDependencyRefs returns the span of every dependency-list
// identifier that matches target within the task located at taskPos.
// Quake's grammar limits a dep list to a single line — it ends at the
// first "{" or newline after "=>" — so the scan stops at whichever
// boundary appears first.
//
// The returned span covers just the identifier, not any leading "=>"
// or ",". Returns nil when the task has no dep list.
func scanDependencyRefs(src string, taskPos parser.Position, target string) []span {
	if taskPos.End > len(src) || taskPos.Start < 0 || taskPos.Start >= taskPos.End {
		return nil
	}
	arrow := findArrow(src, taskPos.Start, taskPos.End)
	if arrow < 0 {
		return nil
	}

	cursor := arrow + len("=>")
	var out []span
	for cursor < taskPos.End {
		// Skip whitespace and commas between dep entries. "{" or "\n"
		// closes the list.
		for cursor < taskPos.End {
			switch src[cursor] {
			case ' ', '\t', ',':
				cursor++
				continue
			case '{', '\n':
				return out
			}
			break
		}
		if cursor >= taskPos.End {
			return out
		}

		// A dep may be namespaced ("db:migrate"); extend across ':'.
		start := cursor
		for cursor < taskPos.End && (isIdentByte(src[cursor]) || src[cursor] == ':') {
			cursor++
		}
		if cursor == start {
			// Unexpected byte — bail rather than loop forever.
			return out
		}
		if src[start:cursor] == target {
			out = append(out, span{start: start, end: cursor})
		}
	}
	return out
}

// scanVariableRefs returns the span of every "$name" occurrence in
// src[start:end] where name equals target. The returned span covers
// the identifier only (excluding the leading "$") so highlights line
// up with the LSP convention of marking the symbol, not its sigil.
func scanVariableRefs(src string, start, end int, target string) []span {
	if start < 0 {
		start = 0
	}
	if end > len(src) {
		end = len(src)
	}

	var out []span
	for i := start; i < end; i++ {
		if src[i] != '$' {
			continue
		}
		nameStart := i + 1
		j := nameStart
		for j < end && isIdentByte(src[j]) {
			j++
		}
		if j == nameStart {
			continue
		}
		if src[nameStart:j] == target {
			out = append(out, span{start: nameStart, end: j})
		}
		// Jump past the identifier we just consumed; the loop's i++
		// advances one more to land on the next candidate byte.
		i = j - 1
	}
	return out
}

// findArrow locates the first "=>" in src[lo:hi], stopping at the
// task body's opening "{" so an "=>" embedded in a command string
// doesn't read as a dep-list header. Returns -1 when no arrow
// precedes the body.
//
// This relies on the grammar never allowing a "{" inside the task
// header — no quoted task names, no braces before "=>". If that ever
// changes, the stop condition needs to become string-aware.
func findArrow(src string, lo, hi int) int {
	for i := lo; i+1 < hi; i++ {
		if src[i] == '{' {
			return -1
		}
		if src[i] == '=' && src[i+1] == '>' {
			return i
		}
	}
	return -1
}

// taskNameSpan returns the span of a task's name token within its
// declaration. Quake's grammar fixes the prefix as "task" plus at
// least one space; the name starts at the first non-whitespace byte
// after that and runs while identifier bytes continue.
func taskNameSpan(src string, t *parser.Task) (span, bool) {
	return declNameSpan(src, t.Position, "task")
}

// variableNameSpan returns the span of a variable's name token. The
// name starts at the declaration's Start offset and ends at the
// first non-identifier byte.
func variableNameSpan(src string, v *parser.Variable) (span, bool) {
	if v.Position.End > len(src) || v.Position.Start < 0 {
		return span{}, false
	}
	start := v.Position.Start
	end := start
	for end < v.Position.End && isIdentByte(src[end]) {
		end++
	}
	if end == start {
		return span{}, false
	}
	return span{start: start, end: end}, true
}

// declNameSpan locates the identifier that follows keyword at the
// start of pos's range. Returns ok=false when keyword isn't where
// expected — a mangled source buffer shouldn't crash the server.
func declNameSpan(src string, pos parser.Position, keyword string) (span, bool) {
	if pos.End > len(src) || pos.Start < 0 || pos.Start >= pos.End {
		return span{}, false
	}
	i := pos.Start
	if i+len(keyword) > pos.End || src[i:i+len(keyword)] != keyword {
		return span{}, false
	}
	i += len(keyword)
	for i < pos.End && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	start := i
	for i < pos.End && isIdentByte(src[i]) {
		i++
	}
	if i == start {
		return span{}, false
	}
	return span{start: start, end: i}, true
}
