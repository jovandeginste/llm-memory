// Package mcp defines the module contract for the llm-memory MCP server. A
// module registers its tools on a fresh SDK server and owns the resources it
// needs while the server runs.
package mcp

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Module is one unit of MCP tools.
type Module interface {
	SetLogger(logger *slog.Logger)
	Logger() *slog.Logger
	SetConfig(config any)
	Config() any
	Initialize() error
	Register(server *mcp.Server) error
	Enabled() error
	Close() error
}

// BaseModule provides no-op implementations for the optional module methods.
type BaseModule struct {
	logger *slog.Logger
	config any
}

// NewBaseModule builds a base module.
func NewBaseModule(config any, logger *slog.Logger) BaseModule {
	return BaseModule{
		config: config,
		logger: logger,
	}
}

// SetLogger replaces the module logger.
func (b *BaseModule) SetLogger(logger *slog.Logger) {
	b.logger = logger
}

// Logger returns the module logger.
func (b *BaseModule) Logger() *slog.Logger {
	return b.logger
}

// SetConfig replaces the module configuration.
func (b *BaseModule) SetConfig(config any) {
	b.config = config
}

// Config returns the module configuration.
func (b *BaseModule) Config() any {
	return b.config
}

// Initialize is a no-op.
func (b *BaseModule) Initialize() error {
	return nil
}

// Register is a no-op.
func (b *BaseModule) Register(_ *mcp.Server) error {
	return nil
}

// Enabled is a no-op.
func (b *BaseModule) Enabled() error {
	return nil
}

// Close is a no-op.
func (b *BaseModule) Close() error {
	return nil
}
