package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type Destination string

const (
	DestinationHome   Destination = "home"
	DestinationSearch Destination = "search"
	DestinationBrowse Destination = "browse"
)

type Overview struct {
	Workspace  string
	Total      int
	Pinned     int
	Diagrams   int
	TypeCounts map[string]int
	TierCounts map[string]int
	UpdatedAt  time.Time
}

type MemoryItem struct {
	ID           string
	Type         string
	StorageTier  string
	Content      string
	SourceType   string
	SourcePath   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastAccessed time.Time
	Pinned       bool
	AccessCount  int
	Score        *float64
	Outcome      *Outcome
}

type Outcome struct {
	Result   string
	Approach string
	Reason   string
}

type Backend interface {
	LoadOverview(context.Context) (Overview, error)
	ListRecent(context.Context, int) ([]MemoryItem, error)
	Search(context.Context, string, int) ([]MemoryItem, error)
}

type Model struct {
	workspace          string
	backend            Backend
	context            context.Context
	destination        Destination
	query              string
	searchFocused      bool
	helpVisible        bool
	width              int
	height             int
	overview           Overview
	overviewLoading    bool
	overviewError      string
	overviewGeneration int
	browseItems        []MemoryItem
	browseCursor       int
	browseLoading      bool
	browseError        string
	browseGeneration   int
	searchItems        []MemoryItem
	searchCursor       int
	searchLoading      bool
	searchError        string
	searchGeneration   int
	lastSearchQuery    string
	detail             *MemoryItem
	detailParent       Destination
	detailScroll       int
}

const browseLimit = 100
const searchLimit = 20

type overviewLoadedMsg struct {
	generation int
	overview   Overview
	err        error
}

type recentLoadedMsg struct {
	generation int
	items      []MemoryItem
	err        error
}

type searchLoadedMsg struct {
	generation int
	query      string
	items      []MemoryItem
	err        error
}

func NewModel(workspace string, backend Backend) Model {
	return NewModelWithContext(context.Background(), workspace, backend)
}

func NewModelWithContext(ctx context.Context, workspace string, backend Backend) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	model := Model{
		workspace:   sanitizeTerminalText(strings.TrimSpace(workspace), false),
		backend:     backend,
		context:     ctx,
		destination: DestinationHome,
	}
	if backend != nil {
		model.overviewLoading = true
		model.browseLoading = true
		model.overviewGeneration = 1
		model.browseGeneration = 1
	}
	return model
}

func (m Model) Init() tea.Cmd {
	if m.backend == nil {
		return nil
	}
	return tea.Batch(
		loadOverviewCmd(m.context, m.backend, m.overviewGeneration),
		loadRecentCmd(m.context, m.backend, m.browseGeneration),
	)
}

func loadOverviewCmd(ctx context.Context, backend Backend, generation int) tea.Cmd {
	return func() tea.Msg {
		overview, err := backend.LoadOverview(ctx)
		return overviewLoadedMsg{generation: generation, overview: overview, err: err}
	}
}

