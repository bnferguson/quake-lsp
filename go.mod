module github.com/bnferguson/quake-lsp

go 1.25.1

// Pin the parser to bnferguson/quake's lsp/01-ast-positions branch
// until upstream miren/quake absorbs the position-tracking changes.
// See ../quake-meta/STANDALONE_PLAN.md for the upstreaming plan.
require miren.dev/quake v0.0.0-20260423211045-d13a2a459f10

require (
	github.com/stretchr/testify v1.11.1
	github.com/tliron/commonlog v0.2.21
	github.com/tliron/glsp v0.2.2
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/iancoleman/strcase v0.3.0 // indirect
	github.com/lab47/peggysue v0.0.0-20250702204832-6234b23da0a5 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/petermattis/goid v0.0.0-20250813065127-a731cc31b4fe // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sasha-s/go-deadlock v0.3.6 // indirect
	github.com/segmentio/ksuid v1.0.4 // indirect
	github.com/sourcegraph/jsonrpc2 v0.2.0 // indirect
	github.com/tliron/go-kutil v0.4.0 // indirect
	golang.org/x/crypto v0.42.0 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/term v0.35.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace miren.dev/quake => github.com/bnferguson/quake v0.0.0-20260423211045-d13a2a459f10
