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

func TestDiagnose_DependencyCycleAnchorsOnBackEdge(t *testing.T) {
	// Cycle a -> b -> c -> a. The diagnostic should anchor on the
	// task that *closes* the cycle (c) and name the back-edge target
	// (a) in DepName, so the LSP layer can narrow the squiggle to the
	// `a` token in `task c => a`.
	qf := mustParse(t, `
task a => b {}
task b => c {}
task c => a {}
`)
	diags := Diagnose(qf)

	cycleDiags := filterBySeverityMsg(diags, "dependency cycle")
	require.Len(t, cycleDiags, 1)
	diag := cycleDiags[0]
	require.Equal(t, "a", diag.DepName, "DepName names the back-edge target")

	taskC := BuildSymbolTable(qf).Task("c")
	require.NotNil(t, taskC)
	require.Equal(t, taskC.Position, diag.Position, "diagnostic anchors on the closing task, not the cycle's first node")
}

func TestDiagnose_SelfCycleAnchorsOnTask(t *testing.T) {
	qf := mustParse(t, `task loop => loop {}`)
	diags := Diagnose(qf)

	cycleDiags := filterBySeverityMsg(diags, "dependency cycle")
	require.Len(t, cycleDiags, 1)
	require.Equal(t, "loop", cycleDiags[0].DepName)
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

func TestDiagnose_ShellLocalAssignmentsAreInScope(t *testing.T) {
	// `name=value` inside a task body is a shell-local assignment.
	// References later in the same task should resolve against it
	// rather than being flagged as undefined.
	qf := mustParse(t, `
task build {
    commit=$(git rev-parse HEAD)
    ldflags="-X main.commit=$commit"
    go build -ldflags "$ldflags"
}
`)
	require.Empty(t, Diagnose(qf))
}

func TestDiagnose_ShellEnvPrefixAssignmentsAreInScope(t *testing.T) {
	// "GOOS=linux GOARCH=amd64 go build" is bash env-prefix syntax —
	// every name=value before the actual command word is a shell
	// assignment.
	qf := mustParse(t, `
task ship {
    GOOS=linux GOARCH=amd64 go build -o build/app
    echo "$GOOS $GOARCH"
}
`)
	require.Empty(t, Diagnose(qf))
}

func TestDiagnose_ForLoopBindingsAreInScope(t *testing.T) {
	// `for X in ...; do` brings X into scope for the loop body and any
	// subsequent commands in the same task. Skipping this leaves real
	// Quakefiles with phantom warnings on the loop variable.
	qf := mustParse(t, `
task lint {
    for f in *.go; do
        echo "checking $f"
    done
}
`)
	require.Empty(t, Diagnose(qf))
}

func TestDiagnose_ReadBuiltinBindingsAreInScope(t *testing.T) {
	// `read -r name < file` and `while read -r line; do ... done` both
	// bind their target names. Multi-target reads bind every name.
	qf := mustParse(t, `
task ingest {
    read -r name < name.txt
    echo "hello $name"

    while read -r line; do
        echo "got $line"
    done < notes.txt

    read -r first second third < parts.txt
    echo "$first $second $third"
}
`)
	require.Empty(t, Diagnose(qf))
}

func TestDiagnose_NestedForLoopBindingsAccumulate(t *testing.T) {
	// Two for-loop bindings on one line both go into scope.
	qf := mustParse(t, `
task cross {
    for os in linux darwin; do for arch in amd64 arm64; do
        echo "$os/$arch"
    done; done
}
`)
	require.Empty(t, Diagnose(qf))
}

func TestDiagnose_ShellLocalForwardReferenceWarns(t *testing.T) {
	// Reference before assignment is still undefined at the point of
	// use — bash would expand it to an empty string.
	qf := mustParse(t, `
task build {
    echo $foo
    foo=bar
}
`)
	unresolved := filterBySeverityMsg(Diagnose(qf), "undefined variable")
	require.Len(t, unresolved, 1)
	require.Contains(t, unresolved[0].Message, `"foo"`)
}

func TestDiagnose_ShellEnvVarsAreInScope(t *testing.T) {
	// $HOME, $PATH, $USER, and $CI are inherited from the calling
	// shell, not declared in the Quakefile. They show up in nearly
	// every real task, and the analyzer flagging them as undefined
	// drowns the user in noise. A small built-in allowlist of
	// POSIX/standard env vars plus common CI vars covers the common
	// case without modeling the shell's environment.
	qf := mustParse(t, `
task release {
    echo "publishing as $USER from $HOME"
    cp build/iso "$HOME/bin/iso"
    if [ -n "$CI" ]; then echo "running on CI"; fi
    PATH="$HOME/.local/bin:$PATH" go build ./...
}
`)
	require.Empty(t, Diagnose(qf))
}

func TestDiagnose_NonAllowlistedShellVarStillWarns(t *testing.T) {
	// Allowlist covers known-stable env vars only; an unknown name
	// like $TYPO is still flagged so typos don't slip through.
	qf := mustParse(t, `
task build {
    echo $TYPO_GOES_HERE
}
`)
	unresolved := filterBySeverityMsg(Diagnose(qf), "undefined variable")
	require.Len(t, unresolved, 1)
	require.Contains(t, unresolved[0].Message, `"TYPO_GOES_HERE"`)
}

func TestDiagnose_QuotedExpressionInVariableWarns(t *testing.T) {
	// The Quake grammar doesn't interpolate {{...}} inside a quoted
	// string — the expression survives as literal text into the
	// runtime. That's almost always a mistake, so we warn.
	qf := mustParse(t, `
INSTALL_DIR = "{{env.HOME}}/bin"

task install {
    mkdir -p $INSTALL_DIR
}
`)
	diags := filterBySeverityMsg(Diagnose(qf), "quoted string")
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, `"INSTALL_DIR"`)
	require.Equal(t, SeverityWarning, diags[0].Severity)
}

func TestDiagnose_BareExpressionVariableDoesNotWarn(t *testing.T) {
	// Bare `{{env.HOME}}` (no surrounding quotes) is a parsed
	// expression, not a string — interpolation works correctly.
	qf := mustParse(t, `
HOME_DIR = {{env.HOME}}

task show {
    echo $HOME_DIR
}
`)
	require.Empty(t, filterBySeverityMsg(Diagnose(qf), "quoted string"))
}

func TestDiagnose_PlainQuotedStringDoesNotWarn(t *testing.T) {
	qf := mustParse(t, `
PROJECT = "quake"

task show {
    echo $PROJECT
}
`)
	require.Empty(t, Diagnose(qf))
}

func TestDiagnose_BacktickValueDoesNotWarn(t *testing.T) {
	// Backtick subshells are out of scope for this diagnostic — the
	// grammar treats their bodies as shell code, and {{...}} inside
	// them is its own (rarer) bug class.
	qf := mustParse(t, "VERSION = `echo {{env.HOME}}`\n")
	require.Empty(t, filterBySeverityMsg(Diagnose(qf), "quoted string"))
}

func TestDiagnose_QuotedExpressionInsideNamespaceWarns(t *testing.T) {
	// The walk should descend into namespaces.
	qf := mustParse(t, `
namespace deploy {
    TARGET = "{{env.RUNNER_OS}}-amd64"
}
`)
	diags := filterBySeverityMsg(Diagnose(qf), "quoted string")
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, `"deploy:TARGET"`)
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
