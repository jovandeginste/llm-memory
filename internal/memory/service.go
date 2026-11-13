// Package memory implements the self-linking memory engine: add, hybrid
// search, automatic linking, reinforcement, decay, graph queries, and context
// rebuilds.
package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"llm-memory/internal/config"
	"llm-memory/internal/embed"
	"llm-memory/internal/extract"
	"llm-memory/internal/index"
	"llm-memory/internal/store"
)

// Service ties the SQLite store, the vector index, and the embedder together.
// mu guards the index pointer and the lifetime of the index it points to, so
// concurrent MCP tool calls cannot use an index that a reindex or restore has
// just closed.
type Service struct {
	mu  sync.RWMutex
	st  *store.Store
	idx *index.Index
	emb embed.Embedder
	cfg config.Config
}

// Open resolves the configured embedder and opens the service.
func Open(ctx context.Context, cfg config.Config) (*Service, error) {
	emb, err := embed.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return OpenWith(ctx, cfg, emb)
}

// OpenWith opens the service with an explicit embedder. It is used by tests and
// by callers that manage the embedder themselves.
func OpenWith(ctx context.Context, cfg config.Config, emb embed.Embedder) (*Service, error) {
	st, err := store.Open(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Service{st: st, emb: emb, cfg: cfg}
	if err := s.initIndex(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return s, nil
}

// initIndex resolves the vector dimension, validates the embedder, and rebuilds
// the in-memory index from SQLite.
func (s *Service) initIndex(ctx context.Context) error {
	storedName, err := s.st.GetMeta("embedder")
	if err != nil {
		return err
	}
	storedDims, err := s.st.GetMeta("dims")
	if err != nil {
		return err
	}
	var dims int
	if storedDims == "" {
		vecs, err := s.emb.Embed(ctx, []string{"dimension probe"})
		if err != nil {
			return fmt.Errorf("probe embedder: %w", err)
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return errors.New("embedder returned an empty vector")
		}
		dims = len(vecs[0])
		if err := s.st.SetMeta("dims", strconv.Itoa(dims)); err != nil {
			return err
		}
		if err := s.st.SetMeta("embedder", s.emb.Name()); err != nil {
			return err
		}
	} else {
		dims, err = strconv.Atoi(storedDims)
		if err != nil || dims <= 0 {
			return fmt.Errorf("invalid stored vector dimension %q", storedDims)
		}
		if storedName != "" && storedName != s.emb.Name() {
			return fmt.Errorf("database was built with embedder %q but %q is configured; run \"llmmem reindex\" to rebuild the vectors",
				storedName, s.emb.Name())
		}
	}
	return s.buildIndex(ctx, dims)
}

// buildIndex creates a fresh vector index from SQLite and swaps it in. It is
// also the recovery path after a point was removed from vec, because vec cannot
// re-add a deleted id. The caller must hold s.mu for writing when the service is
// shared.
func (s *Service) buildIndex(ctx context.Context, dims int) error {
	mems, err := s.st.AllForIndex()
	if err != nil {
		return err
	}
	items := make([]index.Item, 0, len(mems))
	for _, m := range mems {
		if len(m.Embedding) != dims {
			return fmt.Errorf("memory #%d has %d dims but the database expects %d; run \"llmmem reindex\"",
				m.ID, len(m.Embedding), dims)
		}
		items = append(items, index.Item{ID: m.ID, Category: m.Category, Vector: m.Embedding})
	}
	idx, err := index.New(ctx, dims)
	if err != nil {
		return err
	}
	if err := idx.Upsert(ctx, items); err != nil {
		idx.Close()
		return err
	}
	old := s.idx
	s.idx = idx
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// Close closes the vector index and the database.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	if s.idx != nil {
		errs = append(errs, s.idx.Close())
	}
	errs = append(errs, s.st.Close())
	return errors.Join(errs...)
}

// Store exposes the underlying store for the CLI (stats, raw reads).
func (s *Service) Store() *store.Store { return s.st }

// Embedder returns the active embedder.
func (s *Service) Embedder() embed.Embedder { return s.emb }

// Dims returns the vector dimension of the database.
func (s *Service) Dims() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.idx.Dims()
}

