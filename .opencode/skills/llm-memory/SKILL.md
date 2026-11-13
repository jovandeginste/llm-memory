---
name: LLM Memory
description: Persistent, self-linking memory for LLM agents through the llmmem CLI and MCP server. Use to recall decisions, facts, preferences, and findings from earlier sessions, to store durable knowledge while working, and to rebuild session context. Also use when the user asks what was decided before, refers to past work, or wants something remembered.
---

# LLM Memory

`llmmem` is a local memory store: SQLite for durability, a vector index for
semantic search, and a self-linking graph between related memories.

## Before you start

```sh
command -v llmmem || ls bin/llmmem
```

If it is missing, build it in the `llm-memory` repository:

```sh
make install        # installs llmmem in ~/go/bin
```

If `llmmem` has never been used, initialize it once:

```sh
llmmem init
```

`--json`, `--config`, and `--db` are persistent flags and may appear anywhere.
Use `--json` on every command whose output you parse. The JSON goes to stdout;
errors go to stderr and exit non-zero. Run `llmmem --help` or
`llmmem <command> --help` for the full flag list.

## MCP tools

When the `llm-memory` MCP server is connected, use its tools instead of shelling
out. They expose the same engine with typed arguments:

| Tool | CLI equivalent |
| --- | --- |
| `memory_context` | `llmmem context` |
| `memory_search` | `llmmem search` |
| `memory_add` | `llmmem add` |
| `memory_get` | `llmmem get` |
| `memory_list` | `llmmem list` |
| `memory_update` | `llmmem update` |
| `memory_delete` | `llmmem delete` |
| `memory_restore` | `llmmem restore` |
| `memory_reinforce` | `llmmem reinforce` |
| `memory_ingest` | `llmmem ingest` |
| `memory_link` / `memory_unlink` | `llmmem link` / `llmmem unlink` |
| `graph_traverse` / `graph_path` / `graph_clusters` / `graph_orphans` | `llmmem graph ...` |
| `memory_decay` / `memory_stats` / `memory_reindex` | `llmmem decay` / `llmmem stats` / `llmmem reindex` |

The server keeps the database and vector index open for its lifetime. Do not
run CLI commands against the same database while the server is active; the
server does not see writes from another process. The rest of this skill uses
the CLI as the fallback when no MCP server is connected.

## Session workflow

### 1. Recall at the start

```sh
llmmem context --days 7
```

Inject the markdown summary into your working context. It is grouped by
category and ordered by strength, so recent decisions and preferences come
first. Widen to `--days 30` for long-running projects.

### 2. Search before answering

When a question may depend on earlier work, search instead of guessing:

```sh
llmmem search "which database did we choose" --k 5 --graph-depth 1 --json
```

- `--graph-depth 1` also returns linked memories, which surfaces related
  findings that keyword search misses.
- `--category decision` narrows by kind.
- `--min-similarity 0.45` drops weak vector hits.

Read `content`, `category`, `strength`, `similarity`, and `via` from the result.
`via` is `vector`, `keyword`, or `graph`; a graph hit is associative, so verify
it before relying on it.

### 3. Store durable knowledge

Store a memory when the user states a preference or decision, when you discover
a non-obvious fact, or when a task's status changes. Do not store small talk,
transient output, or anything already in the repo.

```sh
llmmem add "The deployment uses SQLite with WAL mode" \
  --category fact --tags sqlite,deployment --importance 0.6

llmmem add "We decided against a server mode; the CLI opens the DB per call" \
  --category decision --tags architecture --importance 0.9
```

- Write one atomic statement per memory, understandable on its own.
- Include exact names, versions, paths, and dates.
- Search first. If a memory already covers the topic, update it instead:
  `llmmem update 12 --content "..." --importance 0.8`
- Use `--source` for a file, URL, or ticket reference.
- Use `--no-link` only when linking would be misleading.

For a long transcript or note file, ingest it in one go:

```sh
llmmem ingest session.md --extract
```

`--extract` uses the configured chat model to split text into atomic facts with
categories, importance, and tags. Without an extractor it chunks on blank lines.

When another tool can emit memory records, stream them as JSONL into one
process instead of starting `llmmem` per memory:

