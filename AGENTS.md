# AGENTS.md

Facts for automated agents and new contributors. This describes the current
repository, not a historical snapshot.

## Project type

- Go module with one binary, `llmmem`, that provides both a CLI and an MCP
  server.
- Targets Go 1.26.x (`go.mod`).
- Dependencies are not vendored.
- Store: SQLite via `modernc.org/sqlite` (pure Go). Vector index:
  `github.com/tamnd/vec`. Embeddings: Ollama, any OpenAI-compatible endpoint, or
  the built-in offline hash embedder.
- MCP server: `github.com/modelcontextprotocol/go-sdk`.

## Entrypoints

- `cmd/llmmem/` — the single `main` package.
- `llmmem <command>` — CLI.
- `llmmem mcp` — MCP server. Streamable HTTP by default on
  `127.0.0.1:8088` at `/mcp`; `--stdio` serves the same tools over
  stdin/stdout.

## Repository layout

- `cmd/llmmem/` — process entrypoint.
- `internal/cli/` — Cobra command tree, including `mcp`.
- `internal/config/` — JSON config plus `LLM_MEMORY_*` environment overrides.
- `internal/store/` — SQLite schema, memories, links, metadata, FTS5.
- `internal/index/` — `tamnd/vec` wrapper. The index is process-resident and
  rebuilt from SQLite at startup.
- `internal/embed/` — hash, Ollama, and OpenAI-compatible embedders.
- `internal/extract/` — optional LLM fact extraction for `ingest`.
- `internal/memory/` — the engine: add, hybrid search, auto-linking, graph,
  strength/decay, context rebuild.
- `internal/mcp/` — MCP module contract and `BaseModule`.
- `internal/mcp/memories/` — the MCP tool module and its parameter/output
  types.
- `internal/mcpserver/` — logger, module lifecycle, HTTP and stdio transports,
  graceful shutdown.
- `internal/version/` — shared version constant.
- `.opencode/skills/llm-memory/` — agent skill for the CLI and MCP tools.

## Commands

- `make build` — builds `bin/llmmem`.
- `make install` — installs `llmmem` in `$(go env GOPATH)/bin`.
- `make test` — `go test ./...`.
- `make vet` — `go vet ./...`.
- `make fmt` — `go fmt ./...`.
- `make skill-install` — copies the skill to
  `~/.config/opencode/skills/llm-memory`.
- `make run-mcp` — runs the MCP server over HTTP on the default address.

## MCP server

- `internal/mcpserver.New` checks `Enabled`, calls `Initialize`, and registers
  every module when a server is built.
- HTTP mode is stateless: a fresh SDK server is built per request, so tool
  registration must stay cheap and side-effect free.
- The memory module owns the `memory.Service` and closes it on shutdown.
- Logging uses `log/slog` with JSON output on stderr. Never log to stdout:
  stdout carries JSON-RPC in stdio mode.
- Default HTTP address: `127.0.0.1:8088`; override with `--addr` or
  `LLM_MEMORY_MCP_ADDR`. `/healthz` returns `ok`.
- Tools are named `memory_*` for records, `graph_*` for graph queries, and
  `memory_*` for maintenance. See `internal/mcp/memories/`.
- `memory_decay` requires `dry_run`; `dry_run=true` is the safe preview.
- Tool results are structured JSON. The SDK also emits a JSON text fallback.
- Tests connect an in-memory MCP client to the tool module
  (`internal/mcp/memories/module_test.go`) and to the HTTP handler
  (`internal/mcpserver/server_test.go`).

## Configuration

- `~/.config/llm-memory/config.json`, overridden by `--config` or
  `LLM_MEMORY_CONFIG`.
- The database defaults to `~/.local/share/llm-memory/memory.db`; override with
  `--db` or `LLM_MEMORY_DB`.
- All environment overrides use the `LLM_MEMORY_` prefix, e.g.
  `LLM_MEMORY_EMBEDDER`, `LLM_MEMORY_EMBED_MODEL`, `LLM_MEMORY_MCP_ADDR`.
- `auto` probes Ollama once and falls back to the hash embedder.
- Changing the embedder changes the vector space; run `llmmem reindex`.

## Storage and concurrency

- SQLite is the source of truth; the database file is a complete backup.
- The database uses one connection (`SetMaxOpenConns(1)`), so operations
  serialize. The vector index has its own mutex, and `memory.Service` holds an
  RWMutex so `reindex` and `restore` can swap the index without racing
  concurrent searches.
- The MCP server keeps the database and index open for its lifetime. The CLI
  and the server must not share one database at the same time: the server does
  not see rows written by another process.
- Links are stored in both directions. Deleting a memory cascades its links.
- Archived memories leave the vector index but stay in SQLite.

## Tests and lint

- Tests use the standard library only.
- `make test` runs the full suite; there are no build tags or external
  services required.
- The MCP tests use the SDK's in-memory and streamable HTTP transports.
- There is no CI configuration in this repository yet.

## Before committing

- Run `make fmt`, `make test`, and `make vet`.
- Do not commit `bin/`, `*.db`, `*.db-wal`, `*.db-shm`, `CONTEXT.md`, or user
  configuration.
- Keep packages under `internal/`; no Go files belong in the repository root.
