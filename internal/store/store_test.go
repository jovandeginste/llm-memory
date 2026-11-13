package store

import (
	"testing"
	"time"
)

// openTemp opens a store in a temporary directory.
func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/memory.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertGetUpdate(t *testing.T) {
	s := openTemp(t)
	m := &Memory{
		Category: "fact", Content: "hello world", Importance: 0.4,
		Tags: []string{"x", "y"}, Embedding: []float32{0.1, 0.2, 0.3},
	}
	id, err := s.InsertMemory(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMemory(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello world" || got.Category != "fact" || len(got.Embedding) != 3 {
		t.Fatalf("unexpected memory: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[1] != "y" {
		t.Fatalf("unexpected tags: %v", got.Tags)
	}
	got.Content = "changed"
	got.Archived = true
	if err := s.UpdateMemory(got); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetMemory(id)
	if err != nil {
		t.Fatal(err)
	}
	if again.Content != "changed" || !again.Archived {
		t.Fatalf("update not persisted: %+v", again)
	}
}

func TestGetMissing(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetMemory(999); err == nil {
		t.Fatal("expected an error for a missing memory")
	}
}

func TestKeywordSearch(t *testing.T) {
	s := openTemp(t)
	for _, content := range []string{
		"SQLite stores vectors as blobs",
		"Kubernetes upgrades run monthly",
	} {
		if _, err := s.InsertMemory(&Memory{
			Category: "fact", Content: content, Importance: 0.5,
			Embedding: []float32{1, 0},
		}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := s.SearchFTS(`"sqlite" OR "vectors"`, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
}

func TestLinksAndCascade(t *testing.T) {
	s := openTemp(t)
	a, _ := s.InsertMemory(&Memory{Category: "fact", Content: "a", Importance: 0.5, Embedding: []float32{1}})
	b, _ := s.InsertMemory(&Memory{Category: "fact", Content: "b", Importance: 0.5, Embedding: []float32{1}})

	created, err := s.AddLink(a, b, 0.7)
	if err != nil || !created {
		t.Fatalf("AddLink: created=%v err=%v", created, err)
	}
	created, err = s.AddLink(a, b, 0.9)
	if err != nil || created {
		t.Fatalf("AddLink should update, not create: created=%v err=%v", created, err)
	}
	links, err := s.Links(a)
	if err != nil || len(links) != 1 || links[0].Similarity != 0.9 {
		t.Fatalf("unexpected links: %+v err=%v", links, err)
	}
	if err := s.DeleteMemory(b); err != nil {
		t.Fatal(err)
	}
	links, err = s.Links(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("links should cascade, got %+v", links)
	}
}

func TestMetaAndCounts(t *testing.T) {
	s := openTemp(t)
	if err := s.SetMeta("embedder", "test"); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetMeta("embedder")
	if err != nil || v != "test" {
		t.Fatalf("got %q err=%v", v, err)
	}
	if v, err := s.GetMeta("missing"); err != nil || v != "" {
		t.Fatalf("missing key: got %q err=%v", v, err)
	}
	if _, err := s.InsertMemory(&Memory{Category: "fact", Content: "x", Importance: 0.5, Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	memories, archived, links, err := s.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if memories != 1 || archived != 0 || links != 0 {
		t.Fatalf("unexpected counts: %d %d %d", memories, archived, links)
	}
}

func TestListSinceAndTouch(t *testing.T) {
	s := openTemp(t)
	id, err := s.InsertMemory(&Memory{Category: "fact", Content: "x", Importance: 0.5, Embedding: []float32{1}})
	if err != nil {
		t.Fatal(err)
	}
	mems, err := s.ListSince(time.Now().Add(-time.Hour), 10)
	if err != nil || len(mems) != 1 {
		t.Fatalf("ListSince: %d err=%v", len(mems), err)
	}
	if err := s.Touch([]int64{id}); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMemory(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Access != 1 {
		t.Fatalf("access count = %d, want 1", m.Access)
	}
}
