package lsp

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// recordingContext captures every notification the server sends so
// tests can verify protocol-level behavior without standing up a
// real JSON-RPC transport.
type recordingContext struct {
	mu            sync.Mutex
	notifications []notification
}

type notification struct {
	method string
	params any
}

func (r *recordingContext) notify(method string, params any) {
	r.mu.Lock()
	r.notifications = append(r.notifications, notification{method, params})
	r.mu.Unlock()
}

func (r *recordingContext) ctx() *glsp.Context {
	return &glsp.Context{Notify: r.notify}
}

func (r *recordingContext) last() notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(nil, r.notifications)
	return r.notifications[len(r.notifications)-1]
}

func TestServer_DidCloseClearsDiagnostics(t *testing.T) {
	s := newServer("test")
	rec := &recordingContext{}
	ctx := rec.ctx()

	// Open a file that produces a diagnostic so "empty on close" is
	// a meaningful assertion rather than a no-op.
	err := s.didOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     testURI,
			Version: 1,
			Text:    "task build => missing {\n    echo hi\n}\n",
		},
	})
	require.NoError(t, err)

	rec.mu.Lock()
	openNotifs := len(rec.notifications)
	rec.mu.Unlock()
	require.Equal(t, 1, openNotifs, "didOpen publishes exactly one diagnostic notification")

	err = s.didClose(ctx, &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: testURI},
	})
	require.NoError(t, err)

	note := rec.last()
	require.Equal(t, protocol.ServerTextDocumentPublishDiagnostics, note.method)
	params, ok := note.params.(protocol.PublishDiagnosticsParams)
	require.True(t, ok)
	require.Equal(t, testURI, string(params.URI))
	require.NotNil(t, params.Diagnostics, "clients expect an empty slice, not null")
	require.Empty(t, params.Diagnostics)

	// Document is actually gone from the store.
	require.Nil(t, s.docs.get(testURI))
}

func TestServer_DidChangeRejectsUnexpectedChangeCount(t *testing.T) {
	s := newServer("test")
	rec := &recordingContext{}

	// Zero changes is a client bug under full-sync.
	err := s.didChange(rec.ctx(), &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: testURI},
			Version:                2,
		},
	})
	require.Error(t, err)
}

func TestServer_InitializeAdvertisesCompletionTriggers(t *testing.T) {
	// Space matters: clients dismiss the completion popup the moment
	// the user types a non-trigger character, so without " " in the
	// trigger list typing the space after "=>" or "," kills the
	// dep-list suggestions until the user types another ident char.
	s := newServer("test")
	result, err := s.initialize(nil, &protocol.InitializeParams{})
	require.NoError(t, err)

	init, ok := result.(protocol.InitializeResult)
	require.True(t, ok)
	require.NotNil(t, init.Capabilities.CompletionProvider)
	require.ElementsMatch(t, []string{"$", ",", ">", " "}, init.Capabilities.CompletionProvider.TriggerCharacters)
}

func TestDocumentStore_ConcurrentAccess(t *testing.T) {
	// Hammer put/get/delete from multiple goroutines under -race to
	// catch map races. The documents themselves are immutable, so
	// readers never need more than the get return value.
	ds := newDocumentStore()

	const workers = 8
	var counter atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				d := &document{uri: "file:///tmp/x"}
				ds.put(d)
				if got := ds.get("file:///tmp/x"); got != nil {
					counter.Add(1)
				}
				ds.delete("file:///tmp/x")
			}
		}()
	}
	wg.Wait()
	// Every successful get increments; the exact count is a race,
	// but we should have observed at least one non-nil read.
	require.Positive(t, counter.Load())
}
