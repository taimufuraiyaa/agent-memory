package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	modernc "modernc.org/sqlite"

	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

const (
	// LexicalMaxQueryTerms bounds the number of quoted terms in one FTS5 query.
	LexicalMaxQueryTerms = 8
	// LexicalMaxLimit bounds rows returned by one lexical query.
	LexicalMaxLimit = 500

	lexicalTokenizer = `tokenize = "porter unicode61 remove_diacritics 2 tokenchars '_'"`
	// bm25 column weights: content, meta, then the unindexed filter column.
	memoryBM25Args  = "memory_lexical_fts, 1.0, 0.3, 0.0"
	passageBM25Args = "passage_lexical_fts, 1.0, 0.0"
	lexicalInChunk  = 400
	// lexicalMaxFetch bounds the candidate rows fetched before canonical filtering.
	lexicalMaxFetch = 4000
)

var lexicalStopTerms = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {}, "do": {}, "does": {},
	"for": {}, "from": {}, "has": {}, "have": {}, "how": {}, "in": {}, "is": {}, "it": {}, "its": {},
	"of": {}, "on": {}, "or": {}, "that": {}, "the": {}, "this": {}, "to": {}, "was": {}, "were": {},
	"what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {}, "with": {},
}

// LexicalQuery selects memories by BM25 relevance within one workspace.
type LexicalQuery struct {
	Workspace string
	Text      string
	Limit     int
}

// LexicalMatch is one ranked lexical memory candidate. Score is positive and
// higher is better; Rank is one-based.
type LexicalMatch struct {
	MemoryID string
	Score    float64
	Rank     int
}

// LexicalIndexStatus reports index coverage for drift detection.
type LexicalIndexStatus struct {
	Available       bool  `json:"available"`
	Memories        int64 `json:"memories"`
	IndexedMemories int64 `json:"indexed_memories"`
	Passages        int64 `json:"passages"`
	IndexedPassages int64 `json:"indexed_passages"`
}

// Drifted reports whether any canonical row lacks an index row or vice versa.
func (s LexicalIndexStatus) Drifted() bool {
	return s.Available && (s.Memories != s.IndexedMemories || s.Passages != s.IndexedPassages)
}

// LexicalTerms reduces free text to bounded, deduplicated search tokens. Only
// letters, digits and underscore survive, so the result is safe to quote.
func LexicalTerms(text string, maxTerms int) []string {
	if maxTerms <= 0 || maxTerms > LexicalMaxQueryTerms {
		maxTerms = LexicalMaxQueryTerms
	}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len([]rune(field)) < 2 {
			continue
		}
		if _, stop := lexicalStopTerms[field]; stop {
			continue
		}
		if _, dup := seen[field]; dup {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
		if len(out) >= maxTerms {
			break
		}
	}
	return out
}

func lexicalMatchExpression(text string) string {
	terms := LexicalTerms(text, LexicalMaxQueryTerms)
	if len(terms) == 0 {
		return ""
	}
	quoted := make([]string, len(terms))
	for i, term := range terms {
		quoted[i] = `"` + term + `"`
	}
	return strings.Join(quoted, " OR ")
}

// migrateLexicalIndexes creates the memory and passage FTS5 indexes, their
// maintenance triggers and a one-pass backfill. Missing FTS5 support is not an
// error: the database stays fully usable without lexical ranking.
func migrateLexicalIndexes(ctx context.Context, s *Store) error {
	ok, err := s.createLexicalSchema(ctx)
	if err != nil {
		return err
	}
	if !ok {
		s.migrationAdvisories = append(s.migrationAdvisories, "lexical index skipped: FTS5 module unavailable")
	}
	return nil
}

