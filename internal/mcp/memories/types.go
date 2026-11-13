package memories

import (
	"time"

	"llm-memory/internal/memory"
	"llm-memory/internal/store"
)

// Parameter types. Fields without "omitempty" are required in the generated
// tool schema.

type addParams struct {
	Content    string   `json:"content" jsonschema:"The memory text. Use one atomic, self-contained statement."`
	Category   string   `json:"category,omitempty" jsonschema:"Memory category: fact, finding, decision, event, task, insight, or preference. Defaults to fact."`
	Importance float64  `json:"importance,omitempty" jsonschema:"Importance from 0 to 1. Use 0.8-1.0 for decisions and preferences, 0.5 for ordinary facts, 0.2-0.3 for trivia."`
	Tags       []string `json:"tags,omitempty" jsonschema:"Short lowercase keywords."`
	Source     string   `json:"source,omitempty" jsonschema:"File, URL, ticket, or session reference."`
	Session    string   `json:"session,omitempty" jsonschema:"Session identifier."`
	Link       *bool    `json:"link,omitempty" jsonschema:"Create automatic links to similar memories. Defaults to true."`
	Threshold  float64  `json:"threshold,omitempty" jsonschema:"Minimum cosine similarity for automatic links. Defaults to the configured value."`
	TopK       int      `json:"topk,omitempty" jsonschema:"Maximum number of automatic links. Defaults to the configured value."`
}

type searchParams struct {
	Query         string  `json:"query" jsonschema:"Words or a question to search for."`
	K             int     `json:"k,omitempty" jsonschema:"Maximum number of results. Defaults to 10."`
	Category      string  `json:"category,omitempty" jsonschema:"Restrict results to one category."`
	MinSimilarity float64 `json:"min_similarity,omitempty" jsonschema:"Drop vector hits below this cosine similarity."`
	GraphDepth    int     `json:"graph_depth,omitempty" jsonschema:"Expand results over the link graph for this many hops."`
	Touch         *bool   `json:"touch,omitempty" jsonschema:"Count results as accessed and reinforce them. Defaults to true."`
}

type getParams struct {
	ID int64 `json:"id" jsonschema:"Memory id."`
}

type listParams struct {
	Category        string `json:"category,omitempty" jsonschema:"Restrict the list to one category."`
	Limit           int    `json:"limit,omitempty" jsonschema:"Maximum number of memories. Defaults to 20."`
	Offset          int    `json:"offset,omitempty" jsonschema:"Skip the first N memories."`
	IncludeArchived bool   `json:"include_archived,omitempty" jsonschema:"Include archived memories."`
}

type updateParams struct {
	ID         int64     `json:"id" jsonschema:"Memory id."`
	Content    *string   `json:"content,omitempty" jsonschema:"New content. Re-embeds and re-indexes the memory."`
	Category   *string   `json:"category,omitempty" jsonschema:"New category."`
	Importance *float64  `json:"importance,omitempty" jsonschema:"New importance from 0 to 1."`
	Tags       *[]string `json:"tags,omitempty" jsonschema:"New tags; replaces the existing tags."`
	Source     *string   `json:"source,omitempty" jsonschema:"New source reference."`
}

type deleteParams struct {
	ID   int64 `json:"id" jsonschema:"Memory id."`
	Hard bool  `json:"hard,omitempty" jsonschema:"Delete permanently instead of archiving. This cannot be undone."`
}

type restoreParams struct {
	ID int64 `json:"id" jsonschema:"Memory id."`
}

type reinforceParams struct {
	ID     int64   `json:"id" jsonschema:"Memory id."`
	Amount float64 `json:"amount,omitempty" jsonschema:"Importance increase. Defaults to 0.1."`
}

type ingestParams struct {
	Text       string   `json:"text" jsonschema:"Raw text to split into atomic memories."`
	Category   string   `json:"category,omitempty" jsonschema:"Default category for extracted memories."`
	Source     string   `json:"source,omitempty" jsonschema:"File, URL, ticket, or session reference."`
	Session    string   `json:"session,omitempty" jsonschema:"Session identifier."`
	Importance float64  `json:"importance,omitempty" jsonschema:"Default importance from 0 to 1."`
	Tags       []string `json:"tags,omitempty" jsonschema:"Default tags added to every extracted memory."`
	Extract    *bool    `json:"extract,omitempty" jsonschema:"Use the configured LLM extractor when available. Defaults to true."`
}

