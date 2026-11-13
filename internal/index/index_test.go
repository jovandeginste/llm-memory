package index

import (
	"context"
	"testing"
)

func TestUpsertSearchDelete(t *testing.T) {
	ctx := context.Background()
	idx, err := New(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	err = idx.Upsert(ctx, []Item{
		{ID: 1, Category: "fact", Vector: []float32{1, 0, 0}},
		{ID: 2, Category: "decision", Vector: []float32{0, 1, 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := idx.Len(ctx); err != nil || n != 2 {
		t.Fatalf("len = %d err=%v", n, err)
	}

	hits, err := idx.Search(ctx, []float32{0.9, 0.1, 0}, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != 1 {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	if hits[0].Score < 0.9 {
		t.Fatalf("top score = %.2f", hits[0].Score)
	}

	filtered, err := idx.Search(ctx, []float32{0.9, 0.1, 0}, 2, "decision")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != 2 {
		t.Fatalf("category filter failed: %+v", filtered)
	}

	if err := idx.Delete(ctx, []int64{1}); err != nil {
		t.Fatal(err)
	}
	if n, err := idx.Len(ctx); err != nil || n != 1 {
		t.Fatalf("len after delete = %d err=%v", n, err)
	}
	// Deleting a missing point is a no-op.
	if err := idx.Delete(ctx, []int64{99}); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestDimensionMismatch(t *testing.T) {
	ctx := context.Background()
	idx, err := New(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.Upsert(ctx, []Item{{ID: 1, Vector: []float32{1, 2, 3}}}); err == nil {
		t.Fatal("expected a dimension error")
	}
	if _, err := idx.Search(ctx, []float32{1}, 1, ""); err == nil {
		t.Fatal("expected a dimension error")
	}
}

func TestEmptyIndex(t *testing.T) {
	ctx := context.Background()
	idx, err := New(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	hits, err := idx.Search(ctx, []float32{1, 0, 0, 0}, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits, got %+v", hits)
	}
}
