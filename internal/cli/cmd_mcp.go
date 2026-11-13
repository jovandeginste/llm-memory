package cli

import (
	"github.com/spf13/cobra"

	"llm-memory/internal/mcp"
	"llm-memory/internal/mcp/memories"
	"llm-memory/internal/mcpserver"
	"llm-memory/internal/memory"
	"llm-memory/internal/version"
)

// newMCPCmd serves the memory store over the Model Context Protocol.
func newMCPCmd(a *App) *cobra.Command {
	var addr string
	var stdio bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server",
		Long: `Serve the memory store over the Model Context Protocol.

By default the server listens on ` + mcpserver.DefaultAddr() + ` using the
streamable HTTP transport at /mcp. Use --stdio when an MCP client launches the
server as a child process. Logging always goes to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runMCP(a, addr, stdio)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", mcpserver.DefaultAddr(), "HTTP listen address")
	cmd.Flags().BoolVar(&stdio, "stdio", false, "serve over stdin/stdout instead of HTTP")

	return cmd
}

// runMCP opens the memory service and serves it until the process is signaled.
func runMCP(a *App, addr string, stdio bool) error {
	logger := mcpserver.NewLogger("llm-memory")

	ctx, stop := mcpserver.SignalContext()
	defer stop()

	svc, err := memory.Open(ctx, a.cfg)
	if err != nil {
		return err
	}

	module := memories.New(svc, logger)
	server := mcpserver.New("llm-memory", version.Version, []mcp.Module{module}, logger)

	if stdio {
		logger.Info("Serving MCP over stdio")

		return server.RunStdio(ctx)
	}

	logger.Info("Starting MCP server", "addr", addr)

	return server.Run(ctx, addr, nil)
}
