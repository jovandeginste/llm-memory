// Package index wraps the tamnd/vec embedded vector database. vec keeps the
// whole index in process memory, so the index is rebuilt from SQLite on every
// start; SQLite remains the durable store.
package index

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/tamnd/vec"
)

// Item is one point to index.
type Item struct {
	ID       int64
	Category string
	Vector   []float32
}

// Hit is one nearest-neighbour result. Score is cosine similarity in [-1, 1].
type Hit struct {
	ID    int64
	Score float64
}

// Index is a goroutine-safe handle to the in-memory vector collection.
type Index struct {
	mu   sync.RWMutex
	db   *vec.DB
	coll *vec.Collection
	dims int
}

// New creates an empty index for vectors of the given dimension.
func New(ctx context.Context, dims int) (*Index, error) {
	if dims <= 0 {
		return nil, fmt.Errorf("vector dimension must be positive, got %d", dims)
	}
	db, err := vec.Open(":memory:")
	if err != nil {
		return nil, fmt.Errorf("open vector index: %w", err)
	}
	err = db.CreateCollection(ctx, vec.CollectionSchema{
		Name: "memories",
		Columns: []vec.ColumnDef{
			{Name: "embedding", Type: vec.TypeVector, Dim: dims, Metric: vec.MetricCosine},
			{Name: "category", Type: vec.TypeText},
		},
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create vector collection: %w", err)
	}
	coll, err := db.Collection("memories")
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db, coll: coll, dims: dims}, nil
}

// Dims returns the vector dimension of the index.
func (i *Index) Dims() int { return i.dims }

// Close releases the index.
func (i *Index) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.db.Close()
}

// Upsert adds or replaces points in one transaction.
func (i *Index) Upsert(ctx context.Context, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	points := make([]vec.Point, len(items))
	for n, it := range items {
		if len(it.Vector) != i.dims {
			return fmt.Errorf("vector for memory #%d has %d dims, index expects %d", it.ID, len(it.Vector), i.dims)
		}
		points[n] = vec.Point{
			ID: vec.PointID{N: uint64(it.ID)},
			Vectors: map[string]vec.AnyVector{
				"embedding": {Dense: vec.Vector(it.Vector)},
			},
			Meta: map[string]vec.Value{
				"category": vec.TextValue(it.Category),
			},
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	_, err := i.coll.UpsertBatch(ctx, points)
	return err
}

// Delete removes points from the index.
func (i *Index) Delete(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	points := make([]vec.PointID, len(ids))
	for n, id := range ids {
		points[n] = vec.PointID{N: uint64(id)}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	err := i.coll.DeleteBatch(ctx, points)
	// vec surfaces a missing point as a plain storage error, not a sentinel, so
	// match on the message. Deleting an absent point is a no-op for us.
	if err != nil && (errors.Is(err, vec.ErrNotFound) || strings.Contains(err.Error(), "not found")) {
		return nil
	}
	return err
}

// Search returns the k nearest memories, optionally filtered by category. An
// empty category means "any".
func (i *Index) Search(ctx context.Context, query []float32, k int, category string) ([]Hit, error) {
	if k <= 0 {
		k = 10
	}
	if len(query) != i.dims {
		return nil, fmt.Errorf("query has %d dims, index expects %d", len(query), i.dims)
	}
	qb := i.coll.Query("embedding", vec.Vector(query)).K(k)
	if category != "" {
		qb = qb.Filter("category = ?", category)
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	results, err := qb.All(ctx)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(results))
	for _, r := range results {
		hits = append(hits, Hit{ID: int64(r.ID.N), Score: float64(r.Score)})
	}
	return hits, nil
}

// Len returns the number of indexed points.
func (i *Index) Len(ctx context.Context) (int64, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.coll.Count(ctx)
}
