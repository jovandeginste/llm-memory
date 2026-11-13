package memory

import (
	"time"

	"llm-memory/internal/store"
)

// Categories are the memory kinds the tool understands. They mirror the article:
// atomic facts extracted from a session, each with its own weight.
var Categories = []string{
	"fact", "finding", "decision", "event", "task", "insight", "preference",
}

// ValidCategory reports whether c is a known category.
func ValidCategory(c string) bool {
	for _, k := range Categories {
		if c == k {
			return true
		}
	}
	return false
}

// LinkView is a link plus the memory at the other end.
type LinkView struct {
	Link  store.Link
	Other *store.Memory
	Score float64 // similarity, convenience for output
}

// Detail is a memory with its graph neighbourhood.
type Detail struct {
	Memory *store.Memory
	Links  []LinkView
}

// SearchResult is one hit from a hybrid search.
type SearchResult struct {
	Memory     *store.Memory
	Score      float64 // fused RRF score
	Similarity float64 // cosine similarity when the hit came from the vector side
	Via        string  // vector, keyword, or graph
}

// AddInput describes a new memory.
type AddInput struct {
	Content    string
	Category   string
	Importance float64
	Tags       []string
	Source     string
	Session    string

	// Link controls automatic linking; nil means the configured default (on).
	Link      *bool
	Threshold float64
	TopK      int
}

// AddResult is the outcome of storing one memory.
type AddResult struct {
	Memory *store.Memory
	Links  []store.Link
}

// SearchInput describes a hybrid search.
type SearchInput struct {
	Query         string
	K             int
	Category      string
	MinSimilarity float64
	GraphDepth    int
	Touch         bool
}

// DecayOptions controls a decay run.
type DecayOptions struct {
	DryRun    bool
	Purge     bool
	ArchiveAt float64
	PurgeAt   float64
	Now       time.Time
}

// DecayAction is one memory the decay run touched.
type DecayAction struct {
	ID       int64
	Action   string // archived, purged, kept
	Strength float64
	Content  string
}

// DecayReport summarises a decay run.
type DecayReport struct {
	Scanned  int
	Archived int
	Purged   int
	Actions  []DecayAction
}

// ContextOptions controls the context rebuild.
type ContextOptions struct {
	Days  int
	Limit int
}

// Node is one memory reached by a graph traversal.
type Node struct {
	ID    int64
	Hop   int
	Score float64
	Via   int64 // the node this one was reached from
}

// Cluster is a connected component of the memory graph.
type Cluster struct {
	Members    []int64
	Size       int
	Categories map[string]int
	TopTags    []string
}

// Stats is the database health summary.
type Stats struct {
	Memories    int64            `json:"memories"`
	Archived    int64            `json:"archived"`
	Links       int64            `json:"links"`
	AvgLinks    float64          `json:"avg_links_per_memory"`
	ByCategory  map[string]int64 `json:"by_category"`
	Embedder    string           `json:"embedder"`
	Dims        int              `json:"dims"`
	DB          string           `json:"db"`
	LastUpdated string           `json:"last_updated,omitempty"`
}
