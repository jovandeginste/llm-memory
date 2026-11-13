package memories

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerGraphTools adds the tools that work on the memory graph.
func registerGraphTools(server *sdk.Server, m *Module) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_link",
		Description: "Create a bidirectional link between two memories.",
	}, m.link)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "memory_unlink",
		Description: "Remove both directions of a link between two memories.",
	}, m.unlink)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "graph_traverse",
		Description: "Walk the memory graph from one memory for N hops. Use when a search hit is relevant but incomplete; its neighbours often hold the missing detail.",
	}, m.traverse)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "graph_path",
		Description: "Find the shortest path between two memories in the link graph.",
	}, m.path)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "graph_clusters",
		Description: "List connected components of the memory graph with their categories and top tags.",
	}, m.clusters)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "graph_orphans",
		Description: "List memories without links. These may need better tags or a manual link.",
	}, m.orphans)
}

func (m *Module) link(_ context.Context, _ *sdk.CallToolRequest, p linkParams) (*sdk.CallToolResult, LinkOutput, error) {
	similarity := p.Similarity
	if similarity <= 0 {
		similarity = 1
	}

	if err := m.svc.Link(p.Src, p.Dst, similarity); err != nil {
		return nil, LinkOutput{}, err
	}

	return nil, LinkOutput{Src: p.Src, Dst: p.Dst, Similarity: similarity}, nil
}

func (m *Module) unlink(_ context.Context, _ *sdk.CallToolRequest, p unlinkParams) (*sdk.CallToolResult, UnlinkOutput, error) {
	if err := m.svc.Unlink(p.Src, p.Dst); err != nil {
		return nil, UnlinkOutput{}, err
	}

	return nil, UnlinkOutput{Src: p.Src, Dst: p.Dst, Unlinked: true}, nil
}

func (m *Module) traverse(_ context.Context, _ *sdk.CallToolRequest, p traverseParams) (*sdk.CallToolResult, TraverseOutput, error) {
	nodes, err := m.svc.Traverse(p.ID, p.Hops)
	if err != nil {
		return nil, TraverseOutput{}, err
	}

	out := TraverseOutput{Nodes: make([]NodeOutput, 0, len(nodes)), Count: len(nodes)}
	for _, n := range nodes {
		node := NodeOutput{ID: n.ID, Hop: n.Hop, Score: n.Score, Via: n.Via}
		if mem, err := m.svc.Store().GetMemory(n.ID); err == nil {
			node.Category = mem.Category
			node.Content = mem.Content
		}

		out.Nodes = append(out.Nodes, node)
	}

	return nil, out, nil
}

func (m *Module) path(_ context.Context, _ *sdk.CallToolRequest, p pathParams) (*sdk.CallToolResult, PathOutput, error) {
	ids, err := m.svc.Path(p.From, p.To)
	if err != nil {
		return nil, PathOutput{}, err
	}

	out := PathOutput{
		From: p.From,
		To:   p.To,
		Hops: len(ids) - 1,
		Path: make([]PathNodeOutput, 0, len(ids)),
	}
	for _, id := range ids {
		node := PathNodeOutput{ID: id}
		if mem, err := m.svc.Store().GetMemory(id); err == nil {
			node.Category = mem.Category
			node.Content = mem.Content
		}

		out.Path = append(out.Path, node)
	}

	return nil, out, nil
}

func (m *Module) clusters(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, ClustersOutput, error) {
	clusters, err := m.svc.Clusters()
	if err != nil {
		return nil, ClustersOutput{}, err
	}

	out := ClustersOutput{Clusters: make([]ClusterOutput, 0, len(clusters)), Count: len(clusters)}
	for _, c := range clusters {
		out.Clusters = append(out.Clusters, ClusterOutput{
			Members:    c.Members,
			Size:       c.Size,
			Categories: c.Categories,
			TopTags:    c.TopTags,
		})
	}

	return nil, out, nil
}

func (m *Module) orphans(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, OrphansOutput, error) {
	mems, err := m.svc.Orphans()
	if err != nil {
		return nil, OrphansOutput{}, err
	}

	return nil, OrphansOutput{Memories: memoryOutputs(mems), Count: len(mems)}, nil
}