func (s *Store) createLexicalSchema(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	ddl := []string{
		`CREATE TABLE IF NOT EXISTS memory_lexical_docs (
			doc_id INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id TEXT NOT NULL UNIQUE,
			workspace TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_lexical_docs_workspace ON memory_lexical_docs(workspace)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_lexical_fts USING fts5(content, meta, workspace UNINDEXED, ` + lexicalTokenizer + `)`,
		`CREATE TABLE IF NOT EXISTS passage_lexical_docs (
			doc_id INTEGER PRIMARY KEY AUTOINCREMENT,
			passage_id TEXT NOT NULL UNIQUE,
			edition_id TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_passage_lexical_docs_edition ON passage_lexical_docs(edition_id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS passage_lexical_fts USING fts5(text, edition_id UNINDEXED, ` + lexicalTokenizer + `)`,
	}
	for _, statement := range ddl {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "fts5") {
				return false, nil
			}
			return false, fmt.Errorf("create lexical schema: %w", err)
		}
	}
	if err := backfillLexicalTx(ctx, tx); err != nil {
		return false, err
	}
	for _, statement := range lexicalTriggers {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return false, fmt.Errorf("create lexical trigger: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit lexical schema: %w", err)
	}
	s.lexicalState.Store(lexicalAvailable)
	return true, nil
}

func backfillLexicalTx(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`INSERT OR IGNORE INTO memory_lexical_docs(memory_id, workspace) SELECT id, workspace FROM memories`,
		`INSERT INTO memory_lexical_fts(rowid, content, meta, workspace)
			SELECT d.doc_id, m.content || char(10) || COALESCE(m.diagram_code, ''), COALESCE(m.tags_json, '') || ' ' || COALESCE(m.entities_json, ''), m.workspace
			FROM memory_lexical_docs d JOIN memories m ON m.id = d.memory_id
			WHERE d.doc_id NOT IN (SELECT rowid FROM memory_lexical_fts)`,
		`INSERT OR IGNORE INTO passage_lexical_docs(passage_id, edition_id) SELECT id, edition_id FROM source_passages`,
		`INSERT INTO passage_lexical_fts(rowid, text, edition_id)
			SELECT d.doc_id, p.text, p.edition_id FROM passage_lexical_docs d JOIN source_passages p ON p.id = d.passage_id
			WHERE d.doc_id NOT IN (SELECT rowid FROM passage_lexical_fts)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("backfill lexical index: %w", err)
		}
	}
	return nil
}

var lexicalTriggers = []string{
	`CREATE TRIGGER IF NOT EXISTS trg_memory_lexical_insert AFTER INSERT ON memories
	BEGIN
		INSERT OR IGNORE INTO memory_lexical_docs(memory_id, workspace) VALUES (NEW.id, NEW.workspace);
		INSERT OR REPLACE INTO memory_lexical_fts(rowid, content, meta, workspace)
		SELECT doc_id, NEW.content || char(10) || COALESCE(NEW.diagram_code, ''), COALESCE(NEW.tags_json, '') || ' ' || COALESCE(NEW.entities_json, ''), NEW.workspace
		FROM memory_lexical_docs WHERE memory_id = NEW.id;
	END`,
	`CREATE TRIGGER IF NOT EXISTS trg_memory_lexical_update AFTER UPDATE OF content, diagram_code, tags_json, entities_json ON memories
	BEGIN
		UPDATE memory_lexical_fts
		SET content = NEW.content || char(10) || COALESCE(NEW.diagram_code, ''), meta = COALESCE(NEW.tags_json, '') || ' ' || COALESCE(NEW.entities_json, '')
		WHERE rowid = (SELECT doc_id FROM memory_lexical_docs WHERE memory_id = NEW.id);
	END`,
	`CREATE TRIGGER IF NOT EXISTS trg_memory_lexical_workspace AFTER UPDATE OF workspace ON memories
	BEGIN
		UPDATE memory_lexical_docs SET workspace = NEW.workspace WHERE memory_id = NEW.id;
		UPDATE memory_lexical_fts SET workspace = NEW.workspace
		WHERE rowid = (SELECT doc_id FROM memory_lexical_docs WHERE memory_id = NEW.id);
	END`,
	`CREATE TRIGGER IF NOT EXISTS trg_memory_lexical_delete AFTER DELETE ON memories
	BEGIN
		DELETE FROM memory_lexical_fts WHERE rowid = (SELECT doc_id FROM memory_lexical_docs WHERE memory_id = OLD.id);
		DELETE FROM memory_lexical_docs WHERE memory_id = OLD.id;
	END`,
	`CREATE TRIGGER IF NOT EXISTS trg_passage_lexical_insert AFTER INSERT ON source_passages
	BEGIN
		INSERT OR IGNORE INTO passage_lexical_docs(passage_id, edition_id) VALUES (NEW.id, NEW.edition_id);
		INSERT OR REPLACE INTO passage_lexical_fts(rowid, text, edition_id)
		SELECT doc_id, NEW.text, NEW.edition_id FROM passage_lexical_docs WHERE passage_id = NEW.id;
	END`,
	`CREATE TRIGGER IF NOT EXISTS trg_passage_lexical_update AFTER UPDATE OF text, edition_id ON source_passages
	BEGIN
		UPDATE passage_lexical_docs SET edition_id = NEW.edition_id WHERE passage_id = NEW.id;
		UPDATE passage_lexical_fts SET text = NEW.text, edition_id = NEW.edition_id
		WHERE rowid = (SELECT doc_id FROM passage_lexical_docs WHERE passage_id = NEW.id);
	END`,
	`CREATE TRIGGER IF NOT EXISTS trg_passage_lexical_delete AFTER DELETE ON source_passages
	BEGIN
		DELETE FROM passage_lexical_fts WHERE rowid = (SELECT doc_id FROM passage_lexical_docs WHERE passage_id = OLD.id);
		DELETE FROM passage_lexical_docs WHERE passage_id = OLD.id;
	END`,
}

const (
	lexicalUnknown int32 = iota
	lexicalAvailable
	lexicalMissing
)

// LexicalIndexAvailable reports whether the FTS5 memory index exists.
func (s *Store) LexicalIndexAvailable(ctx context.Context) bool {
	switch s.lexicalState.Load() {
	case lexicalAvailable:
		return true
	case lexicalMissing:
		return false
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('memory_lexical_fts', 'passage_lexical_fts')`).Scan(&count)
	if err != nil {
		return false
	}
	if count == 2 {
		s.lexicalState.Store(lexicalAvailable)
		return true
	}
	s.lexicalState.Store(lexicalMissing)
	return false
}

