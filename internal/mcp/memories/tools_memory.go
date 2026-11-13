package memories

import (
	"context"

	"llm-memory/internal/memory"
	"llm-memory/internal/store"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMemoryTools adds the tools that create and inspect memories.
func registerMemoryTools(server *sdk.Server, m *Module) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_add",
		Description: "Store one memory and link it to similar memories. Use for durable facts, decisions, preferences, findings, events, tasks, and insights. Write one atomic, self-contained statement.",
	}, m.add)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_search",
		Description: "Hybrid vector and keyword search over stored memories, optionally expanded over the link graph. Use before answering questions that may depend on earlier work.",
	}, m.search)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_get",
		Description: "Show one memory with its links and current strength.",
	}, m.get)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_list",
		Description: "List recent memories, newest update first.",
	}, m.list)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_update",
		Description: "Change the content, category, importance, tags, or source of a memory. Changing content re-embeds it.",
	}, m.update)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_delete",
		Description: "Archive a memory by default, or delete it permanently with hard=true. Prefer archiving.",
	}, m.delete)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_restore",
		Description: "Bring an archived memory back into the vector index.",
	}, m.restore)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_reinforce",
		Description: "Boost a memory that proved useful. Raises its importance and records an access.",
	}, m.reinforce)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_ingest",
		Description: "Split raw text into atomic memories. Uses the configured LLM extractor when available, otherwise paragraph chunking.",
	}, m.ingest)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_context",
		Description: "Build a markdown summary of recent memories grouped by category, for injection at the start of a session.",
	}, m.context)
}

func (m *Module) add(ctx context.Context, _ *sdk.CallToolRequest, p addParams) (*sdk.CallToolResult, AddOutput, error) {
	res, err := m.svc.Add(ctx, memory.AddInput{
		Content:    p.Content,
		Category:   p.Category,
		Importance: p.Importance,
		Tags:       p.Tags,
		Source:     p.Source,
		Session:    p.Session,
		Link:       p.Link,
		Threshold:  p.Threshold,
		TopK:       p.TopK,
	})
	if err != nil {
		return nil, AddOutput{}, err
	}

	return nil, AddOutput{
		MemoryOutput: memoryOutput(res.Memory),
		Linked:       linkOutputs(res.Links),
	}, nil
}

func (m *Module) search(ctx context.Context, _ *sdk.CallToolRequest, p searchParams) (*sdk.CallToolResult, SearchOutput, error) {
	touch := true
	if p.Touch != nil {
		touch = *p.Touch
	}

	results, err := m.svc.Search(ctx, memory.SearchInput{
		Query:         p.Query,
		K:             p.K,
		Category:      p.Category,
		MinSimilarity: p.MinSimilarity,
		GraphDepth:    p.GraphDepth,
		Touch:         touch,
	})
	if err != nil {
		return nil, SearchOutput{}, err
	}

	out := SearchOutput{Results: make([]SearchResultOutput, 0, len(results)), Count: len(results)}
	for _, r := range results {
		out.Results = append(out.Results, SearchResultOutput{
			MemoryOutput: memoryOutput(r.Memory),
			Score:        r.Score,
			Similarity:   r.Similarity,
			Via:          r.Via,
		})
	}

	return nil, out, nil
}

func (m *Module) get(_ context.Context, _ *sdk.CallToolRequest, p getParams) (*sdk.CallToolResult, GetOutput, error) {
	d, err := m.svc.Get(p.ID)
	if err != nil {
		return nil, GetOutput{}, err
	}

	return nil, GetOutput{
		MemoryOutput: memoryOutput(d.Memory),
		Linked:       detailLinks(d),
	}, nil
}

func (m *Module) list(_ context.Context, _ *sdk.CallToolRequest, p listParams) (*sdk.CallToolResult, ListOutput, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	mems, err := m.svc.List(store.Filter{
		Category:        p.Category,
		IncludeArchived: p.IncludeArchived,
		Limit:           limit,
		Offset:          p.Offset,
	})
	if err != nil {
		return nil, ListOutput{}, err
	}

	out := ListOutput{Memories: memoryOutputs(mems), Count: len(mems)}

	return nil, out, nil
}

func (m *Module) update(ctx context.Context, _ *sdk.CallToolRequest, p updateParams) (*sdk.CallToolResult, MemoryOutput, error) {
	if p.Content == nil && p.Category == nil && p.Importance == nil && p.Tags == nil && p.Source == nil {
		return nil, MemoryOutput{}, errNothingToUpdate
	}

	mem, err := m.svc.Update(ctx, p.ID, func(target *store.Memory) error {
		if p.Content != nil {
			target.Content = *p.Content
		}
		if p.Category != nil {
			target.Category = *p.Category
		}
		if p.Importance != nil {
			target.Importance = *p.Importance
		}
		if p.Tags != nil {
			target.Tags = *p.Tags
		}
		if p.Source != nil {
			target.Source = *p.Source
		}

		return nil
	})
	if err != nil {
		return nil, MemoryOutput{}, err
	}

	return nil, memoryOutput(mem), nil
}

func (m *Module) delete(ctx context.Context, _ *sdk.CallToolRequest, p deleteParams) (*sdk.CallToolResult, DeleteOutput, error) {
	if err := m.svc.Delete(ctx, p.ID, p.Hard); err != nil {
		return nil, DeleteOutput{}, err
	}

	action := "archived"
	if p.Hard {
		action = "deleted"
	}

	return nil, DeleteOutput{ID: p.ID, Action: action}, nil
}

func (m *Module) restore(ctx context.Context, _ *sdk.CallToolRequest, p restoreParams) (*sdk.CallToolResult, MemoryOutput, error) {
	mem, err := m.svc.Restore(ctx, p.ID)
	if err != nil {
		return nil, MemoryOutput{}, err
	}

	return nil, memoryOutput(mem), nil
}

func (m *Module) reinforce(_ context.Context, _ *sdk.CallToolRequest, p reinforceParams) (*sdk.CallToolResult, MemoryOutput, error) {
	mem, err := m.svc.Reinforce(p.ID, p.Amount)
	if err != nil {
		return nil, MemoryOutput{}, err
	}

	return nil, memoryOutput(mem), nil
}

func (m *Module) ingest(ctx context.Context, _ *sdk.CallToolRequest, p ingestParams) (*sdk.CallToolResult, IngestOutput, error) {
	extract := true
	if p.Extract != nil {
		extract = *p.Extract
	}

	results, err := m.svc.Ingest(ctx, p.Text, memory.IngestOptions{
		Category:   p.Category,
		Source:     p.Source,
		Session:    p.Session,
		Importance: p.Importance,
		Tags:       p.Tags,
		Extract:    extract,
	})
	if err != nil {
		return nil, IngestOutput{}, err
	}

	out := IngestOutput{Memories: make([]MemoryOutput, 0, len(results)), Count: len(results)}
	for _, res := range results {
		out.Memories = append(out.Memories, memoryOutput(res.Memory))
	}

	return nil, out, nil
}

func (m *Module) context(ctx context.Context, _ *sdk.CallToolRequest, p contextParams) (*sdk.CallToolResult, ContextOutput, error) {
	markdown, err := m.svc.Context(ctx, memory.ContextOptions{Days: p.Days, Limit: p.Limit})
	if err != nil {
		return nil, ContextOutput{}, err
	}

	return nil, ContextOutput{Markdown: markdown}, nil
}