// Embed returns the vector of one text.
func (s *Service) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := s.emb.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors for one input", len(vecs))
	}
	return vecs[0], nil
}

// Add stores one memory, indexes it, and links it to related memories.
func (s *Service) Add(ctx context.Context, in AddInput) (*AddResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return nil, errors.New("memory content is empty")
	}
	if in.Category == "" {
		in.Category = "fact"
	}
	if !ValidCategory(in.Category) {
		return nil, fmt.Errorf("unknown category %q (use %s)", in.Category, strings.Join(Categories, ", "))
	}
	if in.Importance == 0 {
		in.Importance = 0.5
	}
	in.Importance = clamp01(in.Importance)
	in.Tags = normalizeTags(in.Tags)

	vec, err := s.Embed(ctx, in.Content)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}

	// Find neighbours before inserting so the new memory cannot match itself.
	link := in.Link == nil || *in.Link
	threshold := in.Threshold
	if threshold <= 0 {
		threshold = s.cfg.LinkThreshold
	}
	topK := in.TopK
	if topK <= 0 {
		topK = s.cfg.LinkTopK
	}
	var hits []index.Hit
	if link {
		hits, err = s.idx.Search(ctx, vec, topK*2+1, "")
		if err != nil {
			return nil, fmt.Errorf("find related memories: %w", err)
		}
		hits = filterHits(hits, threshold, topK)
	}

	now := time.Now().UTC()
	m := &store.Memory{
		Category:   in.Category,
		Content:    in.Content,
		Importance: in.Importance,
		Tags:       in.Tags,
		Source:     in.Source,
		Session:    in.Session,
		CreatedAt:  now,
		UpdatedAt:  now,
		LastAccess: now,
		Embedding:  vec,
	}
	if _, err := s.st.InsertMemory(m); err != nil {
		return nil, fmt.Errorf("store memory: %w", err)
	}
	if err := s.idx.Upsert(ctx, []index.Item{{ID: m.ID, Category: m.Category, Vector: vec}}); err != nil {
		// Roll back the row so the database and index stay consistent.
		_ = s.st.DeleteMemory(m.ID)
		return nil, fmt.Errorf("index memory: %w", err)
	}

	res := &AddResult{Memory: m}
	if link {
		reinforced := make([]int64, 0, len(hits))
		for _, h := range hits {
			if h.ID == m.ID {
				continue
			}
			if _, err := s.st.AddLink(m.ID, h.ID, h.Score); err != nil {
				return nil, fmt.Errorf("link #%d -> #%d: %w", m.ID, h.ID, err)
			}
			if _, err := s.st.AddLink(h.ID, m.ID, h.Score); err != nil {
				return nil, fmt.Errorf("link #%d -> #%d: %w", m.ID, h.ID, err)
			}
			res.Links = append(res.Links, store.Link{Src: m.ID, Dst: h.ID, Similarity: h.Score, CreatedAt: now})
			reinforced = append(reinforced, h.ID)
		}
		// Memory evolution: linking bumps the updated time of the older memory.
		if err := s.st.BumpUpdated(reinforced); err != nil {
			return nil, err
		}
	}
	m.Degree = len(res.Links)
	m.Strength = Strength(m, m.Degree, now)
	return res, nil
}

// filterHits drops hits below threshold and caps the list.
func filterHits(hits []index.Hit, threshold float64, topK int) []index.Hit {
	out := make([]index.Hit, 0, topK)
	for _, h := range hits {
		if h.Score < threshold {
			continue
		}
		out = append(out, h)
		if len(out) >= topK {
			break
		}
	}
	return out
}

