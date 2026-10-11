package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeBackend struct {
	overview      Overview
	recent        []MemoryItem
	search        []MemoryItem
	overviewErr   error
	recentErr     error
	searchErr     error
	recentLimit   int
	searchLimit   int
	searchQuery   string
	overviewCalls int
	recentCalls   int
	searchCalls   int
}

func (backend *fakeBackend) LoadOverview(context.Context) (Overview, error) {
	backend.overviewCalls++
	return backend.overview, backend.overviewErr
}

func (backend *fakeBackend) ListRecent(_ context.Context, limit int) ([]MemoryItem, error) {
	backend.recentCalls++
	backend.recentLimit = limit
	return backend.recent, backend.recentErr
}

func (backend *fakeBackend) Search(_ context.Context, query string, limit int) ([]MemoryItem, error) {
	backend.searchCalls++
	backend.searchQuery = query
	backend.searchLimit = limit
	return backend.search, backend.searchErr
}

func applyCommand(t *testing.T, model Model, command tea.Cmd) Model {
	t.Helper()
	if command == nil {
		return model
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		for _, item := range batch {
			model = applyCommand(t, model, item)
		}
		return model
	}
	updated, followup := model.Update(message)
	model = updated.(Model)
	return applyCommand(t, model, followup)
}

func sampleMemories() []MemoryItem {
	score := 0.91
	return []MemoryItem{
		{ID: "memory-1", Type: "semantic", StorageTier: "vector", Content: "First durable memory", SourceType: "user_input", UpdatedAt: time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC), Pinned: true, Score: &score},
		{ID: "memory-2", Type: "procedural", StorageTier: "markdown", Content: "Second durable memory", SourceType: "agent_observation", UpdatedAt: time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)},
	}
}

func TestOverviewAndBrowseLoadAsynchronously(t *testing.T) {
	backend := &fakeBackend{
		overview: Overview{Workspace: "agent-memory", Total: 2, Pinned: 1, Diagrams: 0, TypeCounts: map[string]int{"semantic": 1, "procedural": 1}, TierCounts: map[string]int{"vector": 1, "markdown": 1}, UpdatedAt: time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC)},
		recent:   sampleMemories(),
	}
	model := NewModel("agent-memory", backend)
	if !model.overviewLoading || !model.browseLoading {
		t.Fatalf("initial loading state missing: %+v", model)
	}
	model = applyCommand(t, model, model.Init())
	if model.overviewLoading || model.browseLoading || model.overview.Total != 2 || len(model.browseItems) != 2 {
		t.Fatalf("loaded model=%+v", model)
	}
	if backend.recentLimit != browseLimit {
		t.Fatalf("recent limit=%d want=%d", backend.recentLimit, browseLimit)
	}

	model = resize(model, 100, 28)
	home := ansi.Strip(model.View().Content)
	for _, want := range []string{"Memories", "2", "Pinned", "1", "semantic", "procedural", "vector", "markdown"} {
		if !strings.Contains(home, want) {
			t.Fatalf("home missing %q:\n%s", want, home)
		}
	}
	model.destination = DestinationBrowse
	browse := ansi.Strip(model.View().Content)
	for _, want := range []string{"First durable memory", "Second durable memory", "semantic", "procedural"} {
		if !strings.Contains(browse, want) {
			t.Fatalf("browse missing %q:\n%s", want, browse)
		}
	}
}

func TestBrowseNavigationOpensAndClosesDetail(t *testing.T) {
	model := resize(NewModel("agent-memory", nil), 100, 28)
	model.destination = DestinationBrowse
	model.browseItems = sampleMemories()
	model, _ = updateKey(model, press(tea.KeyDown, ""))
	if model.browseCursor != 1 {
		t.Fatalf("browse cursor=%d", model.browseCursor)
	}
	model, _ = updateKey(model, press(tea.KeyEnter, ""))
	if model.detail == nil || model.detail.ID != "memory-2" {
		t.Fatalf("detail=%+v", model.detail)
	}
	plain := ansi.Strip(model.View().Content)
	for _, want := range []string{"Memory detail", "memory-2", "Second durable memory", "agent_observation"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("detail missing %q:\n%s", want, plain)
		}
	}
	model, _ = updateKey(model, press(tea.KeyEsc, ""))
	if model.detail != nil || model.destination != DestinationBrowse {
		t.Fatalf("escape did not return to browse: %+v", model)
	}
}

