package memories

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"llm-memory/internal/config"
	"llm-memory/internal/embed"
	"llm-memory/internal/memory"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestModule opens a memory service backed by a temporary database and the
// offline hash embedder.
func newTestModule(t *testing.T) *Module {
	t.Helper()

	cfg := config.Defaults()
	cfg.DB = filepath.Join(t.TempDir(), "memory.db")
	cfg.Embedder = "hash"

	svc, err := memory.OpenWith(context.Background(), cfg, embed.NewHash(256))
	if err != nil {
		t.Fatal(err)
	}

	module := New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = module.Close() })

	return module
}

// newTestClient connects an in-memory MCP client to the module.
func newTestClient(t *testing.T, module *Module) *sdk.ClientSession {
	t.Helper()

	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	if err := module.Register(server); err != nil {
		t.Fatal(err)
	}

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

// callTool invokes a tool and decodes its structured output.
func callTool[T any](t *testing.T, session *sdk.ClientSession, name string, args map[string]any) T {
	t.Helper()

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned an error: %v", name, res.Content)
	}

	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s: decode output: %v", name, err)
	}

	return out
}

func TestToolsAreRegistered(t *testing.T) {
	session := newTestClient(t, newTestModule(t))

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	for _, want := range []string{
		"memory_add", "memory_search", "memory_get", "memory_list",
		"memory_update", "memory_delete", "memory_restore", "memory_reinforce",
		"memory_ingest", "memory_context", "memory_link", "memory_unlink",
		"graph_traverse", "graph_path", "graph_clusters", "graph_orphans",
		"memory_stats", "memory_decay", "memory_reindex",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("tool %q is not registered (got %v)", want, names)
		}
	}
}

func TestAddGetSearch(t *testing.T) {
	session := newTestClient(t, newTestModule(t))

	added := callTool[AddOutput](t, session, "memory_add", map[string]any{
		"content":    "SQLite stores memory vectors as little-endian blobs",
		"category":   "fact",
		"importance": 0.8,
		"tags":       []string{"sqlite", "storage"},
	})
	if added.ID == 0 {
		t.Fatal("added memory has no id")
	}
	if added.Category != "fact" || len(added.Tags) != 2 {
		t.Fatalf("unexpected memory: %+v", added)
	}

	got := callTool[GetOutput](t, session, "memory_get", map[string]any{"id": added.ID})
	if got.Content != added.Content {
		t.Fatalf("content = %q, want %q", got.Content, added.Content)
	}
	if got.Strength <= 0 {
		t.Fatalf("strength = %.2f, want > 0", got.Strength)
	}

	found := callTool[SearchOutput](t, session, "memory_search", map[string]any{
		"query": "sqlite vectors",
		"k":     5,
	})
	if found.Count == 0 {
		t.Fatal("search returned no results")
	}
	if !strings.Contains(strings.ToLower(found.Results[0].Content), "sqlite") {
		t.Fatalf("top result is not the sqlite memory: %+v", found.Results[0])
	}
}

func TestUpdateAndDelete(t *testing.T) {
	session := newTestClient(t, newTestModule(t))

	added := callTool[AddOutput](t, session, "memory_add", map[string]any{
		"content": "The deployment uses Apache",
	})

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "memory_update",
		Arguments: map[string]any{"id": added.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("memory_update without fields should fail")
	}

	updated := callTool[MemoryOutput](t, session, "memory_update", map[string]any{
		"id":         added.ID,
		"content":    "The deployment uses nginx",
		"importance": 0.9,
	})
	if updated.Content != "The deployment uses nginx" || updated.Importance != 0.9 {
		t.Fatalf("update did not apply: %+v", updated)
	}

	deleted := callTool[DeleteOutput](t, session, "memory_delete", map[string]any{"id": added.ID})
	if deleted.Action != "archived" {
		t.Fatalf("default delete action = %q, want archived", deleted.Action)
	}

	restored := callTool[MemoryOutput](t, session, "memory_restore", map[string]any{"id": added.ID})
	if restored.Archived {
		t.Fatal("restored memory is still archived")
	}
}

func TestGraphTools(t *testing.T) {
	session := newTestClient(t, newTestModule(t))

	first := callTool[AddOutput](t, session, "memory_add", map[string]any{"content": "alpha node"})
	second := callTool[AddOutput](t, session, "memory_add", map[string]any{"content": "beta node"})

	link := callTool[LinkOutput](t, session, "memory_link", map[string]any{
		"src": first.ID, "dst": second.ID, "similarity": 0.9,
	})
	if link.Similarity != 0.9 {
		t.Fatalf("similarity = %.2f, want 0.9", link.Similarity)
	}

	traversed := callTool[TraverseOutput](t, session, "graph_traverse", map[string]any{
		"id": first.ID, "hops": 1,
	})
	if traversed.Count != 2 {
		t.Fatalf("traversal reached %d nodes, want 2", traversed.Count)
	}

	path := callTool[PathOutput](t, session, "graph_path", map[string]any{
		"from": first.ID, "to": second.ID,
	})
	if path.Hops != 1 || len(path.Path) != 2 {
		t.Fatalf("unexpected path: %+v", path)
	}

	clusters := callTool[ClustersOutput](t, session, "graph_clusters", map[string]any{})
	if clusters.Count != 1 {
		t.Fatalf("clusters = %d, want 1", clusters.Count)
	}

	orphans := callTool[OrphansOutput](t, session, "graph_orphans", map[string]any{})
	if orphans.Count != 0 {
		t.Fatalf("orphans = %d, want 0", orphans.Count)
	}

	if _, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "memory_unlink",
		Arguments: map[string]any{"src": first.ID, "dst": second.ID},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStatsContextAndIngest(t *testing.T) {
	session := newTestClient(t, newTestModule(t))

	ingested := callTool[IngestOutput](t, session, "memory_ingest", map[string]any{
		"text":     "first paragraph\n\nsecond paragraph",
		"category": "fact",
	})
	if ingested.Count != 2 {
		t.Fatalf("ingested %d memories, want 2", ingested.Count)
	}

	stats := callTool[memory.Stats](t, session, "memory_stats", map[string]any{})
	if stats.Memories != 2 {
		t.Fatalf("stats memories = %d, want 2", stats.Memories)
	}

	contextOut := callTool[ContextOutput](t, session, "memory_context", map[string]any{"days": 7})
	if !strings.Contains(contextOut.Markdown, "# Memory Context") {
		t.Fatalf("unexpected context: %q", contextOut.Markdown)
	}

	reindexed := callTool[ReindexOutput](t, session, "memory_reindex", map[string]any{})
	if reindexed.Memories != 2 || reindexed.Dims != 256 {
		t.Fatalf("unexpected reindex result: %+v", reindexed)
	}
}

func TestDecayRequiresDryRun(t *testing.T) {
	session := newTestClient(t, newTestModule(t))

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "memory_decay",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("memory_decay without dry_run should fail")
	}

	dryRun := callTool[DecayOutput](t, session, "memory_decay", map[string]any{"dry_run": true})
	if dryRun.Scanned != 0 {
		t.Fatalf("scanned = %d, want 0", dryRun.Scanned)
	}
}