```sh
some-tool | llmmem add - --jsonl
```

Each line is `{"content":"...","category":"...","importance":0.8,"tags":["..."]}`.
`text` works as an alias for `content`; omitted fields use the command-line
flags.

### 4. Reinforce what proved useful

```sh
llmmem reinforce 12 --amount 0.1
```

Call this when a memory directly shaped your answer, or when the user confirms
it is important. Reinforcement raises importance and access recency, so useful
memories survive decay.

### 5. Flush before context is lost

Before a compaction, a long hand-off, or the end of a session, write everything
that matters while you still have it. Do not wait for the user to ask.

```sh
llmmem add "Task: migrate the CI pipeline to Go 1.26; blocked on the runner image" \
  --category task --tags ci,migration --importance 0.8

llmmem ingest notes.md --extract
```

Store decisions with their rationale, open tasks with their status, discovered
facts with exact paths and versions, and user preferences verbatim. Vague
summaries are worthless later.

### 6. Maintenance

```sh
llmmem stats
llmmem decay --dry-run
llmmem graph orphans
```

`decay --dry-run` is always safe. Do not run `decay --purge` or `delete --hard`
without explicit user approval; both destroy data. `reindex` is only needed
after the embedding model changes.

## Categories

| Category | Use for |
| --- | --- |
| `fact` | stable, verifiable statements |
| `finding` | results of investigation, audit, or analysis |
| `decision` | choices with rationale |
| `event` | things that happened at a point in time |
| `task` | work to do or status of work |
| `insight` | non-obvious conclusions |
| `preference` | how the user or team wants things done |

Decisions and preferences are weighted higher in the strength formula. Use
importance `0.8–1.0` for decisions and preferences, `0.5` for ordinary facts,
and `0.2–0.3` for trivia.

## Graph commands

```sh
llmmem links 12                      # neighbours of a memory
llmmem graph traverse 12 --hops 2    # everything reachable within two hops
llmmem graph path 12 40              # conceptual chain between two memories
llmmem graph clusters                # connected components and their themes
llmmem graph orphans                 # memories with no links
```

Use `graph traverse` when a search hit looks relevant but incomplete: its
neighbours often contain the missing detail. Use `graph orphans` to find
knowledge that never got connected; those memories may need better tags or a
manual `llmmem link`.

## Rules

- Never store passwords, API keys, tokens, private keys, or personal data that
  does not belong in a plain SQLite file.
- Prefer updating an existing memory over adding a near-duplicate.
- Do not invent memories or backfill them from guesses. Only store what was
  actually stated or observed.
- Treat retrieved memories as evidence with a timestamp, not as current truth.
  Check `updated_at` and `strength` before relying on an old, weak memory.
- If `llmmem` reports an embedder mismatch, run `llmmem reindex`; do not delete
  the database.

## JSON shapes

Search result:

```json
[{"id": 12, "category": "decision", "content": "...",
  "importance": 0.9, "tags": ["architecture"], "strength": 0.94, "links": 3,
  "access_count": 2, "created_at": "...", "updated_at": "...",
  "last_access": "...", "score": 0.032, "similarity": 0.78, "via": "vector"}]
```

`add` returns the memory plus `linked`:

```json
{"id": 13, "...": "...", "linked": [{"id": 12, "similarity": 0.81}]}
```

`get` and `links` return the memory plus `linked` entries with `id`,
`similarity`, `content`, and `category`.

`stats` returns counts, per-category totals, the embedder, the vector dimension,
and the database path.

## Troubleshooting

- **No results**: search fewer, more specific words, add `--graph-depth 1`, or
  use `llmmem list` to browse.
- **Everything is linked to everything**: raise `--threshold` or lower
  `link_topk` in the config.
- **`database was built with embedder ... but ... is configured`**: the model
  changed. Run `llmmem reindex`.
- **Slow startup**: the `auto` embedder probes Ollama on every call. Set
  `LLM_MEMORY_EMBEDDER=hash` for fully offline use, or run Ollama locally.
- **Archived memory missing from search**: it was decayed. `llmmem get <id>`
  still works; `llmmem restore <id>` puts it back into the index. Ask the user
  before restoring or purging data.
