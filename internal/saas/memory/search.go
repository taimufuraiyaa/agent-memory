package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

const (
	defaultSearchLimit  = 20
	maximumSearchLimit  = 200
	maximumSearchQuery  = 4000
	maximumSearchCursor = 2048
)

var (
	ErrSearchForbidden = errors.New("memory search is forbidden")
	ErrInvalidSearch   = errors.New("memory search input is invalid")
)

type SearchCommand struct {
	WorkspaceID string
	Query       string
	Limit       int
	Cursor      string
}

type SearchPosition struct {
	Score     float64
	UpdatedAt time.Time
	ID        string
}

type SearchQuery struct {
	WorkspaceID string
	Text        string
	Limit       int
	After       *SearchPosition
}

type SearchItem struct {
	ID          string           `json:"id"`
	WorkspaceID string           `json:"workspace_id"`
	Type        core.MemoryType  `json:"type"`
	Content     string           `json:"content"`
	SourceKind  core.SourceType  `json:"source_kind"`
	Entities    []string         `json:"entities"`
	Tags        []string         `json:"tags"`
	Keywords    []string         `json:"keywords"`
	Outcome     *core.Outcome    `json:"outcome,omitempty"`
	Confidence  float64          `json:"confidence"`
	StorageTier core.StorageTier `json:"storage_tier"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	Score       float64          `json:"score"`
}

type SearchRow struct {
	Item  SearchItem
	Score float64
}

type SearchResult struct {
	Items      []SearchItem `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type SearchRepository interface {
	SearchMemories(context.Context, string, SearchQuery) ([]SearchRow, error)
}

type SearchService struct {
	repository SearchRepository
}

func NewSearchService(repository SearchRepository) *SearchService {
	return &SearchService{repository: repository}
}

func (s *SearchService) Search(ctx context.Context, command SearchCommand) (SearchResult, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || request.TenantID == "" || request.AccountID == "" || !request.Can("memory:read") {
		return SearchResult{}, ErrSearchForbidden
	}
	if s == nil || s.repository == nil {
		return SearchResult{}, errors.New("hosted memory search repository is not configured")
	}
	workspaceID := strings.TrimSpace(command.WorkspaceID)
	query := strings.Join(strings.Fields(command.Query), " ")
	if _, err := uuid.Parse(workspaceID); err != nil || query == "" || !utf8.ValidString(query) || utf8.RuneCountInString(query) > maximumSearchQuery {
		return SearchResult{}, ErrInvalidSearch
	}
	limit := command.Limit
	if limit == 0 {
		limit = defaultSearchLimit
	}
	if limit < 1 || limit > maximumSearchLimit {
		return SearchResult{}, ErrInvalidSearch
	}
	position, err := decodeSearchCursor(strings.TrimSpace(command.Cursor), searchBinding(workspaceID, query))
	if err != nil {
		return SearchResult{}, ErrInvalidSearch
	}
	rows, err := s.repository.SearchMemories(ctx, request.TenantID, SearchQuery{
		WorkspaceID: workspaceID, Text: query, Limit: limit + 1, After: position,
	})
	if err != nil {
		return SearchResult{}, err
	}
	if len(rows) > limit+1 {
		return SearchResult{}, errors.New("hosted memory search repository exceeded its result bound")
	}
	result := SearchResult{Items: make([]SearchItem, 0, min(len(rows), limit))}
	for index, row := range rows {
		if index == limit {
			break
		}
		if !validSearchRow(row, workspaceID) {
			return SearchResult{}, errors.New("hosted memory search repository returned an invalid row")
		}
		row.Item.Score = row.Score
		result.Items = append(result.Items, row.Item)
	}
	if len(rows) > limit {
		last := result.Items[len(result.Items)-1]
		result.NextCursor, err = encodeSearchCursor(searchCursor{
			Version: 1, Binding: searchBinding(workspaceID, query), Score: last.Score,
			UpdatedAt: last.UpdatedAt.UTC(), ID: last.ID,
		})
		if err != nil {
			return SearchResult{}, err
		}
	}
	return result, nil
}

type searchCursor struct {
	Version   int       `json:"v"`
	Binding   string    `json:"binding"`
	Score     float64   `json:"score"`
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
}

func searchBinding(workspaceID, query string) string {
	digest := sha256.Sum256([]byte(workspaceID + "\x00" + strings.ToLower(query)))
	return hex.EncodeToString(digest[:])
}

func decodeSearchCursor(value, binding string) (*SearchPosition, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > maximumSearchCursor {
		return nil, ErrInvalidSearch
	}
	contents, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(contents) == 0 || len(contents) > maximumSearchCursor {
		return nil, ErrInvalidSearch
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var cursor searchCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, ErrInvalidSearch
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidSearch
	}
	if cursor.Version != 1 || cursor.Binding != binding || !validPosition(cursor.Score, cursor.UpdatedAt, cursor.ID) {
		return nil, ErrInvalidSearch
	}
	return &SearchPosition{Score: cursor.Score, UpdatedAt: cursor.UpdatedAt.UTC(), ID: cursor.ID}, nil
}

func encodeSearchCursor(cursor searchCursor) (string, error) {
	if cursor.Version != 1 || len(cursor.Binding) != 64 || !validPosition(cursor.Score, cursor.UpdatedAt, cursor.ID) {
		return "", errors.New("memory search cursor state is invalid")
	}
	contents, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(contents)
	if len(encoded) > maximumSearchCursor {
		return "", errors.New("memory search cursor is too large")
	}
	return encoded, nil
}

func validPosition(score float64, updatedAt time.Time, id string) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && !updatedAt.IsZero() && uuid.Validate(id) == nil
}

func validSearchRow(row SearchRow, workspaceID string) bool {
	return row.Item.WorkspaceID == workspaceID && validPosition(row.Score, row.Item.UpdatedAt, row.Item.ID) &&
		!row.Item.CreatedAt.IsZero() && validMemoryType(row.Item.Type) && validSource(row.Item.SourceKind) && validStorageTier(row.Item.StorageTier)
}

func validMemoryType(value core.MemoryType) bool {
	switch value {
	case core.EpisodicMemory, core.SemanticMemory, core.ProceduralMemory, core.OutcomeMemory:
		return true
	default:
		return false
	}
}

func validStorageTier(value core.StorageTier) bool {
	switch value {
	case core.TierMarkdown, core.TierVector, core.TierVectorGraph, core.TierDocument, core.TierCold:
		return true
	default:
		return false
	}
}
