// Package store owns the SQLite database: the durable source of truth for
// memories, links, and metadata. Vectors live here too, so the database file is
// a complete backup; the in-memory vector index is rebuilt from it at startup.
package store

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a memory does not exist.
var ErrNotFound = errors.New("memory not found")

// Store is a handle to the SQLite database.
type Store struct {
	db   *sql.DB
	path string
}

// Memory is one stored memory.
type Memory struct {
	ID         int64
	Category   string
	Content    string
	Importance float64
	Tags       []string
	Source     string
	Session    string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastAccess time.Time
	Access     int
	Archived   bool
	Embedding  []float32

	// Strength is computed on load, never stored.
	Strength float64 `json:"-"`
	Degree   int     `json:"-"`
}

// Link is a directed edge between two memories. Links are stored in both
// directions, so a link always has a mirror.
type Link struct {
	Src        int64
	Dst        int64
	Similarity float64
	CreatedAt  time.Time
}

// Filter narrows a memory listing.
type Filter struct {
	Category        string
	IncludeArchived bool
	Limit           int
	Offset          int
}

// Scored is a search hit with a relevance score.
type Scored struct {
	ID    int64
	Score float64
}

// Open opens or creates the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps a CLI process free of lock contention and makes
	// transactions deterministic.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// schema is the full DDL. Every statement is idempotent.
