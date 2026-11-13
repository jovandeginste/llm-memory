// Package mcpserver bootstraps the llm-memory MCP server: module lifecycle,
// HTTP and stdio transports, and graceful shutdown.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	mcp "llm-memory/internal/mcp"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const shutdownTimeout = 5 * time.Second

// Server wraps an MCP server and the modules that provide its tools.
type Server struct {
	name    string
	version string
	modules []mcp.Module
	logger  *slog.Logger
}

// New creates a server and initializes all modules. Disabled modules are
// skipped; initialization failures are logged and do not prevent startup.
func New(name, version string, modules []mcp.Module, logger *slog.Logger) *Server {
	enabled := make([]mcp.Module, 0, len(modules))

	for _, module := range modules {
		if err := module.Enabled(); err != nil {
			logger.Info("Module disabled", "module", fmt.Sprintf("%T", module), "reason", err)

			continue
		}

		if err := module.Initialize(); err != nil {
			logger.Error("failed to initialize module", "module", fmt.Sprintf("%T", module), "error", err)
		}

		enabled = append(enabled, module)
	}

	return &Server{
		name:    name,
		version: version,
		modules: enabled,
		logger:  logger,
	}
}

// Run listens on addr and shuts down gracefully when ctx is canceled.
func (s *Server) Run(ctx context.Context, addr string, extraRoutes func(*http.ServeMux)) error {
	defer s.closeModules()

	handler := s.handler()
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	if extraRoutes != nil {
		extraRoutes(mux)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	errCh := make(chan error, 1)

	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		return server.Shutdown(shutdownCtx)
	}
}

// RunStdio serves MCP over stdin/stdout until ctx is canceled or stdin closes.
func (s *Server) RunStdio(ctx context.Context) error {
	defer s.closeModules()

	if err := s.build().Run(ctx, &sdk.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

// DefaultAddr returns the default HTTP listen address.
func DefaultAddr() string {
	if addr := strings.TrimSpace(os.Getenv("LLM_MEMORY_MCP_ADDR")); addr != "" {
		return addr
	}

	return "127.0.0.1:8088"
}

func (s *Server) closeModules() {
	for _, module := range s.modules {
		if err := module.Close(); err != nil {
			s.logger.Error("failed to close module", "module", fmt.Sprintf("%T", module), "error", err)
		}
	}
}

func (s *Server) build() *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    s.name,
		Version: s.version,
	}, &sdk.ServerOptions{
		Logger: s.logger,
		Capabilities: &sdk.ServerCapabilities{
			Tools: &sdk.ToolCapabilities{ListChanged: false},
		},
	})

	for _, module := range s.modules {
		if err := module.Register(server); err != nil {
			s.logger.Error("failed to register module", "module", fmt.Sprintf("%T", module), "error", err)

			continue
		}

		s.logger.Debug("Module registered", "module", fmt.Sprintf("%T", module))
	}

	return server
}

func (s *Server) handler() http.Handler {
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return s.build()
	}, &sdk.StreamableHTTPOptions{
		Stateless: true,
		Logger:    s.logger,
	})
}