type linkParams struct {
	Src        int64   `json:"src" jsonschema:"Source memory id."`
	Dst        int64   `json:"dst" jsonschema:"Destination memory id."`
	Similarity float64 `json:"similarity,omitempty" jsonschema:"Link strength from 0 to 1. Defaults to 1."`
}

type unlinkParams struct {
	Src int64 `json:"src" jsonschema:"Source memory id."`
	Dst int64 `json:"dst" jsonschema:"Destination memory id."`
}

type traverseParams struct {
	ID   int64 `json:"id" jsonschema:"Starting memory id."`
	Hops int   `json:"hops,omitempty" jsonschema:"Number of hops. Defaults to 2."`
}

type pathParams struct {
	From int64 `json:"from" jsonschema:"Start memory id."`
	To   int64 `json:"to" jsonschema:"End memory id."`
}

type contextParams struct {
	Days  int `json:"days,omitempty" jsonschema:"Include memories updated in the last N days. Defaults to 7."`
	Limit int `json:"limit,omitempty" jsonschema:"Maximum memories in the summary. Defaults to 40."`
}

type decayParams struct {
	DryRun       *bool   `json:"dry_run" jsonschema:"Required. When true, report what would happen without changing anything."`
	Purge        bool    `json:"purge,omitempty" jsonschema:"Also hard-delete memories below the purge threshold."`
	ArchiveBelow float64 `json:"archive_below,omitempty" jsonschema:"Archive memories with strength below this value. Defaults to 0.15."`
	PurgeBelow   float64 `json:"purge_below,omitempty" jsonschema:"Purge memories with strength below this value. Defaults to 0.05."`
}

type statsParams struct{}

type reindexParams struct{}

// Output types. They are returned as MCP structured content.

