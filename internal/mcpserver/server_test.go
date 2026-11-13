package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"llm-memory/internal/config"
	"llm-memory/internal/embed"
	mcp "llm-memory/internal/mcp"
	"llm-memory/internal/mcp/memories"
	"llm-memory/internal/memory"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStreamableHTTPHandler(t *testing.T) {
	cfg := config.Defaults()
	cfg.DB = filepath.Join(t.TempDir(), "memory.db")
	cfg.Embedder = "hash"

	svc, err := memory.OpenWith(context.Background(), cfg, embed.NewHash(256))
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	module := memories.New(svc, logger)
	server := New("test", "0.1.0", []mcp.Module{module}, logger)

	httpServer := httptest.NewServer(server.handler())
	t.Cleanup(httpServer.Close)
	t.Cleanup(server.closeModules)

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: httpServer.URL,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	if !slices.Contains(names, "memory_add") || !slices.Contains(names, "memory_search") {
		t.Fatalf("memory tools are not registered: %v", names)
	}
}
