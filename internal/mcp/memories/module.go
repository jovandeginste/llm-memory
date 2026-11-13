// Package memories exposes the self-linking memory engine as MCP tools.
package memories

import (
	"errors"
	"log/slog"

	mcp "llm-memory/internal/mcp"
	"llm-memory/internal/memory"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// errNothingToUpdate is returned when memory_update has no fields to apply.
var errNothingToUpdate = errors.New("nothing to update; pass at least one field")

// Module provides the memory, graph, and maintenance tools.
type Module struct {
	mcp.BaseModule
	svc *memory.Service
}

// New builds the memory module.
func New(svc *memory.Service, logger *slog.Logger) *Module {
	m := &Module{
		BaseModule: mcp.NewBaseModule(nil, logger),
		svc:        svc,
	}
	m.SetLogger(logger.With("module", "memories"))

	return m
}

// Register adds every tool to the server.
func (m *Module) Register(server *sdk.Server) error {
	m.Logger().Debug("Registering MCP package")
	registerMemoryTools(server, m)
	registerGraphTools(server, m)
	registerMaintenanceTools(server, m)

	return nil
}

// Enabled reports whether the module has a service to work with.
func (m *Module) Enabled() error {
	if m.svc == nil {
		return errors.New("memory service is not configured")
	}

	return nil
}

// Close closes the memory service and its database.
func (m *Module) Close() error {
	if m.svc == nil {
		return nil
	}

	return m.svc.Close()
}
