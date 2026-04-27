package lsp

import (
	"fmt"
	"sync"

	"github.com/tliron/commonlog"
	// Blank import registers the default stderr logging backend that
	// glsp/server expects. Without it, server startup panics.
	_ "github.com/tliron/commonlog/simple"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
)

// serverName is the name reported in the LSP initialize response.
const serverName = "quake-lsp"

// Serve runs a Quakefile language server over stdio until the client
// disconnects. Every handler reparses from scratch — Quakefiles are
// small and peggysue is not incremental — so freshness beats
// efficiency here.
func Serve(version string) error {
	// commonlog log level 1 = warning-and-above. We have to keep the
	// default backend out of stdout regardless, because the LSP
	// contract reserves stdout for protocol traffic.
	commonlog.Configure(1, nil)

	s := newServer(version)
	srv := server.NewServer(&s.handler, serverName, false)
	return srv.RunStdio()
}

// documentStore owns the set of open documents. Every handler that
// reads or writes a document acquires the mutex; documents
// themselves are immutable once stored, so readers can safely
// dereference after releasing the lock.
type documentStore struct {
	mu   sync.RWMutex
	docs map[string]*document
}

func newDocumentStore() *documentStore {
	return &documentStore{docs: make(map[string]*document)}
}

func (ds *documentStore) put(d *document) {
	ds.mu.Lock()
	ds.docs[d.uri] = d
	ds.mu.Unlock()
}

func (ds *documentStore) get(uri string) *document {
	ds.mu.RLock()
	d := ds.docs[uri]
	ds.mu.RUnlock()
	return d
}

func (ds *documentStore) delete(uri string) {
	ds.mu.Lock()
	delete(ds.docs, uri)
	ds.mu.Unlock()
}

// quakeServer binds the glsp handler struct to its document store.
// glsp's handler fields are bare funcs, so we build the struct from
// closures over the server instance.
type quakeServer struct {
	version string
	docs    *documentStore
	handler protocol.Handler
}

func newServer(version string) *quakeServer {
	s := &quakeServer{
		version: version,
		docs:    newDocumentStore(),
	}
	s.handler = protocol.Handler{
		Initialize:                    s.initialize,
		Initialized:                   s.initialized,
		Shutdown:                      s.shutdown,
		SetTrace:                      s.setTrace,
		TextDocumentDidOpen:           s.didOpen,
		TextDocumentDidChange:         s.didChange,
		TextDocumentDidSave:           s.didSave,
		TextDocumentDidClose:          s.didClose,
		TextDocumentDocumentSymbol:    s.documentSymbol,
		TextDocumentDefinition:        s.definition,
		TextDocumentReferences:        s.references,
		TextDocumentHover:             s.hover,
		TextDocumentDocumentHighlight: s.documentHighlight,
		TextDocumentCompletion:        s.completion,
		TextDocumentPrepareRename:     s.prepareRename,
		TextDocumentRename:            s.rename,
		WorkspaceSymbol:               s.workspaceSymbol,
	}
	return s
}

func (s *quakeServer) initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	caps := s.handler.CreateServerCapabilities()

	syncKind := protocol.TextDocumentSyncKindFull
	openClose := true
	caps.TextDocumentSync = protocol.TextDocumentSyncOptions{
		OpenClose: &openClose,
		Change:    &syncKind,
	}
	caps.DocumentSymbolProvider = true
	caps.DefinitionProvider = true
	caps.ReferencesProvider = true
	caps.HoverProvider = true
	caps.DocumentHighlightProvider = true
	caps.CompletionProvider = &protocol.CompletionOptions{
		// "$" triggers variable completion, "," extends a dep list,
		// ">" is the back half of "=>" so the first task candidate
		// appears the moment the user finishes typing the arrow. " "
		// re-fires completion after "=> " and after ", " — without
		// it, clients dismiss the popup the moment the user types
		// the space that normally separates dep-list entries. In
		// other contexts the classifier returns nil, so the space
		// trigger is effectively scoped to dep lists.
		TriggerCharacters: []string{"$", ",", ">", " "},
	}
	// Rename advertises prepareProvider so clients ask us whether a
	// rename can start at the cursor before prompting for a new name.
	prepareRename := true
	caps.RenameProvider = protocol.RenameOptions{PrepareProvider: &prepareRename}
	caps.WorkspaceSymbolProvider = true

	return protocol.InitializeResult{
		Capabilities: caps,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    serverName,
			Version: &s.version,
		},
	}, nil
}