// SearchMemoryLexical returns BM25-ranked, non-superseded memories for one
// workspace. Query text never reaches the FTS5 grammar unquoted.
func (s *Store) SearchMemoryLexical(ctx context.Context, query LexicalQuery) ([]LexicalMatch, error) {
	workspace := strings.TrimSpace(query.Workspace)
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	expression := lexicalMatchExpression(query.Text)
	if expression == "" {
		return []LexicalMatch{}, nil
	}
	if !s.LexicalIndexAvailable(ctx) {
		return nil, errors.New("lexical index is not available")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > LexicalMaxLimit {
		limit = LexicalMaxLimit
	}
	ranked, err := s.lexicalTopDocs(ctx, limit,
		`SELECT rowid, bm25(`+memoryBM25Args+`) FROM memory_lexical_fts WHERE memory_lexical_fts MATCH ? AND workspace = ? ORDER BY 2, 1 LIMIT ?`,
		[]any{expression, workspace},
		`SELECT d.doc_id, d.memory_id FROM memory_lexical_docs d JOIN memories m ON m.id = d.memory_id
		 WHERE d.doc_id IN (%s) AND d.workspace = ? AND m.superseded_by IS NULL`,
		[]any{workspace})
	if err != nil {
		return nil, fmt.Errorf("lexical memory search: %w", err)
	}
	out := make([]LexicalMatch, len(ranked))
	for i, row := range ranked {
		out[i] = LexicalMatch{MemoryID: row.key, Score: row.score, Rank: i + 1}
	}
	return out, nil
}

// SearchPassagesLexical returns BM25-ranked passages restricted to the given
// editions. Callers must authorize editions before calling; the edition
// restriction is applied inside the index scan so unauthorized passages never
// compete for result slots.
func (s *Store) SearchPassagesLexical(ctx context.Context, editionIDs []string, text string, limit int) ([]library.PassageMatch, error) {
	expression := lexicalMatchExpression(text)
	if expression == "" || len(editionIDs) == 0 {
		return []library.PassageMatch{}, nil
	}
	if !s.LexicalIndexAvailable(ctx) {
		return nil, errors.New("lexical index is not available")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > LexicalMaxLimit {
		limit = LexicalMaxLimit
	}
	merged := make([]library.PassageMatch, 0, limit)
	for start := 0; start < len(editionIDs); start += lexicalInChunk {
		end := min(start+lexicalInChunk, len(editionIDs))
		chunk := editionIDs[start:end]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := []any{expression}
		for _, id := range chunk {
			args = append(args, id)
		}
		ranked, err := s.lexicalTopDocs(ctx, limit,
			`SELECT rowid, bm25(`+passageBM25Args+`) FROM passage_lexical_fts WHERE passage_lexical_fts MATCH ? AND edition_id IN (`+placeholders+`) ORDER BY 2, 1 LIMIT ?`,
			args,
			`SELECT d.doc_id, d.passage_id FROM passage_lexical_docs d JOIN source_passages p ON p.id = d.passage_id
			 WHERE d.doc_id IN (%s) AND p.source_asset_id NOT IN (SELECT source_asset_id FROM source_deletions)`,
			nil)
		if err != nil {
			return nil, fmt.Errorf("lexical passage search: %w", err)
		}
		for _, row := range ranked {
			merged = append(merged, library.PassageMatch{PassageID: row.key, Score: row.score})
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Score == merged[j].Score {
			return merged[i].PassageID < merged[j].PassageID
		}
		return merged[i].Score > merged[j].Score
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	for i := range merged {
		merged[i].Rank = i + 1
	}
	return merged, nil
}

type lexicalRankedRow struct {
	key   string
	score float64
}

// lexicalTopDocs fetches the best BM25 documents from the index (filters that
// the index can evaluate are part of rankSQL), resolves them against canonical
// rows (resolveSQL carries the canonical filters), and widens the fetch when
// canonical filtering leaves fewer than limit rows while the index still has
// more candidates.
func (s *Store) lexicalTopDocs(ctx context.Context, limit int, rankSQL string, rankArgs []any, resolveSQL string, resolveArgs []any) ([]lexicalRankedRow, error) {
	fetch := max(limit*4, 40)
	for {
		fetch = min(fetch, lexicalMaxFetch)
		rows, err := s.db.QueryContext(ctx, rankSQL, append(append([]any{}, rankArgs...), fetch)...)
		if err != nil {
			return nil, err
		}
		type rankedDoc struct {
			id    int64
			score float64
		}
		var docs []rankedDoc
		for rows.Next() {
			var d rankedDoc
			var raw float64
			if err := rows.Scan(&d.id, &raw); err != nil {
				_ = rows.Close()
				return nil, err
			}
			d.score = -raw
			docs = append(docs, d)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		if len(docs) == 0 {
			return nil, nil
		}
		keys, err := s.lexicalResolve(ctx, docs2ids(docs, func(d rankedDoc) int64 { return d.id }), resolveSQL, resolveArgs)
		if err != nil {
			return nil, err
		}
		out := make([]lexicalRankedRow, 0, limit)
		for _, d := range docs {
			if key, ok := keys[d.id]; ok {
				out = append(out, lexicalRankedRow{key: key, score: d.score})
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].score == out[j].score {
				return out[i].key < out[j].key
			}
			return out[i].score > out[j].score
		})
		saturated := len(docs) >= fetch && fetch < lexicalMaxFetch
		if len(out) >= limit || !saturated {
			if len(out) > limit {
				out = out[:limit]
			}
			return out, nil
		}
		fetch *= 4
	}
}

func docs2ids[T any](items []T, id func(T) int64) []int64 {
	out := make([]int64, len(items))
	for i, item := range items {
		out[i] = id(item)
	}
	return out
}

func (s *Store) lexicalResolve(ctx context.Context, ids []int64, resolveSQL string, extra []any) (map[int64]string, error) {
	keys := make(map[int64]string, len(ids))
	for start := 0; start < len(ids); start += lexicalInChunk {
		end := min(start+lexicalInChunk, len(ids))
		chunk := ids[start:end]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+len(extra))
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, extra...)
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(resolveSQL, placeholders), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var key string
			if err := rows.Scan(&id, &key); err != nil {
				_ = rows.Close()
				return nil, err
			}
			keys[id] = key
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return keys, nil
}

// LexicalIndexStatus compares canonical rows with index rows.
func (s *Store) LexicalIndexStatus(ctx context.Context) (LexicalIndexStatus, error) {
	status := LexicalIndexStatus{Available: s.LexicalIndexAvailable(ctx)}
	counts := []struct {
		query string
		dest  *int64
	}{
		{`SELECT COUNT(*) FROM memories`, &status.Memories},
		{`SELECT COUNT(*) FROM source_passages`, &status.Passages},
	}
	if status.Available {
		counts = append(counts,
			struct {
				query string
				dest  *int64
			}{`SELECT COUNT(*) FROM memory_lexical_docs`, &status.IndexedMemories},
			struct {
				query string
				dest  *int64
			}{`SELECT COUNT(*) FROM passage_lexical_docs`, &status.IndexedPassages},
		)
	}
	for _, c := range counts {
		if err := s.db.QueryRowContext(ctx, c.query).Scan(c.dest); err != nil {
			return status, err
		}
	}
	return status, nil
}

// RebuildLexicalIndex repopulates both indexes from canonical rows.
func (s *Store) RebuildLexicalIndex(ctx context.Context) error {
	if !s.LexicalIndexAvailable(ctx) {
		return errors.New("lexical index is not available")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`DELETE FROM memory_lexical_fts`, `DELETE FROM memory_lexical_docs`,
		`DELETE FROM passage_lexical_fts`, `DELETE FROM passage_lexical_docs`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("clear lexical index: %w", err)
		}
	}
	if err := backfillLexicalTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

var registerLexicalWarmupOnce sync.Once

// registerLexicalConnectionWarmup makes every new SQLite connection load the
// schema, including FTS5 virtual tables, before any transaction begins.
// FTS5 reads its configuration while the schema is parsed; when that first
// happens inside a write transaction the open read snapshot makes the later
// write upgrade fail immediately with SQLITE_BUSY under concurrent writers.
func registerLexicalConnectionWarmup() {
	registerLexicalWarmupOnce.Do(func() {
		modernc.RegisterConnectionHook(func(conn modernc.ExecQuerierContext, _ string) error {
			ctx := context.Background()
			for _, statement := range []string{
				`SELECT 1 FROM sqlite_master LIMIT 1`,
				`SELECT 1 FROM memory_lexical_fts LIMIT 0`,
				`SELECT 1 FROM passage_lexical_fts LIMIT 0`,
			} {
				// Missing tables are expected before migration 32; warmup is best effort.
				if rows, err := conn.(driver.QueryerContext).QueryContext(ctx, statement, nil); err == nil {
					_ = rows.Close()
				}
			}
			return nil
		})
	})
}
