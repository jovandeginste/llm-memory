package embed

import (
	"context"
	"math"
	"testing"
)

func TestHashDeterministic(t *testing.T) {
	e := NewHash(128)
	a, err := e.Embed(context.Background(), []string{"the quick brown fox"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Embed(context.Background(), []string{"the quick brown fox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a[0]) != 128 {
		t.Fatalf("got %d dims, want 128", len(a[0]))
	}
	for i := range a[0] {
		if a[0][i] != b[0][i] {
			t.Fatalf("embedding is not deterministic at %d: %v != %v", i, a[0][i], b[0][i])
		}
	}
}

func TestHashSimilarityOrdering(t *testing.T) {
	e := NewHash(256)
	vecs, err := e.Embed(context.Background(), []string{
		"sqlite vector storage for agent memory",
		"agent memory stored in sqlite with vector search",
		"kubernetes cluster upgrade schedule",
	})
	if err != nil {
		t.Fatal(err)
	}
	related := cosine(vecs[0], vecs[1])
	unrelated := cosine(vecs[0], vecs[2])
	if related <= unrelated {
		t.Fatalf("related similarity %.3f should exceed unrelated %.3f", related, unrelated)
	}
	if related < 0.5 {
		t.Fatalf("related similarity %.3f is unexpectedly low", related)
	}
}

func TestHashEmptyText(t *testing.T) {
	e := NewHash(32)
	vecs, err := e.Embed(context.Background(), []string{""})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vecs[0] {
		if v != 0 {
			t.Fatalf("empty text produced non-zero value at %d", i)
		}
	}
}

func TestTokenize(t *testing.T) {
	got := Tokenize("The CLI, it's 42-times better!")
	want := []string{"the", "cli", "it", "42", "times", "better"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// cosine computes the cosine similarity of two vectors.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
