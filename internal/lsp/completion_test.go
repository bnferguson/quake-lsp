package lsp

import (
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestCompletion_DepListOffersTasks(t *testing.T) {
	src := "task build {\n    echo hi\n}\n" +
		"task test {\n    echo hi\n}\n" +
		"task ship => \n"
	d := parse(testURI, src, 1)

	// Cursor right after "=> " — classify as dep-list context. The
	// buffer doesn't parse as a `ship` task (no body), so we exercise
	// the text-only classifier.
	off := indexOf(src, "=> ") + len("=> ")
	items := d.completion(posAt(src, off))
	require.NotEmpty(t, items)

	labels := completionLabels(items)
	require.Contains(t, labels, "build")
	require.Contains(t, labels, "test")
	for _, item := range items {
		require.NotNil(t, item.Kind)
		require.Equal(t, protocol.CompletionItemKindFunction, *item.Kind)
	}
}

func TestCompletion_DepListAfterComma(t *testing.T) {
	// Partial second entry in a dep list. The buffer doesn't parse —
	// classifier must still see "=> build, " as dep-list context.
	src := "task build {\n    echo hi\n}\n" +
		"task test {\n    echo hi\n}\n" +
		"task ship => build, "
	d := parse(testURI, src, 1)

	off := len(src)
	items := d.completion(posAt(src, off))
	require.NotEmpty(t, items, "comma continuation still offers tasks")
	require.Contains(t, completionLabels(items), "test")
}

func TestCompletion_DepListIncludesNamespacedTask(t *testing.T) {
	src := "namespace db {\n" +
		"    task migrate {\n" +
		"        echo migrating\n" +
		"    }\n" +
		"}\n" +
		"task deploy => \n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "=> ") + len("=> ")
	items := d.completion(posAt(src, off))
	require.Contains(t, completionLabels(items), "db:migrate", "namespaced tasks surface by fully-qualified name")
}

func TestCompletion_DollarOffersVariables(t *testing.T) {
	src := "PROJECT = \"quake\"\n" +
		"VERSION = \"1.0.0\"\n" +
		"\n" +
		"task show {\n" +
		"    echo $\n" +
		"}\n"
	d := parse(testURI, src, 1)

	// Cursor sits immediately after the `$`.
	off := indexOf(src, "echo $") + len("echo $")
	items := d.completion(posAt(src, off))
	require.NotEmpty(t, items)
	for _, item := range items {
		require.NotNil(t, item.Kind)
		require.Equal(t, protocol.CompletionItemKindVariable, *item.Kind)
	}
	labels := completionLabels(items)
	require.Contains(t, labels, "PROJECT")
	require.Contains(t, labels, "VERSION")
}

func TestCompletion_DollarWithPrefixStillOffersAll(t *testing.T) {
	// Client-side filtering handles the prefix match; the server
	// returns the full set so the user can keep typing without a
	// server round-trip per byte.
	src := "PROJECT = \"quake\"\n" +
		"PROJECT_ROOT = \"/tmp\"\n" +
		"\n" +
		"task show {\n" +
		"    echo $PRO\n" +
		"}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "$PRO") + len("$PRO")
	items := d.completion(posAt(src, off))
	labels := completionLabels(items)
	require.Contains(t, labels, "PROJECT")
	require.Contains(t, labels, "PROJECT_ROOT")
}

func TestCompletion_InsideExpressionOffersVariables(t *testing.T) {
	// {{ ... }} expressions resolve identifiers against the same
	// variable set. "env" identifiers aren't declared, so the offered
	// set is just the declared variables — the client still filters by
	// what the user typed.
	src := "NAME = \"quake\"\n" +
		"\n" +
		"task show {\n" +
		"    echo {{ }}\n" +
		"}\n"
	d := parse(testURI, src, 1)

	// Offset points at the space between the braces.
	off := indexOf(src, "{{ ") + len("{{ ")
	items := d.completion(posAt(src, off))
	require.Contains(t, completionLabels(items), "NAME")
	for _, item := range items {
		require.NotNil(t, item.Kind)
		require.Equal(t, protocol.CompletionItemKindVariable, *item.Kind)
	}
}

func TestCompletion_NilOutsideKnownContext(t *testing.T) {
	src := "task build {\n    echo hi\n}\n"
	d := parse(testURI, src, 1)

	off := indexOf(src, "echo") + 2 // middle of the bare word "echo"
	require.Nil(t, d.completion(posAt(src, off)))
}

func TestCompletion_NilForUnparsedBuffer(t *testing.T) {
	// A parse failure leaves d.symbols nil; completion must return
	// nil rather than panic. Matches the rest of the handler surface
	// which no-ops on broken buffers.
	src := "task foo\n"
	d := parse(testURI, src, 1)
	require.Nil(t, d.symbols)
	require.Nil(t, d.completion(posAt(src, 0)))
}

func TestCompletion_DetailsSurfaceTaskSignatureAndDescription(t *testing.T) {
	src := "# Build the binary for a target.\n" +
		"task build(target) {\n" +
		"    echo $target\n" +
		"}\n" +
		"task ship => \n"
	d := parse(testURI, src, 1)

	items := d.completion(posAt(src, indexOf(src, "=> ")+len("=> ")))
	var got *protocol.CompletionItem
	for i := range items {
		if items[i].Label == "build" {
			got = &items[i]
			break
		}
	}
	require.NotNil(t, got)
	require.NotNil(t, got.Detail)
	require.Contains(t, *got.Detail, "target")

	doc, ok := got.Documentation.(protocol.MarkupContent)
	require.True(t, ok, "Documentation should be Markdown so the popup matches hover")
	require.Equal(t, protocol.MarkupKindMarkdown, doc.Kind)
	require.Contains(t, doc.Value, "Build the binary for a target.")
}

func TestCompletion_VariableDetailShowsLiteralValue(t *testing.T) {
	src := "VERSION = \"1.0.0\"\n" +
		"task show {\n" +
		"    echo $\n" +
		"}\n"
	d := parse(testURI, src, 1)

	items := d.completion(posAt(src, indexOf(src, "echo $")+len("echo $")))
	var got *protocol.CompletionItem
	for i := range items {
		if items[i].Label == "VERSION" {
			got = &items[i]
			break
		}
	}
	require.NotNil(t, got)
	require.NotNil(t, got.Detail)
	require.Contains(t, *got.Detail, "1.0.0")
}

func TestClassifyCompletion_Cases(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   completionKind
	}{
		{"arrow", "task ship => ", completionTaskName},
		{"arrow_with_partial", "task ship => bu", completionTaskName},
		{"after_comma", "task ship => build, ", completionTaskName},
		{"before_arrow", "task ship ", completionNone},
		{"after_dollar", "task s { echo $", completionVariableName},
		{"inside_expression", "task s { echo {{ ", completionExpressionIdent},
		{"past_expression", "task s { echo {{ X }} ", completionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCompletion(tc.source, len(tc.source))
			require.Equal(t, tc.want, got)
		})
	}
}

// completionLabels lifts the Label field off each item for easier
// set-style assertions.
func completionLabels(items []protocol.CompletionItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}
