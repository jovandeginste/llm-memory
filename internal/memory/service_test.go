package memory

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"llm-memory/internal/config"
	"llm-memory/internal/embed"
	"llm-memory/internal/store"
)

// newService opens a service backed by a temporary database and the offline
// hash embedder.
func newService(t *testing.T) *Service {
	t.Helper()
	cfg := config.Defaults()
	cfg.DB = filepath.Join(t.TempDir(), "memory.db")
	cfg.Embedder = "hash"
	svc, err := OpenWith(context.Background(), cfg, embed.NewHash(256))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	return svc
}

// add stores a memory with sensible defaults.
func add(t *testing.T, svc *Service, content, category string) *AddResult {
	t.Helper()
	res, err := svc.Add(context.Background(), AddInput{Content: content, Category: category})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAddAndAutoLink(t *testing.T) {
	svc := newService(t)
	first := add(t, svc, "the Go CLI uses the standard flag package", "decision")
	second := add(t, svc, "the Go CLI uses the flag package and no dependencies", "insight")
	if len(second.Links) == 0 {
		t.Fatalf("expected an automatic link, got none (first=%d)", first.Memory.ID)
	}
	if second.Links[0].Dst != first.Memory.ID {
		t.Fatalf("linked to #%d, want #%d", second.Links[0].Dst, first.Memory.ID)
	}
	if second.Links[0].Similarity < 0.5 {
		t.Fatalf("similarity %.2f is below the link threshold", second.Links[0].Similarity)
	}
	d, err := svc.Get(first.Memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Links) != 1 {
		t.Fatalf("link should be bidirectional, got %d links", len(d.Links))
	}
}

func TestSearchHybrid(t *testing.T) {
	svc := newService(t)
	add(t, svc, "SQLite stores memory vectors as blobs", "fact")
	add(t, svc, "Kubernetes upgrades run monthly on Thursday", "fact")
	add(t, svc, "the CLI is written in Go", "fact")

	results, err := svc.Search(context.Background(), SearchInput{Query: "sqlite vectors", K: 3, Touch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if !strings.Contains(strings.ToLower(results[0].Memory.Content), "sqlite") {
		t.Fatalf("top result is not the sqlite memory: %q", results[0].Memory.Content)
	}
	if results[0].Via != "vector" {
		t.Fatalf("via = %q, want vector", results[0].Via)
	}
}

func TestSearchCategoryFilter(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if _, err := svc.Add(ctx, AddInput{Content: "shared topic alpha", Category: "fact"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(ctx, AddInput{Content: "shared topic beta", Category: "decision"}); err != nil {
		t.Fatal(err)
	}
	results, err := svc.Search(ctx, SearchInput{Query: "shared topic", K: 5, Category: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Memory.Category != "decision" {
		t.Fatalf("category filter failed: %+v", results)
	}
}

func TestReinforce(t *testing.T) {
	svc := newService(t)
	m := add(t, svc, "an important fact", "fact").Memory
	before := m.Importance
	got, err := svc.Reinforce(m.ID, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Importance <= before {
		t.Fatalf("importance did not increase: %.2f -> %.2f", before, got.Importance)
	}
	if got.Access != 1 {
		t.Fatalf("access count = %d, want 1", got.Access)
	}
}

func TestDecayArchivesAndPurges(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	m := add(t, svc, "low value trivia", "fact").Memory
	if _, err := svc.Update(ctx, m.ID, func(m *store.Memory) error {
		m.Importance = 0.01
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(3, 0, 0)

	report, err := svc.Decay(ctx, DecayOptions{DryRun: true, Now: future})
	if err != nil {
		t.Fatal(err)
	}
	if report.Archived != 1 {
		t.Fatalf("dry run archived %d, want 1", report.Archived)
	}
	live, err := svc.List(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatal("dry run must not change the database")
	}

	report, err = svc.Decay(ctx, DecayOptions{Now: future})
	if err != nil {
		t.Fatal(err)
	}
	if report.Archived != 1 {
		t.Fatalf("archived %d, want 1", report.Archived)
	}
	live, err = svc.List(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("archived memory still listed: %+v", live)
	}
}

func TestDecayPurges(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	m := add(t, svc, "dead trivia", "fact").Memory
	if _, err := svc.Update(ctx, m.ID, func(m *store.Memory) error {
		m.Importance = 0.01
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report, err := svc.Decay(ctx, DecayOptions{Now: time.Now().AddDate(3, 0, 0), Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Purged != 1 {
		t.Fatalf("purged %d, want 1", report.Purged)
	}
	if _, err := svc.Get(m.ID); err == nil {
		t.Fatal("memory should be gone")
	}
}

func TestGraphOperations(t *testing.T) {
	svc := newService(t)
	a := add(t, svc, "node alpha", "fact").Memory
	b := add(t, svc, "node beta", "fact").Memory
	c := add(t, svc, "node gamma", "fact").Memory
	if err := svc.Link(a.ID, b.ID, 0.8); err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(b.ID, c.ID, 0.7); err != nil {
		t.Fatal(err)
	}

	nodes, err := svc.Traverse(a.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("traverse reached %d nodes, want 3", len(nodes))
	}
	path, err := svc.Path(a.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 3 || path[0] != a.ID || path[2] != c.ID {
		t.Fatalf("unexpected path: %v", path)
	}
	clusters, err := svc.Clusters()
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Size != 3 {
		t.Fatalf("unexpected clusters: %+v", clusters)
	}
	orphans, err := svc.Orphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("unexpected orphans: %+v", orphans)
	}
	if err := svc.Unlink(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Unlink(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	orphans, err = svc.Orphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 3 {
		t.Fatalf("expected 3 orphans, got %d", len(orphans))
	}
}

func TestContextSummary(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	add(t, svc, "the deployment runs on Kubernetes", "fact")
	add(t, svc, "we chose SQLite for storage", "decision")
	out, err := svc.Context(ctx, ContextOptions{Days: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Memory Context", "## Decision", "## Fact", "Kubernetes", "SQLite"} {
		if !strings.Contains(out, want) {
			t.Fatalf("context is missing %q:\n%s", want, out)
		}
	}
}

func TestUpdateReembeds(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	m := add(t, svc, "the old topic", "fact").Memory
	if _, err := svc.Update(ctx, m.ID, func(m *store.Memory) error {
		m.Content = "a completely new topic about penguins"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	results, err := svc.Search(ctx, SearchInput{Query: "penguins", K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Memory.ID != m.ID {
		t.Fatalf("updated memory not found by search: %+v", results)
	}
}

func TestReindex(t *testing.T) {
	svc := newService(t)
	add(t, svc, "first memory", "fact")
	add(t, svc, "second memory", "fact")
	n, err := svc.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("reindexed %d, want 2", n)
	}
	if _, err := svc.Search(context.Background(), SearchInput{Query: "first", K: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestIngestChunks(t *testing.T) {
	svc := newService(t)
	text := "first paragraph about alpha\n\nsecond paragraph about beta\n"
	results, err := svc.Ingest(context.Background(), text, IngestOptions{Extract: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d memories, want 2", len(results))
	}
}

func TestStats(t *testing.T) {
	svc := newService(t)
	add(t, svc, "one", "fact")
	st, err := svc.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Memories != 1 || st.Dims != 256 || !strings.HasPrefix(st.Embedder, "hash") {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestDeleteArchiveAndHard(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	a := add(t, svc, "archive me", "fact").Memory
	if err := svc.Delete(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(a.ID); err != nil {
		t.Fatal(err)
	}
	live, err := svc.List(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatal("archived memory should not be listed")
	}
	if err := svc.Delete(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(a.ID); err == nil {
		t.Fatal("memory should be deleted")
	}
}

func TestRestore(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	m := add(t, svc, "bring me back", "fact").Memory
	if err := svc.Delete(ctx, m.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(ctx, SearchInput{Query: "bring me back", K: 3}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Restore(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Archived {
		t.Fatal("memory is still archived")
	}
	results, err := svc.Search(ctx, SearchInput{Query: "bring me back", K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Memory.ID != m.ID {
		t.Fatalf("restored memory not searchable: %+v", results)
	}
}

func TestStrength(t *testing.T) {
	now := time.Now()
	fresh := &store.Memory{Importance: 0.5, Category: "fact", CreatedAt: now, LastAccess: now}
	if got := Strength(fresh, 0, now); got < 0.7 || got > 0.8 {
		t.Fatalf("fresh strength = %.2f, want about 0.72", got)
	}
	old := &store.Memory{Importance: 0.5, Category: "fact", CreatedAt: now.AddDate(-1, 0, 0), LastAccess: now.AddDate(-1, 0, 0)}
	if got := Strength(old, 0, now); got >= Strength(fresh, 0, now) {
		t.Fatalf("old memory should be weaker: %.2f", got)
	}
	if got := Strength(fresh, 6, now); got <= Strength(fresh, 0, now) {
		t.Fatalf("linked memory should be stronger: %.2f", got)
	}
	pref := &store.Memory{Importance: 0.5, Category: "preference", CreatedAt: now, LastAccess: now}
	if got := Strength(pref, 0, now); got < Strength(fresh, 0, now) {
		t.Fatalf("preference should be stickier: %.2f", got)
	}
}

// TestConcurrentSearchAndReindex exercises the index swap under the same kind
// of concurrency the MCP server allows.
func TestConcurrentSearchAndReindex(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	add(t, svc, "the memory store uses sqlite", "fact")
	add(t, svc, "the memory store uses a vector index", "fact")

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < 20; j++ {
				if _, err := svc.Search(ctx, SearchInput{Query: "memory store", K: 5}); err != nil {
					t.Errorf("search: %v", err)

					return
				}
			}
		}()
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

		for j := 0; j < 5; j++ {
			if _, err := svc.Reindex(ctx); err != nil {
				t.Errorf("reindex: %v", err)

				return
			}
		}
	}()

	wg.Wait()
}