func loadRecentCmd(ctx context.Context, backend Backend, generation int) tea.Cmd {
	return func() tea.Msg {
		items, err := backend.ListRecent(ctx, browseLimit)
		return recentLoadedMsg{generation: generation, items: items, err: err}
	}
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch loaded := message.(type) {
	case overviewLoadedMsg:
		if loaded.generation != m.overviewGeneration {
			return m, nil
		}
		m.overviewLoading = false
		if loaded.err != nil {
			m.overviewError = sanitizeTerminalText(loaded.err.Error(), false)
			return m, nil
		}
		m.overviewError = ""
		m.overview = loaded.overview
		return m, nil
	case recentLoadedMsg:
		if loaded.generation != m.browseGeneration {
			return m, nil
		}
		m.browseLoading = false
		if loaded.err != nil {
			m.browseError = sanitizeTerminalText(loaded.err.Error(), false)
			return m, nil
		}
		m.browseError = ""
		m.browseItems = append([]MemoryItem(nil), loaded.items...)
		m.browseCursor = clampCursor(m.browseCursor, len(m.browseItems))
		return m, nil
	case searchLoadedMsg:
		if loaded.generation != m.searchGeneration {
			return m, nil
		}
		m.searchLoading = false
		if loaded.err != nil {
			m.searchError = sanitizeTerminalText(loaded.err.Error(), false)
			return m, nil
		}
		m.searchError = ""
		m.lastSearchQuery = loaded.query
		m.searchItems = append([]MemoryItem(nil), loaded.items...)
		m.searchCursor = clampCursor(m.searchCursor, len(m.searchItems))
		return m, nil
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		return m, nil
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	pressed := key.String()
	if pressed == "ctrl+c" {
		return m, tea.Quit
	}
	if m.helpVisible {
		switch pressed {
		case "esc", "?":
			m.helpVisible = false
		case "q":
			return m, tea.Quit
		}
		return m, nil
	}
	if m.detail != nil {
		switch pressed {
		case "esc", "backspace":
			m.detail = nil
			m.detailScroll = 0
		case "q":
			return m, tea.Quit
		case "up", "k":
			if m.detailScroll > 0 {
				m.detailScroll--
			}
		case "down", "j":
			m.detailScroll++
		case "g":
			m.detailScroll = 0
		case "G":
			m.detailScroll = int(^uint(0) >> 1)
		}
		return m, nil
	}
	if m.searchFocused {
		switch pressed {
		case "esc":
			m.searchFocused = false
		case "enter":
			query := strings.TrimSpace(m.query)
			if query == "" {
				return m, nil
			}
			m.query = query
			m.searchFocused = false
			return m.startSearch(query)
		case "backspace":
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
			}
		default:
			if key.Text != "" {
				m.query += sanitizeTerminalText(key.Text, false)
			}
		}
		return m, nil
	}

	switch pressed {
	case "q":
		return m, tea.Quit
	case "?":
		m.helpVisible = true
	case "/":
		m.destination = DestinationSearch
		m.searchFocused = true
	case "tab":
		m.destination = nextDestination(m.destination)
	case "shift+tab":
		m.destination = previousDestination(m.destination)
	case "1":
		m.destination = DestinationHome
	case "2":
		m.destination = DestinationSearch
	case "3":
		m.destination = DestinationBrowse
	case "up", "k":
		if m.destination == DestinationBrowse && m.browseCursor > 0 {
			m.browseCursor--
		} else if m.destination == DestinationSearch && m.searchCursor > 0 {
			m.searchCursor--
		}
	case "down", "j":
		if m.destination == DestinationBrowse && m.browseCursor < len(m.browseItems)-1 {
			m.browseCursor++
		} else if m.destination == DestinationSearch && m.searchCursor < len(m.searchItems)-1 {
			m.searchCursor++
		}
	case "g":
		if m.destination == DestinationBrowse {
			m.browseCursor = 0
		} else if m.destination == DestinationSearch {
			m.searchCursor = 0
		}
	case "G":
		if m.destination == DestinationBrowse && len(m.browseItems) > 0 {
			m.browseCursor = len(m.browseItems) - 1
		} else if m.destination == DestinationSearch && len(m.searchItems) > 0 {
			m.searchCursor = len(m.searchItems) - 1
		}
	case "enter":
		if m.destination == DestinationBrowse && len(m.browseItems) > 0 {
			item := m.browseItems[m.browseCursor]
			m.detail = &item
			m.detailParent = DestinationBrowse
			m.detailScroll = 0
		} else if m.destination == DestinationSearch && len(m.searchItems) > 0 {
			item := m.searchItems[m.searchCursor]
			m.detail = &item
			m.detailParent = DestinationSearch
			m.detailScroll = 0
		}
	case "r":
		return m.refresh()
	}
	return m, nil
}

func (m Model) refresh() (tea.Model, tea.Cmd) {
	if m.backend == nil {
		return m, nil
	}
	switch m.destination {
	case DestinationBrowse:
		m.browseGeneration++
		m.browseLoading = true
		m.browseError = ""
		return m, loadRecentCmd(m.context, m.backend, m.browseGeneration)
	case DestinationHome:
		m.overviewGeneration++
		m.overviewLoading = true
		m.overviewError = ""
		return m, loadOverviewCmd(m.context, m.backend, m.overviewGeneration)
	case DestinationSearch:
		query := strings.TrimSpace(m.lastSearchQuery)
		if query == "" {
			query = strings.TrimSpace(m.query)
		}
		if query == "" {
			return m, nil
		}
		return m.startSearch(query)
	default:
		return m, nil
	}
}

func (m Model) startSearch(query string) (tea.Model, tea.Cmd) {
	if m.backend == nil {
		return m, nil
	}
	m.searchGeneration++
	m.searchLoading = true
	m.searchError = ""
	return m, loadSearchCmd(m.context, m.backend, m.searchGeneration, query)
}

func loadSearchCmd(ctx context.Context, backend Backend, generation int, query string) tea.Cmd {
	return func() tea.Msg {
		items, err := backend.Search(ctx, query, searchLimit)
		return searchLoadedMsg{generation: generation, query: query, items: items, err: err}
	}
}

func clampCursor(cursor, length int) int {
	if length <= 0 || cursor < 0 {
		return 0
	}
	if cursor >= length {
		return length - 1
	}
	return cursor
}

func nextDestination(current Destination) Destination {
	switch current {
	case DestinationHome:
		return DestinationSearch
	case DestinationSearch:
		return DestinationBrowse
	default:
		return DestinationHome
	}
}

func previousDestination(current Destination) Destination {
	switch current {
	case DestinationHome:
		return DestinationBrowse
	case DestinationBrowse:
		return DestinationSearch
	default:
		return DestinationHome
	}
}