// MemoryOutput is the stable JSON shape of one memory.
type MemoryOutput struct {
	ID          int64    `json:"id"`
	Category    string   `json:"category"`
	Content     string   `json:"content"`
	Importance  float64  `json:"importance"`
	Tags        []string `json:"tags,omitempty"`
	Source      string   `json:"source,omitempty"`
	Session     string   `json:"session,omitempty"`
	Strength    float64  `json:"strength"`
	Links       int      `json:"links"`
	AccessCount int      `json:"access_count"`
	Archived    bool     `json:"archived,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	LastAccess  string   `json:"last_access"`
}

// LinkedOutput is a link with the memory at the other end.
type LinkedOutput struct {
	ID         int64   `json:"id"`
	Similarity float64 `json:"similarity"`
	Category   string  `json:"category,omitempty"`
	Content    string  `json:"content,omitempty"`
}

// AddOutput is a stored memory plus the links created for it.
type AddOutput struct {
	MemoryOutput
	Linked []LinkedOutput `json:"linked"`
}

// GetOutput is a memory with its links.
type GetOutput struct {
	MemoryOutput
	Linked []LinkedOutput `json:"linked"`
}

// SearchResultOutput is one hybrid search hit.
type SearchResultOutput struct {
	MemoryOutput
	Score      float64 `json:"score"`
	Similarity float64 `json:"similarity"`
	Via        string  `json:"via"`
}

// SearchOutput is the hybrid search response.
type SearchOutput struct {
	Results []SearchResultOutput `json:"results"`
	Count   int                  `json:"count"`
}

// ListOutput is a page of memories.
type ListOutput struct {
	Memories []MemoryOutput `json:"memories"`
	Count    int            `json:"count"`
}

// IngestOutput is the result of splitting raw text.
type IngestOutput struct {
	Memories []MemoryOutput `json:"memories"`
	Count    int            `json:"count"`
}

// DeleteOutput reports what happened to a memory.
type DeleteOutput struct {
	ID     int64  `json:"id"`
	Action string `json:"action"`
}

// LinkOutput reports a new link.
type LinkOutput struct {
	Src        int64   `json:"src"`
	Dst        int64   `json:"dst"`
	Similarity float64 `json:"similarity"`
}

// UnlinkOutput reports a removed link.
type UnlinkOutput struct {
	Src      int64 `json:"src"`
	Dst      int64 `json:"dst"`
	Unlinked bool  `json:"unlinked"`
}

// NodeOutput is one memory reached by a graph traversal.
type NodeOutput struct {
	ID       int64   `json:"id"`
	Hop      int     `json:"hop"`
	Score    float64 `json:"score"`
	Via      int64   `json:"via,omitempty"`
	Category string  `json:"category,omitempty"`
	Content  string  `json:"content,omitempty"`
}

// TraverseOutput is the result of a graph traversal.
type TraverseOutput struct {
	Nodes []NodeOutput `json:"nodes"`
	Count int          `json:"count"`
}

// PathNodeOutput is one memory on a shortest path.
type PathNodeOutput struct {
	ID       int64  `json:"id"`
	Category string `json:"category,omitempty"`
	Content  string `json:"content,omitempty"`
}

// PathOutput is the shortest path between two memories.
type PathOutput struct {
	From int64            `json:"from"`
	To   int64            `json:"to"`
	Hops int              `json:"hops"`
	Path []PathNodeOutput `json:"path"`
}

// ClusterOutput is one connected component of the memory graph.
type ClusterOutput struct {
	Members    []int64        `json:"members"`
	Size       int            `json:"size"`
	Categories map[string]int `json:"categories"`
	TopTags    []string       `json:"top_tags,omitempty"`
}

// ClustersOutput lists the connected components.
type ClustersOutput struct {
	Clusters []ClusterOutput `json:"clusters"`
	Count    int             `json:"count"`
}

// OrphansOutput lists memories without links.
type OrphansOutput struct {
	Memories []MemoryOutput `json:"memories"`
	Count    int            `json:"count"`
}

// ContextOutput is the markdown context summary.
type ContextOutput struct {
	Markdown string `json:"markdown"`
}

// DecayActionOutput is one memory touched by a decay run.
type DecayActionOutput struct {
	ID       int64   `json:"id"`
	Action   string  `json:"action"`
	Strength float64 `json:"strength"`
	Content  string  `json:"content,omitempty"`
}

// DecayOutput summarises a decay run.
type DecayOutput struct {
	Scanned  int                 `json:"scanned"`
	Archived int                 `json:"archived"`
	Purged   int                 `json:"purged"`
	Actions  []DecayActionOutput `json:"actions"`
}

// ReindexOutput reports a re-embedding run.
type ReindexOutput struct {
	Memories int     `json:"memories"`
	Embedder string  `json:"embedder"`
	Dims     int     `json:"dims"`
	Seconds  float64 `json:"seconds"`
}

// memoryOutput converts a stored memory to its tool shape.
func memoryOutput(m *store.Memory) MemoryOutput {
	return MemoryOutput{
		ID: m.ID, Category: m.Category, Content: m.Content,
		Importance: m.Importance, Tags: m.Tags, Source: m.Source, Session: m.Session,
		Strength: m.Strength, Links: m.Degree, AccessCount: m.Access, Archived: m.Archived,
		CreatedAt:  m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:  m.UpdatedAt.UTC().Format(time.RFC3339),
		LastAccess: m.LastAccess.UTC().Format(time.RFC3339),
	}
}

// memoryOutputs converts a slice of memories.
func memoryOutputs(mems []*store.Memory) []MemoryOutput {
	out := make([]MemoryOutput, 0, len(mems))
	for _, m := range mems {
		out = append(out, memoryOutput(m))
	}

	return out
}

// linkOutputs converts links without the memory at the other end.
func linkOutputs(links []store.Link) []LinkedOutput {
	out := make([]LinkedOutput, 0, len(links))
	for _, l := range links {
		out = append(out, LinkedOutput{ID: l.Dst, Similarity: l.Similarity})
	}

	return out
}

// detailLinks converts a memory detail's links.
func detailLinks(d *memory.Detail) []LinkedOutput {
	out := make([]LinkedOutput, 0, len(d.Links))
	for _, l := range d.Links {
		out = append(out, LinkedOutput{
			ID: l.Link.Dst, Similarity: l.Link.Similarity,
			Category: l.Other.Category, Content: l.Other.Content,
		})
	}

	return out
}
