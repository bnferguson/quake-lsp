# quake-lsp

Language server for [Quakefile](https://github.com/mirendev/quake) build files.

Spun out from [bnferguson/quake](https://github.com/bnferguson/quake)'s
`lsp/*` branch stack so the LSP can iterate independently of the
upstream build tool.

## Install

```sh
go install github.com/bnferguson/quake-lsp/cmd/quake-lsp@main
```

No tagged releases yet — install from `main`.

## Editor setup

[`zed-quakefile`](https://github.com/bnferguson/zed-quakefile) drives
this binary. Other editors can launch `quake-lsp` over stdio; it
implements LSP 3.16.

## Status

The parser is imported from `bnferguson/quake`'s
[`lsp/01-ast-positions`](https://github.com/bnferguson/quake/tree/lsp/01-ast-positions)
branch via a `replace` directive in `go.mod`, pinned to a SHA. Once
that branch lands upstream the pin will move to `mirendev/quake`.

See [`STANDALONE_PLAN.md`](https://github.com/bnferguson/quake-meta/blob/lsp/meta/STANDALONE_PLAN.md)
for the upstreaming plan.