const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memories (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	category    TEXT    NOT NULL DEFAULT 'fact',
	content     TEXT    NOT NULL,
	importance  REAL    NOT NULL DEFAULT 0.5,
	tags        TEXT    NOT NULL DEFAULT '',
	source      TEXT    NOT NULL DEFAULT '',
	session     TEXT    NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL,
	last_access INTEGER NOT NULL,
	access_count INTEGER NOT NULL DEFAULT 0,
	archived    INTEGER NOT NULL DEFAULT 0,
	embedding   BLOB    NOT NULL,
	dims        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memories_category ON memories(category);
CREATE INDEX IF NOT EXISTS idx_memories_archived ON memories(archived);

CREATE TABLE IF NOT EXISTS links (
	src        INTEGER NOT NULL,
	dst        INTEGER NOT NULL,
	similarity REAL    NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (src, dst),
	FOREIGN KEY (src) REFERENCES memories(id) ON DELETE CASCADE,
	FOREIGN KEY (dst) REFERENCES memories(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_links_dst ON links(dst);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
	content,
	tags,
	content='memories',
	content_rowid='id',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
	INSERT INTO memories_fts(rowid, content, tags) VALUES (new.id, new.content, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
	INSERT INTO memories_fts(memories_fts, rowid, content, tags) VALUES ('delete', old.id, old.content, old.tags);
END;

CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
	INSERT INTO memories_fts(memories_fts, rowid, content, tags) VALUES ('delete', old.id, old.content, old.tags);
	INSERT INTO memories_fts(rowid, content, tags) VALUES (new.id, new.content, new.tags);
END;
`

// migrate creates the schema and keeps the FTS index in sync with existing rows.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Older databases (or a database created before the FTS triggers) need a
	// one-time rebuild. The marker makes that a no-op afterwards.
	var built string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'fts_version'`).Scan(&built)
	if errors.Is(err, sql.ErrNoRows) || built != "1" {
		if _, err := s.db.Exec(`INSERT INTO memories_fts(memories_fts) VALUES ('rebuild')`); err != nil {
			return fmt.Errorf("rebuild fts: %w", err)
		}
		if err := s.SetMeta("fts_version", "1"); err != nil {
			return err
		}
	}
	return nil
}

// GetMeta reads a metadata value; a missing key returns "".
func (s *Store) GetMeta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta writes a metadata value.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// InsertMemory stores a new memory and returns its id.
func (s *Store) InsertMemory(m *Memory) (int64, error) {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	if m.LastAccess.IsZero() {
		m.LastAccess = m.CreatedAt
	}
	blob, dims := encodeVector(m.Embedding)
	res, err := s.db.Exec(`INSERT INTO memories
		(category, content, importance, tags, source, session,
		 created_at, updated_at, last_access, access_count, archived, embedding, dims)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		m.Category, m.Content, m.Importance, strings.Join(m.Tags, ","),
		m.Source, m.Session, m.CreatedAt.UnixNano(), m.UpdatedAt.UnixNano(),
		m.LastAccess.UnixNano(), m.Access, blob, dims)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	m.ID = id
	return id, nil
}

// GetMemory loads one memory by id.
func (s *Store) GetMemory(id int64) (*Memory, error) {
	row := s.db.QueryRow(`SELECT id, category, content, importance, tags,
		source, session, created_at, updated_at, last_access, access_count, archived, embedding
		FROM memories WHERE id = ?`, id)
	m, err := scanMemory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: #%d", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// UpdateMemory writes the mutable fields of an existing memory.
func (s *Store) UpdateMemory(m *Memory) error {
	blob, dims := encodeVector(m.Embedding)
	_, err := s.db.Exec(`UPDATE memories SET
		category = ?, content = ?, importance = ?, tags = ?,
		source = ?, session = ?, updated_at = ?, last_access = ?,
		access_count = ?, archived = ?, embedding = ?, dims = ?
		WHERE id = ?`,
		m.Category, m.Content, m.Importance, strings.Join(m.Tags, ","),
		m.Source, m.Session, m.UpdatedAt.UnixNano(), m.LastAccess.UnixNano(),
		m.Access, boolInt(m.Archived), blob, dims, m.ID)
	return err
}

// DeleteMemory removes a memory; links cascade.
func (s *Store) DeleteMemory(id int64) error {
	res, err := s.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: #%d", ErrNotFound, id)
	}
	return nil
}

// SetArchived toggles the archived flag.
func (s *Store) SetArchived(id int64, archived bool) error {
	_, err := s.db.Exec(`UPDATE memories SET archived = ?, updated_at = ? WHERE id = ?`,
		boolInt(archived), time.Now().UTC().UnixNano(), id)
	return err
}

// SetEmbedding replaces the stored vector.
func (s *Store) SetEmbedding(id int64, vec []float32) error {
	blob, dims := encodeVector(vec)
	_, err := s.db.Exec(`UPDATE memories SET embedding = ?, dims = ? WHERE id = ?`, blob, dims, id)
	return err
}

// Touch records an access: it bumps the counter and the last-access time.
func (s *Store) Touch(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC().UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE memories SET access_count = access_count + 1, last_access = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(now, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BumpUpdated marks memories as updated, which is how a link reinforces an
// existing memory ("memory evolution").
func (s *Store) BumpUpdated(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC().UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE memories SET updated_at = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(now, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// List returns memories matching filter, newest update first.
func (s *Store) List(f Filter) ([]*Memory, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	var sb strings.Builder
	sb.WriteString(`SELECT id, category, content, importance, tags, source,
		session, created_at, updated_at, last_access, access_count, archived, embedding
		FROM memories WHERE 1 = 1`)
	args := []any{}
	if f.Category != "" {
		sb.WriteString(` AND category = ?`)
		args = append(args, f.Category)
	}
	if !f.IncludeArchived {
		sb.WriteString(` AND archived = 0`)
	}
	sb.WriteString(` ORDER BY updated_at DESC LIMIT ? OFFSET ?`)
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListSince returns live memories updated at or after since, newest first.
func (s *Store) ListSince(since time.Time, limit int) ([]*Memory, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, category, content, importance, tags, source,
		session, created_at, updated_at, last_access, access_count, archived, embedding
		FROM memories WHERE archived = 0 AND updated_at >= ?`
	args := []any{since.UnixNano()}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllForIndex returns every live memory with its stored vector. It is the input
// for rebuilding the in-memory vector index.
func (s *Store) AllForIndex() ([]*Memory, error) {
	rows, err := s.db.Query(`SELECT id, category, content, importance, tags,
		source, session, created_at, updated_at, last_access, access_count, archived, embedding
		FROM memories WHERE archived = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SearchFTS runs a keyword search over content and tags. The query must already
// be valid FTS5 syntax.
func (s *Store) SearchFTS(query string, limit int, category string) ([]Scored, error) {
	if limit <= 0 {
		limit = 20
	}
	var sb strings.Builder
	sb.WriteString(`SELECT m.id, bm25(memories_fts) AS rank
		FROM memories_fts JOIN memories m ON m.id = memories_fts.rowid
		WHERE memories_fts MATCH ? AND m.archived = 0`)
	args := []any{query}
	if category != "" {
		sb.WriteString(` AND m.category = ?`)
		args = append(args, category)
	}
	sb.WriteString(` ORDER BY rank LIMIT ?`)
	args = append(args, limit)
	rows, err := s.db.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Scored
	for rows.Next() {
		var sc Scored
		var rank float64
		if err := rows.Scan(&sc.ID, &rank); err != nil {
			return nil, err
		}
		sc.Score = -rank
		out = append(out, sc)
	}
	return out, rows.Err()
}

// AddLink inserts a directed link. It reports whether the link was new and
// raises the similarity if the link already existed.
func (s *Store) AddLink(src, dst int64, similarity float64) (bool, error) {
	var existing float64
	err := s.db.QueryRow(`SELECT similarity FROM links WHERE src = ? AND dst = ?`, src, dst).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.Exec(`INSERT INTO links(src, dst, similarity, created_at) VALUES (?, ?, ?, ?)`,
			src, dst, similarity, time.Now().UTC().UnixNano())
		return true, err
	case err != nil:
		return false, err
	case similarity > existing:
		_, err = s.db.Exec(`UPDATE links SET similarity = ? WHERE src = ? AND dst = ?`, similarity, src, dst)
		return false, err
	}
	return false, nil
}

// DeleteLink removes a directed link.
func (s *Store) DeleteLink(src, dst int64) error {
	_, err := s.db.Exec(`DELETE FROM links WHERE src = ? AND dst = ?`, src, dst)
	return err
}

// Links returns the outgoing links of a memory.
func (s *Store) Links(id int64) ([]Link, error) {
	rows, err := s.db.Query(`SELECT src, dst, similarity, created_at FROM links WHERE src = ? ORDER BY similarity DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLinks(rows)
}

// AllLinks returns every link in the graph.
func (s *Store) AllLinks() ([]Link, error) {
	rows, err := s.db.Query(`SELECT src, dst, similarity, created_at FROM links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLinks(rows)
}

// Degrees returns the number of outgoing links per memory.
func (s *Store) Degrees() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT src, COUNT(*) FROM links GROUP BY src`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Degree returns the outgoing link count of one memory.
func (s *Store) Degree(id int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE src = ?`, id).Scan(&n)
	return n, err
}

// Counts returns totals used by stats and health reports.
func (s *Store) Counts() (memories, archived, links int64, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE archived = 0`).Scan(&memories); err != nil {
		return
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE archived = 1`).Scan(&archived); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM links`).Scan(&links)
	return
}

// CategoryCounts returns live memory counts per category.
func (s *Store) CategoryCounts() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT category, COUNT(*) FROM memories WHERE archived = 0 GROUP BY category ORDER BY 2 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// MaxUpdated returns the most recent update time, or the zero time.
func (s *Store) MaxUpdated() (time.Time, error) {
	var ns sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(updated_at) FROM memories WHERE archived = 0`).Scan(&ns); err != nil {
		return time.Time{}, err
	}
	if !ns.Valid {
		return time.Time{}, nil
	}
	return time.Unix(0, ns.Int64).UTC(), nil
}

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanMemory reads the standard memory column list.
func scanMemory(sc scanner) (*Memory, error) {
	var m Memory
	var tags string
	var created, updated, access int64
	var archived int
	var blob []byte
	if err := sc.Scan(&m.ID, &m.Category, &m.Content, &m.Importance,
		&tags, &m.Source, &m.Session, &created, &updated, &access,
		&m.Access, &archived, &blob); err != nil {
		return nil, err
	}
	m.Tags = splitTags(tags)
	m.CreatedAt = time.Unix(0, created).UTC()
	m.UpdatedAt = time.Unix(0, updated).UTC()
	m.LastAccess = time.Unix(0, access).UTC()
	m.Archived = archived != 0
	m.Embedding = decodeVector(blob)
	return &m, nil
}

// scanLinks reads link rows.
func scanLinks(rows *sql.Rows) ([]Link, error) {
	var out []Link
	for rows.Next() {
		var l Link
		var created int64
		if err := rows.Scan(&l.Src, &l.Dst, &l.Similarity, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = time.Unix(0, created).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// splitTags parses the comma-separated tag column.
func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// encodeVector packs a float32 vector into little-endian bytes.
func encodeVector(vec []float32) ([]byte, int) {
	blob := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(v))
	}
	return blob, len(vec)
}

// decodeVector unpacks little-endian float32 bytes.
func decodeVector(blob []byte) []float32 {
	if len(blob)%4 != 0 {
		return nil
	}
	out := make([]float32, len(blob)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out
}

// boolInt renders a bool as SQLite's 0/1.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