func (s *quakeServer) initialized(ctx *glsp.Context, params *protocol.InitializedParams) error {
	return nil
}

func (s *quakeServer) shutdown(ctx *glsp.Context) error {
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func (s *quakeServer) setTrace(ctx *glsp.Context, params *protocol.SetTraceParams) error {
	protocol.SetTraceValue(params.Value)
	return nil
}

func (s *quakeServer) didOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	item := params.TextDocument
	d := parse(item.URI, item.Text, int32(item.Version))
	s.docs.put(d)
	s.publishDiagnostics(ctx, d)
	return nil
}

func (s *quakeServer) didChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	// With TextDocumentSyncKindFull the LSP spec guarantees exactly
	// one change, carrying the entire buffer. Anything else is a
	// client bug worth surfacing rather than silently coalescing.
	if len(params.ContentChanges) != 1 {
		return fmt.Errorf("didChange: expected 1 full-sync change, got %d", len(params.ContentChanges))
	}
	var text string
	switch c := params.ContentChanges[0].(type) {
	case protocol.TextDocumentContentChangeEventWhole:
		text = c.Text
	case protocol.TextDocumentContentChangeEvent:
		text = c.Text
	default:
		return fmt.Errorf("didChange: unexpected change variant %T", c)
	}

	d := parse(params.TextDocument.URI, text, int32(params.TextDocument.Version))
	s.docs.put(d)
	s.publishDiagnostics(ctx, d)
	return nil
}

func (s *quakeServer) didSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	// No-op by design. We don't advertise includeText, so save adds
	// nothing a full-sync didChange hasn't already delivered.
	return nil
}

func (s *quakeServer) didClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	s.docs.delete(params.TextDocument.URI)
	// Clients expect an empty diagnostic set on close so any stale
	// squiggles clear out.
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         params.TextDocument.URI,
		Diagnostics: []protocol.Diagnostic{},
	})
	return nil
}

func (s *quakeServer) documentSymbol(ctx *glsp.Context, params *protocol.DocumentSymbolParams) (any, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	return d.documentSymbols(), nil
}

func (s *quakeServer) definition(ctx *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	loc := d.definition(params.Position)
	if loc == nil {
		return nil, nil
	}
	return *loc, nil
}

func (s *quakeServer) references(ctx *glsp.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	return d.references(params.Position, params.Context.IncludeDeclaration), nil
}

func (s *quakeServer) hover(ctx *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	return d.hover(params.Position), nil
}

func (s *quakeServer) documentHighlight(ctx *glsp.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	return d.documentHighlight(params.Position), nil
}

func (s *quakeServer) completion(ctx *glsp.Context, params *protocol.CompletionParams) (any, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	return d.completion(params.Position), nil
}

func (s *quakeServer) prepareRename(ctx *glsp.Context, params *protocol.PrepareRenameParams) (any, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	r := d.prepareRename(params.Position)
	if r == nil {
		return nil, nil
	}
	return *r, nil
}

func (s *quakeServer) rename(ctx *glsp.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	d := s.docs.get(params.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	return d.rename(params.Position, params.NewName)
}

func (s *quakeServer) workspaceSymbol(ctx *glsp.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	return s.docs.workspaceSymbols(params.Query), nil
}

// publishDiagnostics sends the current diagnostic set for d to the
// client. A nil slice still renders the empty array on the wire,
// clearing prior squiggles.
func (s *quakeServer) publishDiagnostics(ctx *glsp.Context, d *document) {
	diags := d.diagnostics()
	if diags == nil {
		diags = []protocol.Diagnostic{}
	}
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         d.uri,
		Diagnostics: diags,
	})
}
