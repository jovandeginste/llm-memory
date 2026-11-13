package memories

import (
	"context"
	"errors"
	"time"

	"llm-memory/internal/memory"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMaintenanceTools adds the tools that inspect or maintain the store.
func registerMaintenanceTools(server *sdk.Server, m *Module) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_stats",
		Description: "Show database health: memory and link counts, categories, embedder, and vector dimensions.",
	}, m.stats)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_decay",
		Description: "Archive weak memories by strength. dry_run is required; set it to true to preview. Set purge=true to also delete dead memories.",
	}, m.decay)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_reindex",
		Description: "Re-embed every live memory and rebuild the vector index. Needed after changing embedding models.",
	}, m.reindex)
}

func (m *Module) stats(_ context.Context, _ *sdk.CallToolRequest, _ statsParams) (*sdk.CallToolResult, memory.Stats, error) {
	st, err := m.svc.Stats()
	if err != nil {
		return nil, memory.Stats{}, err
	}

	return nil, *st, nil
}

func (m *Module) decay(ctx context.Context, _ *sdk.CallToolRequest, p decayParams) (*sdk.CallToolResult, DecayOutput, error) {
	if p.DryRun == nil {
		return nil, DecayOutput{}, errors.New("dry_run is required; set it to true to preview without changing anything")
	}

	report, err := m.svc.Decay(ctx, memory.DecayOptions{
		DryRun:    *p.DryRun,
		Purge:     p.Purge,
		ArchiveAt: p.ArchiveBelow,
		PurgeAt:   p.PurgeBelow,
	})
	if err != nil {
		return nil, DecayOutput{}, err
	}

	out := DecayOutput{
		Scanned:  report.Scanned,
		Archived: report.Archived,
		Purged:   report.Purged,
		Actions:  make([]DecayActionOutput, 0, len(report.Actions)),
	}
	for _, action := range report.Actions {
		out.Actions = append(out.Actions, DecayActionOutput{
			ID:       action.ID,
			Action:   action.Action,
			Strength: action.Strength,
			Content:  action.Content,
		})
	}

	return nil, out, nil
}

func (m *Module) reindex(ctx context.Context, _ *sdk.CallToolRequest, _ reindexParams) (*sdk.CallToolResult, ReindexOutput, error) {
	start := time.Now()

	n, err := m.svc.Reindex(ctx)
	if err != nil {
		return nil, ReindexOutput{}, err
	}

	return nil, ReindexOutput{
		Memories: n,
		Embedder: m.svc.Embedder().Name(),
		Dims:     m.svc.Dims(),
		Seconds:  time.Since(start).Seconds(),
	}, nil
}