func TestDetailContentScrollsWithinTheViewport(t *testing.T) {
	lines := make([]string, 30)
	for index := range lines {
		lines[index] = "content line " + string(rune('A'+index))
	}
	item := MemoryItem{ID: "long-memory", Type: "semantic", StorageTier: "vector", Content: strings.Join(lines, "\n")}
	model := resize(NewModel("agent-memory", nil), 80, 16)
	model.detail = &item
	before := ansi.Strip(model.View().Content)
	for range 5 {
		model, _ = updateKey(model, press(tea.KeyDown, ""))
	}
	after := ansi.Strip(model.View().Content)
	if before == after || strings.Contains(after, "content line A") {
		t.Fatalf("detail did not scroll:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(after, "content line F") {
		t.Fatalf("scrolled detail missing later content:\n%s", after)
	}
}

func TestAsyncErrorsRenderAndRefreshStartsANewGeneration(t *testing.T) {
	backend := &fakeBackend{overviewErr: errors.New("overview unavailable"), recentErr: errors.New("database busy")}
	model := applyCommand(t, NewModel("agent-memory", backend), NewModel("agent-memory", backend).Init())
	model = resize(model, 100, 24)
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "overview unavailable") {
		t.Fatalf("overview error not rendered:\n%s", plain)
	}
	model.destination = DestinationBrowse
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "database busy") {
		t.Fatalf("browse error not rendered:\n%s", plain)
	}
	previous := model.browseGeneration
	model, command := updateKey(model, press('r', "r"))
	if command == nil || !model.browseLoading || model.browseGeneration != previous+1 {
		t.Fatalf("refresh did not start a new load: %+v command=%v", model, command)
	}
}

func TestStaleBrowseResultDoesNotReplaceCurrentData(t *testing.T) {
	model := NewModel("agent-memory", nil)
	model.browseGeneration = 3
	model.browseItems = sampleMemories()
	updated, _ := model.Update(recentLoadedMsg{generation: 2, items: []MemoryItem{{ID: "stale"}}})
	model = updated.(Model)
	if len(model.browseItems) != 2 || model.browseItems[0].ID != "memory-1" {
		t.Fatalf("stale result replaced browse data: %+v", model.browseItems)
	}
}

func TestSearchTrimsQueryLoadsRankedResultsAndOpensDetail(t *testing.T) {
	backend := &fakeBackend{search: sampleMemories()}
	model := resize(NewModel("agent-memory", backend), 100, 28)
	model, _ = updateKey(model, press('/', "/"))
	for _, character := range []rune{' ', ' ', '架', '構', ' ', ' '} {
		model, _ = updateKey(model, press(character, string(character)))
	}
	model, command := updateKey(model, press(tea.KeyEnter, ""))
	if command == nil || !model.searchLoading || model.searchFocused {
		t.Fatalf("search did not start: %+v command=%v", model, command)
	}
	model = applyCommand(t, model, command)
	if backend.searchQuery != "架構" || backend.searchLimit != searchLimit || len(model.searchItems) != 2 {
		t.Fatalf("search backend=%+v model=%+v", backend, model)
	}
	plain := ansi.Strip(model.View().Content)
	for _, want := range []string{"First durable memory", "0.910", "Second durable memory"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("search view missing %q:\n%s", want, plain)
		}
	}
	model, _ = updateKey(model, press(tea.KeyDown, ""))
	model, _ = updateKey(model, press(tea.KeyEnter, ""))
	if model.detail == nil || model.detail.ID != "memory-2" || model.detailParent != DestinationSearch {
		t.Fatalf("search detail=%+v parent=%q", model.detail, model.detailParent)
	}
	model, _ = updateKey(model, press(tea.KeyEsc, ""))
	if model.detail != nil || model.destination != DestinationSearch {
		t.Fatalf("escape did not return to search: %+v", model)
	}
}

func TestSearchRejectsBlankQueryWithoutStartingIO(t *testing.T) {
	backend := &fakeBackend{}
	model := NewModel("agent-memory", backend)
	model.destination = DestinationSearch
	model.searchFocused = true
	model.query = "   "
	model, command := updateKey(model, press(tea.KeyEnter, ""))
	if command != nil || model.searchLoading || !model.searchFocused || backend.searchCalls != 0 {
		t.Fatalf("blank search changed state: %+v command=%v calls=%d", model, command, backend.searchCalls)
	}
}

func TestSearchErrorAndStaleResultHandling(t *testing.T) {
	backend := &fakeBackend{searchErr: errors.New("embedding unavailable")}
	model := resize(NewModel("agent-memory", backend), 100, 24)
	model.destination = DestinationSearch
	model.query = "database"
	model.searchFocused = true
	model, command := updateKey(model, press(tea.KeyEnter, ""))
	model = applyCommand(t, model, command)
	if model.searchError != "embedding unavailable" || model.searchLoading {
		t.Fatalf("search error state=%+v", model)
	}
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "embedding unavailable") || !strings.Contains(plain, "Press r to retry") {
		t.Fatalf("search error not rendered:\n%s", plain)
	}

	model.searchGeneration = 4
	model.searchItems = sampleMemories()
	updated, _ := model.Update(searchLoadedMsg{generation: 3, items: []MemoryItem{{ID: "stale"}}})
	model = updated.(Model)
	if len(model.searchItems) != 2 || model.searchItems[0].ID != "memory-1" {
		t.Fatalf("stale search replaced data: %+v", model.searchItems)
	}
}
