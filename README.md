# llm-memory

Persistent, self-linking memory for LLM agents, in one Go binary with a CLI and
an MCP server.

`llmmem` stores memories in SQLite, embeds them, keeps a vector index, and links
each new memory to the ones it resembles. Agents get a durable knowledge graph
instead of waking up with amnesia every session.

The design follows Jeremiah Ojo's *7-layer memory architecture*: semantic
search, session ingestion, A-MEM style linking, a memory graph, structured
facts, context rebuild, and decay. This tool implements those layers without
paid services:

- **SQLite** (`modernc.org/sqlite`, pure Go) is the durable store. The database
  file is a complete backup.
- **[tamnd/vec](https://github.com/tamnd/vec)** is the vector index. `vec` is
  process-resident today, so the index is rebuilt from SQLite at startup.
- **Embeddings** come from a local Ollama model, any OpenAI-compatible endpoint,
  or a built-in offline hash embedder.

## Install

```sh
make build          # bin/llmmem
make install        # ~/go/bin/llmmem
make skill-install  # skill for every OpenCode project (optional)
```

Requires Go 1.26 or newer.

## Docker

```sh
docker build -t llm-memory .
docker run -d --name llm-memory \
  -p 127.0.0.1:8088:8088 \
  -v llm-memory-data:/data \
  llm-memory
```

The image runs `llmmem mcp` as uid 10001, keeps the database in `/data`, and
listens on `0.0.0.0:8088`. There is no authentication, so publish the port on
localhost only unless you put it behind a proxy. The CLI works in the same
image:

```sh
docker run --rm -v llm-memory-data:/data llm-memory search "sqlite"
```

Do not run the CLI and the server against the same database at the same time.
Pass configuration through `-e`, for example
`-e LLM_MEMORY_EMBEDDER=hash` or `-e LLM_MEMORY_OLLAMA_URL=http://ollama:11434`.

The `Build` GitHub Actions workflow tests the code, builds the image, and
pushes it to `ghcr.io/<owner>/<repo>` on pushes and tags. Pull requests only
build the image.

## Quick start

```sh
llmmem init

llmmem add "We prefer Go for CLI tools" \
  --category preference --tags go,cli --importance 0.9

llmmem add "We chose SQLite plus tamnd/vec for the memory store" \
  --category decision --tags memory,sqlite

llmmem search "which storage did we choose" --k 5
llmmem context --days 7
```

`init` creates the database and a config file, and detects the embedder:

```text
wrote config: ~/.config/llm-memory/config.json
database: ~/.local/share/llm-memory/memory.db
embedder: ollama:nomic-embed-text (768 dims)
```

If no Ollama model is reachable it falls back to the offline hash embedder.

## MCP server

The same binary serves the store over the Model Context Protocol. A local MCP
client should launch it over stdio:

```sh
llmmem mcp --stdio
```

A single client can also use the streamable HTTP transport. It listens on
`127.0.0.1:8088` by default:

```sh
llmmem mcp                         # http://127.0.0.1:8088/mcp
llmmem mcp --addr 0.0.0.0:8088     # expose it; there is no authentication
```

Set `LLM_MEMORY_MCP_ADDR` to change the default HTTP address. `--config` and
`--db` work as they do for every other command. Logs go to stderr, so stdout
stays clean for the stdio protocol.

OpenCode, local server:

```json
{
  "mcp": {
    "llm-memory": {
      "type": "local",
      "command": ["llmmem", "mcp", "--stdio"]
    }
  }
}
```

OpenCode, remote server:

```json
{
  "mcp": {
    "llm-memory": {
      "type": "remote",
      "url": "http://127.0.0.1:8088/mcp"
    }
  }
}
```

The server opens the database and rebuilds the vector index once at startup.
Do not use the CLI on the same database while the server is running: the
server does not see rows written by another process until it restarts.

### Tools

| Tool | Purpose |
| --- | --- |
| `memory_add` | store one memory and auto-link it |
| `memory_search` | hybrid vector and keyword search, optionally over the graph |
| `memory_get` | show one memory with its links |
| `memory_list` | list recent memories |
| `memory_update` | change content, category, importance, tags, or source |
| `memory_delete` | archive a memory, or delete it permanently with `hard` |
| `memory_restore` | bring an archived memory back |
| `memory_reinforce` | boost a memory that proved useful |
| `memory_ingest` | split raw text into atomic memories |
| `memory_context` | markdown summary of recent memories |
| `memory_link` / `memory_unlink` | create or remove a bidirectional link |
| `graph_traverse` | walk the link graph from a memory |
| `graph_path` | shortest path between two memories |
| `graph_clusters` | connected components and their themes |
| `graph_orphans` | memories with no links |
| `memory_decay` | archive weak memories; `dry_run` is required |
| `memory_stats` | database health summary |
| `memory_reindex` | re-embed everything after changing models |

Tool results are structured JSON. `memory_search` adds `score`, `similarity`,
and `via` to each memory; `via` is `vector`, `keyword`, or `graph`.

## How it works

```text
        add ──► embed ──► SQLite (memories, links, vectors)
                  │                 ▲
                  └──► vec index ───┘   rebuilt at every start

     search ──► vec ANN + FTS5 ──► reciprocal rank fusion ──► graph expansion
```

### Layers

| Layer | Implementation |
| --- | --- |
| Semantic search | `vec` cosine ANN over the `embedding` column |
| Keyword search | SQLite FTS5 with BM25, fused with the vector hits |
| Self-linking | new memories link to the top 5 neighbours above 0.5 similarity, both directions |
| Memory graph | links are stored in both directions and expanded during search |
| Structured facts | `ingest` uses an optional LLM extractor, or paragraph chunking |
| Context rebuild | `context` renders a markdown summary of recent memories |
| Decay | strength formula archives weak memories and can purge dead ones |

### Memory categories

`fact`, `finding`, `decision`, `event`, `task`, `insight`, `preference`.

Decisions and preferences are stickier in the strength formula.

### Strength and decay

Every memory has a strength in `[0, 1]`:

```text
age      = 0.5 ^ (days_since_created / 30)
recency  = 0.5 ^ (days_since_last_access / 7)
links    = min(0.30, 0.05 * link_count)
weight   = 1.3 for decisions, 1.4 for preferences, 1.0 otherwise

strength = clamp01((0.55*importance + 0.30*age + 0.15*recency) * weight + links)
```

`llmmem decay` archives memories below `0.15` and, with `--purge`, deletes those
below `0.05`. Archived memories leave the vector index but stay in SQLite. Run
`decay --dry-run` first.

`llmmem reinforce <id>` raises importance and records an access, the equivalent
of spaced repetition.

## Commands

### Memory

| Command | Purpose |
| --- | --- |
| `init` | create the database and config |
| `add <text\|->` | store a memory, or a JSONL stream from stdin, and auto-link it |
| `ingest <file\|->` | split raw text into atomic memories |
| `search <query>` | hybrid vector + keyword search |
| `get <id>` | show a memory with its links |
| `list` | list recent memories |
| `update <id>` | change content, category, importance, tags, or source |
| `delete <id>` | archive, or delete permanently with `--hard` |
| `restore <id>` | bring an archived memory back into the index |
| `reinforce <id>` | boost a memory that proved useful |

### Streaming JSONL

A tool can emit memories as JSONL and pipe the whole stream into one `llmmem`
process, so the database and vector index open only once:

```sh
some-tool | llmmem add -

# each line emitted by some-tool looks like:
{"content":"We chose SQLite for the memory store","category":"decision","importance":0.9}
{"content":"The CLI is written in Go","tags":["go","cli"],"source":"README"}
```

Each non-empty line is one JSON object. `content` (or `text`) is required;
`category`, `importance`, `tags`, `source`, and `session` override the
corresponding command-line flags. Omitted fields keep the flag defaults. Blank
lines are ignored.

JSONL is detected when the first non-empty line starts with `{`. Pass `--jsonl`
to require it. Plain text on stdin is still stored as one memory. With `--json`
the result is an array of the same objects that a single `add --json` returns.

### Graph

| Command | Purpose |
| --- | --- |
| `link <src> <dst>` | create a bidirectional link |
| `unlink <src> <dst>` | remove both directions |
| `links <id>` | list a memory's links |
| `graph traverse <id> --hops 2` | walk the link graph |
| `graph path <a> <b>` | shortest path between memories |
| `graph clusters` | connected components with themes |
| `graph orphans` | memories with no links |

### Maintenance

| Command | Purpose |
| --- | --- |
| `context` | rebuild the session context summary |
| `decay` | archive weak memories |
| `stats` | database health summary |
| `reindex` | re-embed everything after changing models |
| `config` | print the resolved configuration |
| `embed <text>` | print an embedding (debug) |

### Server

| Command | Purpose |
| --- | --- |
| `mcp` | serve the MCP tools over HTTP or stdio |

`mcp` defaults to streamable HTTP on `127.0.0.1:8088`. Use `--stdio` when an MCP
client launches the command as a child process, and `--addr` to change the HTTP
listen address.

### Flags

The persistent flags `--config`, `--db`, and `--json` are accepted anywhere:

```sh
llmmem --db /tmp/test.db search "sqlite" --k 3
llmmem search "sqlite" --json
llmmem --version
llmmem --help
```

Most commands accept `--json` for machine consumption.

Useful command flags:

```text
add       --category --importance --tags --source --session
          --no-link --threshold --topk --jsonl
search    --k --category --min-similarity --graph-depth --no-touch
list      --category --limit --offset --include-archived
context   --days --limit --write FILE
decay     --dry-run --purge --archive-below --purge-below
mcp       --addr --stdio
```

## Configuration

`~/.config/llm-memory/config.json` (override with `--config` or
`LLM_MEMORY_CONFIG`):

```json
{
  "db": "~/.local/share/llm-memory/memory.db",
  "embedder": "auto",
  "ollama_url": "http://localhost:11434",
  "embed_model": "nomic-embed-text",
  "embed_url": "https://api.openai.com/v1",
  "embed_api_key": "",
  "link_threshold": 0.5,
  "link_topk": 5,
  "extract_url": "",
  "extract_model": "",
  "extract_api_key": ""
}
```

Environment overrides:

| Variable | Meaning |
| --- | --- |
| `LLM_MEMORY_DB` | database file |
| `LLM_MEMORY_EMBEDDER` | `auto`, `hash`, `ollama`, or `openai` |
| `LLM_MEMORY_OLLAMA_URL` | Ollama base URL |
| `LLM_MEMORY_EMBED_MODEL` | Ollama embedding model |
| `LLM_MEMORY_EMBED_URL` | OpenAI-compatible base URL |
| `LLM_MEMORY_EMBED_API_KEY` | API key for the embeddings endpoint |
| `LLM_MEMORY_LINK_THRESHOLD` | auto-link similarity threshold |
| `LLM_MEMORY_LINK_TOPK` | maximum auto-links per memory |
| `LLM_MEMORY_EXTRACT_URL` | OpenAI-compatible chat URL for `ingest --extract` |
| `LLM_MEMORY_EXTRACT_MODEL` | chat model for fact extraction |
| `LLM_MEMORY_EXTRACT_API_KEY` | API key for the chat endpoint |
| `LLM_MEMORY_MCP_ADDR` | default HTTP listen address for `llmmem mcp` |

### Embedders

- **`auto`** (default) uses Ollama when `GET /api/tags` lists the configured
  model, otherwise the hash embedder. The choice and dimension are recorded in
  the database.
- **`ollama`** with `nomic-embed-text` (768 dims) is a good local default.
- **`openai`** works with OpenAI, LM Studio, vLLM, or any compatible `/embeddings`.
- **`hash`** is deterministic, dependency-free lexical hashing. It always works
  offline but only approximates semantics, so auto-linking needs near-duplicate
  wording.

Changing the embedder changes the vector space. `llmmem` refuses to mix
dimensions and tells you to run `llmmem reindex`, which re-embeds every live
memory.

### Fact extraction

`ingest` can split raw text with any OpenAI-compatible chat model:

```sh
export LLM_MEMORY_EXTRACT_URL=http://localhost:11434/v1
export LLM_MEMORY_EXTRACT_MODEL=qwen3.5:9b
llmmem ingest session.md --extract
```

Without an extractor it chunks the text on blank lines and stores each chunk.

## OpenCode skill

`.opencode/skills/llm-memory/SKILL.md` teaches an agent when and how to use the
CLI: recall at session start, search before answering, store durable facts,
reinforce useful memories, and run maintenance safely.

Project-local skill (this repo):

```text
.opencode/skills/llm-memory/SKILL.md
```

Install it for every project:

```sh
make skill-install
```

## Design notes

- **SQLite is the source of truth.** Vectors are stored as little-endian
  `float32` blobs next to the text, so one file holds everything.
- **The vector index is ephemeral.** `tamnd/vec` is process-resident in its
  current build, so `llmmem` reloads non-archived vectors at startup. For a
  personal memory store this is milliseconds; the trade-off is no on-disk ANN
  index yet.
- **Links are stored twice**, once per direction, which keeps neighbour queries
  a single indexed lookup.
- **Search fuses** vector ranks and BM25 ranks with reciprocal rank fusion
  (`1/(60+rank)`), then optionally expands one or more hops over the link graph.
- **Access is reinforcement.** Searches bump `access_count` and `last_access`,
  which feeds the recency term in the strength formula.

## Limitations

- The hash embedder is lexical, not semantic. Use Ollama or an API for real
  semantic recall.
- Archived memories are not re-embedded by `reindex`; restore before switching
  models if you need them.
- The MCP server keeps the database and vector index open for its lifetime. The
  CLI and the server should not share one database at the same time: the
  server does not see rows written by another process.
- Secrets do not belong in memory. The store is plain SQLite.

## Development

```sh
make test
make vet
make build
```
