package analysis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiagnose_UndefinedDependency(t *testing.T) {
	qf := mustParse(t, `
task build => missing {
    echo hi
}
`)
	diags := Diagnose(qf)

	require.Len(t, diags, 1)
	require.Equal(t, SeverityError, diags[0].Severity)
	require.Contains(t, diags[0].Message, `"build"`)
	require.Contains(t, diags[0].Message, `"missing"`)
}

func TestDiagnose_DependencyCycle(t *testing.T) {
	qf := mustParse(t, `
task a => b {}
task b => c {}
task c => a {}
`)
	diags := Diagnose(qf)

	// Exactly one cycle, whichever entry point DFS takes.
	cycleDiags := filterBySeverityMsg(diags, "dependency cycle")
	require.Len(t, cycleDiags, 1)
	require.Contains(t, cycleDiags[0].Message, "a")
	require.Contains(t, cycleDiags[0].Message, "b")
	require.Contains(t, cycleDiags[0].Message, "c")
}

func TestDiagnose_CycleReportedOnceRegardlessOfEntryPoint(t *testing.T) {
	// Three-node cycle. DFS will visit a, b, c in sorted order, but
	// the canonical cycleKey must collapse the repeated discovery
	// paths to a single diagnostic.
	qf := mustParse(t, `
task a => b {}
task b => c {}
task c => a {}
`)
	diags := Diagnose(qf)

	cycleDiags := filterBySeverityMsg(diags, "dependency cycle")
	require.Len(t, cycleDiags, 1, "one cycle, one diagnostic, regardless of traversal order")
}

func TestDiagnose_SelfCycle(t *testing.T) {
	qf := mustParse(t, `task loop => loop {}`)
	diags := Diagnose(qf)

	cycleDiags := filterBySeverityMsg(diags, "dependency cycle")
	require.Len(t, cycleDiags, 1)
	require.Contains(t, cycleDiags[0].Message, "loop")
}

func TestDiagnose_UnresolvedVariable(t *testing.T) {
	qf := mustParse(t, `
PROJECT = "quake"

task build {
    echo building $PROJECT
    echo leftover $UNKNOWN
}
`)
	diags := Diagnose(qf)

	unresolved := filterBySeverityMsg(diags, "undefined variable")
	require.Len(t, unresolved, 1)
	require.Contains(t, unresolved[0].Message, `"UNKNOWN"`)
	require.Equal(t, SeverityWarning, unresolved[0].Severity)
}

func TestDiagnose_TaskArgumentsDoNotWarn(t *testing.T) {
	qf := mustParse(t, `
task deploy(env) {
    echo deploying to $env
}
`)
	diags := Diagnose(qf)
	require.Empty(t, diags, "arguments shadow the missing-variable check")
}

func TestDiagnose_OrderedByFileAndLine(t *testing.T) {
	qf := mustParse(t, `
task a => missing1 {}

task b => missing2 {}
`)
	diags := Diagnose(qf)
	require.Len(t, diags, 2)
	require.Less(t, diags[0].Position.Line, diags[1].Position.Line)
}

func TestDiagnose_NilInputIsEmpty(t *testing.T) {
	require.Nil(t, Diagnose(nil))
}

func TestSeverity_String(t *testing.T) {
	require.Equal(t, "error", SeverityError.String())
	require.Equal(t, "warning", SeverityWarning.String())
}

func filterBySeverityMsg(diags []Diagnostic, substring string) []Diagnostic {
	var out []Diagnostic
	for _, d := range diags {
		if strings.Contains(d.Message, substring) {
			out = append(out, d)
		}
	}
	return out
}