// Search runs hybrid retrieval: vector search plus keyword search, fused with
// reciprocal rank fusion, optionally expanded over the link graph.
func (s *Service) Search(ctx context.Context, in SearchInput) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		return nil, errors.New("search query is empty")
	}
	if in.K <= 0 {
		in.K = 10
	}
	vec, err := s.Embed(ctx, in.Query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	candidates := in.K * 4
	if candidates < 20 {
		candidates = 20
	}

	type merged struct {
		score      float64
		similarity float64
		via        string
	}
	byID := map[int64]*merged{}
	addRanked := func(id int64, rank int, via string, similarity float64) {
		m, ok := byID[id]
		if !ok {
			m = &merged{via: via}
			byID[id] = m
		}
		m.score += 1.0 / (60.0 + float64(rank))
		if similarity != 0 || via == "vector" {
			m.similarity = similarity
			m.via = "vector"
		}
	}

	hits, err := s.idx.Search(ctx, vec, candidates, in.Category)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	for i, h := range hits {
		if in.MinSimilarity > 0 && h.Score < in.MinSimilarity {
			continue
		}
		addRanked(h.ID, i+1, "vector", h.Score)
	}

	if q := ftsQuery(in.Query); q != "" {
		kw, err := s.st.SearchFTS(q, candidates, in.Category)
		if err != nil {
			return nil, fmt.Errorf("keyword search: %w", err)
		}
		for i, h := range kw {
			addRanked(h.ID, i+1, "keyword", 0)
		}
	}
	if len(byID) == 0 {
		return nil, nil
	}

	// Rank and keep the top k before graph expansion.
	ranked := make([]int64, 0, len(byID))
	for id := range byID {
		ranked = append(ranked, id)
	}
	sort.Slice(ranked, func(a, b int) bool {
		if byID[ranked[a]].score == byID[ranked[b]].score {
			return ranked[a] < ranked[b]
		}
		return byID[ranked[a]].score > byID[ranked[b]].score
	})
	if len(ranked) > in.K {
		ranked = ranked[:in.K]
	}

	// Graph expansion: walk the links of the top hits and add neighbours with a
	// hop penalty. This is associative recall that keyword search cannot do.
	if in.GraphDepth > 0 {
		seen := map[int64]bool{}
		for _, id := range ranked {
			seen[id] = true
		}
		frontier := append([]int64(nil), ranked...)
		for hop := 1; hop <= in.GraphDepth; hop++ {
			next := []int64{}
			for _, id := range frontier {
				links, err := s.st.Links(id)
				if err != nil {
					return nil, err
				}
				parentScore := byID[id].score
				if parentScore == 0 {
					parentScore = 1.0 / 60.0
				}
				for _, l := range links {
					if seen[l.Dst] {
						continue
					}
					seen[l.Dst] = true
					byID[l.Dst] = &merged{
						score:      parentScore * 0.5 * l.Similarity,
						similarity: l.Similarity,
						via:        "graph",
					}
					ranked = append(ranked, l.Dst)
					next = append(next, l.Dst)
					if len(ranked) >= in.K*2 {
						break
					}
				}
				if len(ranked) >= in.K*2 {
					break
				}
			}
			frontier = next
			if len(frontier) == 0 {
				break
			}
		}
	}

	now := time.Now().UTC()
	degrees, err := s.st.Degrees()
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(ranked))
	var touched []int64
	for _, id := range ranked {
		m, err := s.st.GetMemory(id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && m.Archived) {
			continue
		}
		if err != nil {
			return nil, err
		}
		m.Degree = degrees[id]
		m.Strength = Strength(m, m.Degree, now)
		out = append(out, SearchResult{
			Memory:     m,
			Score:      byID[id].score,
			Similarity: byID[id].similarity,
			Via:        byID[id].via,
		})
		touched = append(touched, id)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	if in.Touch {
		if err := s.st.Touch(touched); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Get returns one memory with its links.
func (s *Service) Get(id int64) (*Detail, error) {
	m, err := s.st.GetMemory(id)
	if err != nil {
		return nil, err
	}
	links, err := s.st.Links(id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	degree := len(links)
	m.Degree = degree
	m.Strength = Strength(m, degree, now)
	detail := &Detail{Memory: m}
	for _, l := range links {
		other, err := s.st.GetMemory(l.Dst)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		detail.Links = append(detail.Links, LinkView{
			Link:  l,
			Other: other,
			Score: l.Similarity,
		})
	}
	return detail, nil
}

// List returns memories matching filter with strength filled in.
func (s *Service) List(f store.Filter) ([]*store.Memory, error) {
	mems, err := s.st.List(f)
	if err != nil {
		return nil, err
	}
	degrees, err := s.st.Degrees()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, m := range mems {
		m.Degree = degrees[m.ID]
		m.Strength = Strength(m, m.Degree, now)
	}
	return mems, nil
}

// Update applies a patch to a memory. Changing the content re-embeds and
// re-indexes it; links are left alone.
func (s *Service) Update(ctx context.Context, id int64, patch func(*store.Memory) error) (*store.Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, err := s.st.GetMemory(id)
	if err != nil {
		return nil, err
	}
	oldContent := m.Content
	if err := patch(m); err != nil {
		return nil, err
	}
	if strings.TrimSpace(m.Content) == "" {
		return nil, errors.New("memory content is empty")
	}
	if !ValidCategory(m.Category) {
		return nil, fmt.Errorf("unknown category %q", m.Category)
	}
	m.Importance = clamp01(m.Importance)
	m.Tags = normalizeTags(m.Tags)
	m.UpdatedAt = time.Now().UTC()
	if m.Content != oldContent {
		vec, err := s.Embed(ctx, m.Content)
		if err != nil {
			return nil, err
		}
		m.Embedding = vec
		if err := s.idx.Upsert(ctx, []index.Item{{ID: m.ID, Category: m.Category, Vector: vec}}); err != nil {
			return nil, err
		}
	}
	if err := s.st.UpdateMemory(m); err != nil {
		return nil, err
	}
	degree, err := s.st.Degree(m.ID)
	if err != nil {
		return nil, err
	}
	m.Degree = degree
	m.Strength = Strength(m, degree, m.UpdatedAt)
	return m, nil
}

// Delete removes a memory, its links, and its index entry. When hard is false
// the memory is archived instead.
func (s *Service) Delete(ctx context.Context, id int64, hard bool) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, err := s.st.GetMemory(id)
	if err != nil {
		return err
	}
	if !m.Archived {
		if err := s.idx.Delete(ctx, []int64{id}); err != nil {
			return err
		}
	}
	if hard {
		return s.st.DeleteMemory(id)
	}
	return s.st.SetArchived(id, true)
}

// Restore brings an archived memory back into the index.
func (s *Service) Restore(ctx context.Context, id int64) (*store.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.st.GetMemory(id)
	if err != nil {
		return nil, err
	}
	if !m.Archived {
		return m, nil
	}
	if len(m.Embedding) != s.idx.Dims() {
		vec, err := s.Embed(ctx, m.Content)
		if err != nil {
			return nil, err
		}
		if err := s.st.SetEmbedding(id, vec); err != nil {
			return nil, err
		}
	}
	if err := s.st.SetArchived(id, false); err != nil {
		return nil, err
	}
	// vec cannot re-add a deleted point id, so rebuild the index instead of
	// upserting the restored memory.
	if err := s.buildIndex(ctx, s.idx.Dims()); err != nil {
		return nil, err
	}
	m.Archived = false
	degree, err := s.st.Degree(id)
	if err != nil {
		return nil, err
	}
	m.Degree = degree
	m.Strength = Strength(m, degree, time.Now().UTC())
	return m, nil
}

// Link creates a bidirectional link between two memories.
func (s *Service) Link(src, dst int64, similarity float64) error {
	if src == dst {
		return errors.New("cannot link a memory to itself")
	}
	if similarity <= 0 {
		similarity = 1
	}
	for _, id := range []int64{src, dst} {
		if _, err := s.st.GetMemory(id); err != nil {
			return err
		}
	}
	if _, err := s.st.AddLink(src, dst, similarity); err != nil {
		return err
	}
	if _, err := s.st.AddLink(dst, src, similarity); err != nil {
		return err
	}
	return s.st.BumpUpdated([]int64{src, dst})
}

// Unlink removes both directions of a link.
func (s *Service) Unlink(src, dst int64) error {
	if err := s.st.DeleteLink(src, dst); err != nil {
		return err
	}
	return s.st.DeleteLink(dst, src)
}

// Reinforce boosts a memory's importance and records an access.
func (s *Service) Reinforce(id int64, amount float64) (*store.Memory, error) {
	if amount == 0 {
		amount = 0.1
	}
	m, err := s.st.GetMemory(id)
	if err != nil {
		return nil, err
	}
	m.Importance = clamp01(m.Importance + amount)
	m.Access++
	m.LastAccess = time.Now().UTC()
	m.UpdatedAt = m.LastAccess
	if err := s.st.UpdateMemory(m); err != nil {
		return nil, err
	}
	degree, err := s.st.Degree(id)
	if err != nil {
		return nil, err
	}
	m.Degree = degree
	m.Strength = Strength(m, degree, m.LastAccess)
	return m, nil
}

// Decay archives weak memories and, when Purge is set, hard-deletes dead ones.
func (s *Service) Decay(ctx context.Context, opts DecayOptions) (*DecayReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if opts.ArchiveAt <= 0 {
		opts.ArchiveAt = archiveThreshold
	}
	if opts.PurgeAt <= 0 {
		opts.PurgeAt = purgeThreshold
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	mems, err := s.st.List(store.Filter{Limit: 1_000_000})
	if err != nil {
		return nil, err
	}
	degrees, err := s.st.Degrees()
	if err != nil {
		return nil, err
	}
	report := &DecayReport{}
	for _, m := range mems {
		report.Scanned++
		degree := degrees[m.ID]
		strength := Strength(m, degree, opts.Now)
		action := ""
		switch {
		case strength < opts.PurgeAt && opts.Purge:
			action = "purged"
			report.Purged++
		case strength < opts.ArchiveAt:
			action = "archived"
			report.Archived++
		default:
			continue
		}
		report.Actions = append(report.Actions, DecayAction{
			ID: m.ID, Action: action, Strength: strength, Content: m.Content,
		})
		if opts.DryRun {
			continue
		}
		if action == "purged" {
			if err := s.idx.Delete(ctx, []int64{m.ID}); err != nil {
				return nil, err
			}
			if err := s.st.DeleteMemory(m.ID); err != nil {
				return nil, err
			}
			continue
		}
		if err := s.idx.Delete(ctx, []int64{m.ID}); err != nil {
			return nil, err
		}
		if err := s.st.SetArchived(m.ID, true); err != nil {
			return nil, err
		}
	}
	return report, nil
}

// Context builds a markdown executive summary of recent memories, grouped by
// category. It is meant to be injected at the start of a session.
func (s *Service) Context(ctx context.Context, opts ContextOptions) (string, error) {
	if opts.Days <= 0 {
		opts.Days = 7
	}
	if opts.Limit <= 0 {
		opts.Limit = 40
	}
	since := time.Now().UTC().AddDate(0, 0, -opts.Days)
	mems, err := s.st.ListSince(since, opts.Limit*5)
	if err != nil {
		return "", err
	}
	degrees, err := s.st.Degrees()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	for _, m := range mems {
		m.Degree = degrees[m.ID]
		m.Strength = Strength(m, m.Degree, now)
	}
	sort.SliceStable(mems, func(a, b int) bool {
		if mems[a].Strength == mems[b].Strength {
			return mems[a].UpdatedAt.After(mems[b].UpdatedAt)
		}
		return mems[a].Strength > mems[b].Strength
	})
	if len(mems) > opts.Limit {
		mems = mems[:opts.Limit]
	}
	if len(mems) == 0 {
		return fmt.Sprintf("# Memory Context\n\n_No memories updated in the last %d days._\n", opts.Days), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Memory Context\n\n_Generated %s from %d memories updated in the last %d days._\n",
		now.Format(time.RFC3339), len(mems), opts.Days)
	for _, cat := range Categories {
		var group []*store.Memory
		for _, m := range mems {
			if m.Category == cat {
				group = append(group, m)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\n## %s\n\n", titleCase(cat))
		for _, m := range group {
			fmt.Fprintf(&sb, "- **#%d** %s", m.ID, oneLine(m.Content))
			if len(m.Tags) > 0 {
				fmt.Fprintf(&sb, " _[%s]_", strings.Join(m.Tags, ", "))
			}
			fmt.Fprintf(&sb, " — strength %.2f, updated %s\n",
				m.Strength, m.UpdatedAt.Format("2006-01-02"))
		}
	}
	return sb.String(), nil
}

// Traverse walks the graph from id for the given number of hops. Links are
// treated as undirected.
func (s *Service) Traverse(id int64, hops int) ([]Node, error) {
	if hops <= 0 {
		hops = 2
	}
	if _, err := s.st.GetMemory(id); err != nil {
		return nil, err
	}
	adj, err := s.adjacency()
	if err != nil {
		return nil, err
	}
	nodes := []Node{{ID: id, Hop: 0, Score: 1}}
	seen := map[int64]bool{id: true}
	frontier := []Node{{ID: id, Hop: 0, Score: 1}}
	for hop := 1; hop <= hops; hop++ {
		var next []Node
		for _, cur := range frontier {
			for _, edge := range adj[cur.ID] {
				if seen[edge.to] {
					continue
				}
				seen[edge.to] = true
				n := Node{ID: edge.to, Hop: hop, Score: cur.Score * 0.5 * edge.similarity, Via: cur.ID}
				nodes = append(nodes, n)
				next = append(next, n)
			}
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}
	return nodes, nil
}

// Path finds the shortest undirected path between two memories.
func (s *Service) Path(from, to int64) ([]int64, error) {
	for _, id := range []int64{from, to} {
		if _, err := s.st.GetMemory(id); err != nil {
			return nil, err
		}
	}
	if from == to {
		return []int64{from}, nil
	}
	adj, err := s.adjacency()
	if err != nil {
		return nil, err
	}
	prev := map[int64]int64{}
	seen := map[int64]bool{from: true}
	queue := []int64{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, edge := range adj[cur] {
			if seen[edge.to] {
				continue
			}
			seen[edge.to] = true
			prev[edge.to] = cur
			if edge.to == to {
				return rebuildPath(prev, from, to), nil
			}
			queue = append(queue, edge.to)
		}
	}
	return nil, fmt.Errorf("no path between #%d and #%d", from, to)
}

// rebuildPath walks the predecessor map back from to.
func rebuildPath(prev map[int64]int64, from, to int64) []int64 {
	path := []int64{to}
	for cur := to; cur != from; {
		p, ok := prev[cur]
		if !ok {
			return nil
		}
		path = append(path, p)
		cur = p
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// Clusters returns the connected components of the graph, largest first.
func (s *Service) Clusters() ([]Cluster, error) {
	adj, err := s.adjacency()
	if err != nil {
		return nil, err
	}
	mems, err := s.st.List(store.Filter{Limit: 1_000_000})
	if err != nil {
		return nil, err
	}
	byID := map[int64]*store.Memory{}
	for _, m := range mems {
		byID[m.ID] = m
	}
	seen := map[int64]bool{}
	var clusters []Cluster
	for _, m := range mems {
		if seen[m.ID] {
			continue
		}
		var members []int64
		queue := []int64{m.ID}
		seen[m.ID] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			members = append(members, cur)
			for _, edge := range adj[cur] {
				if seen[edge.to] {
					continue
				}
				seen[edge.to] = true
				queue = append(queue, edge.to)
			}
		}
		c := Cluster{Members: members, Size: len(members), Categories: map[string]int{}}
		tagCount := map[string]int{}
		for _, id := range members {
			mem := byID[id]
			if mem == nil {
				continue
			}
			c.Categories[mem.Category]++
			for _, t := range mem.Tags {
				tagCount[t]++
			}
		}
		c.TopTags = topKeys(tagCount, 5)
		clusters = append(clusters, c)
	}
	sort.Slice(clusters, func(a, b int) bool { return clusters[a].Size > clusters[b].Size })
	return clusters, nil
}

// Orphans returns live memories with no links.
func (s *Service) Orphans() ([]*store.Memory, error) {
	mems, err := s.st.List(store.Filter{Limit: 1_000_000})
	if err != nil {
		return nil, err
	}
	degrees, err := s.st.Degrees()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var out []*store.Memory
	for _, m := range mems {
		if degrees[m.ID] != 0 {
			continue
		}
		m.Degree = 0
		m.Strength = Strength(m, 0, now)
		out = append(out, m)
	}
	return out, nil
}

// Stats summarises the database.
func (s *Service) Stats() (*Stats, error) {
	memories, archived, links, err := s.st.Counts()
	if err != nil {
		return nil, err
	}
	byCategory, err := s.st.CategoryCounts()
	if err != nil {
		return nil, err
	}
	last, err := s.st.MaxUpdated()
	if err != nil {
		return nil, err
	}
	st := &Stats{
		Memories: memories, Archived: archived, Links: links,
		ByCategory: byCategory,
		Embedder:   s.emb.Name(), Dims: s.Dims(), DB: s.st.Path(),
	}
	if memories > 0 {
		st.AvgLinks = float64(links) / float64(memories)
	}
	if !last.IsZero() {
		st.LastUpdated = last.Format(time.RFC3339)
	}
	return st, nil
}

// Reindex re-embeds every live memory with the current embedder and rebuilds the
// vector index. It is required after changing embedding models.
func (s *Service) Reindex(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	mems, err := s.st.AllForIndex()
	if err != nil {
		return 0, err
	}
	items := make([]index.Item, 0, len(mems))
	dims := 0
	for start := 0; start < len(mems); start += 32 {
		end := start + 32
		if end > len(mems) {
			end = len(mems)
		}
		batch := mems[start:end]
		texts := make([]string, len(batch))
		for i, m := range batch {
			texts[i] = m.Content
		}
		vecs, err := s.emb.Embed(ctx, texts)
		if err != nil {
			return 0, fmt.Errorf("re-embed batch at %d: %w", start, err)
		}
		for i, m := range batch {
			if len(vecs[i]) == 0 {
				return 0, fmt.Errorf("empty embedding for memory #%d", m.ID)
			}
			if dims == 0 {
				dims = len(vecs[i])
			}
			if len(vecs[i]) != dims {
				return 0, fmt.Errorf("embedder returned inconsistent dimensions (%d and %d)", dims, len(vecs[i]))
			}
			if err := s.st.SetEmbedding(m.ID, vecs[i]); err != nil {
				return 0, err
			}
			items = append(items, index.Item{ID: m.ID, Category: m.Category, Vector: vecs[i]})
		}
	}
	if dims == 0 {
		dims = s.idx.Dims()
	}
	idx, err := index.New(ctx, dims)
	if err != nil {
		return 0, err
	}
	if err := idx.Upsert(ctx, items); err != nil {
		idx.Close()
		return 0, err
	}
	old := s.idx
	s.idx = idx
	if err := s.st.SetMeta("dims", strconv.Itoa(dims)); err != nil {
		return 0, err
	}
	if err := s.st.SetMeta("embedder", s.emb.Name()); err != nil {
		return 0, err
	}
	if old != nil {
		_ = old.Close()
	}
	return len(items), nil
}

// Extractor returns the configured fact extractor, or nil when extraction is
// not configured.
func (s *Service) Extractor() *extract.Extractor {
	if s.cfg.ExtractURL == "" || s.cfg.ExtractModel == "" {
		return nil
	}
	return extract.New(s.cfg.ExtractURL, s.cfg.ExtractKey, s.cfg.ExtractModel)
}

// IngestOptions controls raw-text ingestion.
type IngestOptions struct {
	Category   string
	Source     string
	Session    string
	Importance float64
	Tags       []string
	// Extract forces LLM fact extraction when an extractor is configured.
	Extract bool
}

// Ingest turns raw text into memories. With a configured extractor and Extract
// set, the text is split into atomic facts by the LLM; otherwise it is chunked
// on blank lines.
func (s *Service) Ingest(ctx context.Context, text string, opts IngestOptions) ([]*AddResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("input text is empty")
	}
	type fact struct {
		Content    string   `json:"content"`
		Category   string   `json:"category"`
		Importance float64  `json:"importance"`
		Tags       []string `json:"tags"`
	}
	var facts []fact
	if opts.Extract {
		if ex := s.Extractor(); ex != nil {
			got, err := ex.Extract(ctx, text)
			if err != nil {
				return nil, err
			}
			for _, f := range got {
				facts = append(facts, fact{Content: f.Content, Category: f.Category, Importance: f.Importance, Tags: f.Tags})
			}
		}
	}
	if len(facts) == 0 {
		for _, chunk := range splitChunks(text, 4000) {
			facts = append(facts, fact{
				Content:    chunk,
				Category:   opts.Category,
				Importance: opts.Importance,
				Tags:       opts.Tags,
			})
		}
	}
	var out []*AddResult
	for _, f := range facts {
		cat := f.Category
		if cat == "" {
			cat = opts.Category
		}
		if !ValidCategory(cat) {
			cat = "fact"
		}
		imp := f.Importance
		if imp == 0 {
			imp = opts.Importance
		}
		tags := append(append([]string{}, opts.Tags...), f.Tags...)
		res, err := s.Add(ctx, AddInput{
			Content: f.Content, Category: cat,
			Importance: imp, Tags: tags, Source: opts.Source, Session: opts.Session,
		})
		if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

// edge is one undirected graph edge with its similarity.
type edge struct {
	to         int64
	similarity float64
}

// adjacency builds the undirected adjacency list of the whole graph.
func (s *Service) adjacency() (map[int64][]edge, error) {
	links, err := s.st.AllLinks()
	if err != nil {
		return nil, err
	}
	adj := map[int64][]edge{}
	for _, l := range links {
		adj[l.Src] = append(adj[l.Src], edge{to: l.Dst, similarity: l.Similarity})
		adj[l.Dst] = append(adj[l.Dst], edge{to: l.Src, similarity: l.Similarity})
	}
	return adj, nil
}

// normalizeTags trims, lowercases, and deduplicates tags.
func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ftsQuery turns free text into a safe FTS5 OR query.
func ftsQuery(text string) string {
	tokens := embed.Tokenize(text)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.ReplaceAll(t, `"`, `""`)
		parts = append(parts, `"`+t+`"`)
	}
	return strings.Join(parts, " OR ")
}

// splitChunks splits text on blank lines and caps each chunk at max runes,
// breaking long paragraphs on sentence boundaries.
func splitChunks(text string, max int) []string {
	var chunks []string
	for _, block := range strings.Split(text, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		for len(block) > max {
			cut := strings.LastIndexAny(block[:max], ".!?\n")
			if cut < max/2 {
				cut = max
			}
			chunks = append(chunks, strings.TrimSpace(block[:cut+1]))
			block = strings.TrimSpace(block[cut+1:])
		}
		if block != "" {
			chunks = append(chunks, block)
		}
	}
	return chunks
}

// topKeys returns the n highest-count keys, ties broken alphabetically.
func topKeys(counts map[string]int, n int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if counts[keys[a]] == counts[keys[b]] {
			return keys[a] < keys[b]
		}
		return counts[keys[a]] > counts[keys[b]]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// titleCase uppercases the first letter of s.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// oneLine collapses whitespace and truncates long content for summaries.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 220 {
		s = string(r[:217]) + "..."
	}
	return s
}
