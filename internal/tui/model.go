package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

type Destination string

const (
	DestinationHome   Destination = "home"
	DestinationSearch Destination = "search"
	DestinationBrowse Destination = "browse"
	DestinationJev    Destination = "jev"
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

type JevCredentialStore interface {
	Configured(context.Context) (bool, error)
	Save(context.Context, string) error
	Clear(context.Context) error
}

type Model struct {
	workspace          string
	backend            Backend
	jevCredentials     JevCredentialStore
	context            context.Context
	consoleBackend     ConsoleBackend
	console            consoleState
	ux                 workspaceUX
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
	jevConfigured      bool
	jevLoading         bool
	jevBusy            bool
	jevError           string
	jevNotice          string
	jevInput           bool
	jevDraft           string
	jevConfirmClear    bool
	jevGeneration      int
}

const browseLimit = 100
const searchLimit = 20

var modelGeneration atomic.Int64

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
	return NewModelWithCredentials(ctx, workspace, backend, nil)
}

func NewModelWithCredentials(ctx context.Context, workspace string, backend Backend, credentials JevCredentialStore) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	model := Model{
		workspace:      sanitizeTerminalText(strings.TrimSpace(workspace), false),
		backend:        backend,
		jevCredentials: credentials,
		context:        ctx,
		destination:    DestinationHome,
	}
	// Reserve a distinct generation range so a workspace switch cannot accept
	// a late result from a previous model instance.
	seed := int(modelGeneration.Add(1)) * 1000000
	model.overviewGeneration = seed
	model.browseGeneration = seed
	model.searchGeneration = seed
	model.jevGeneration = seed
	model.console.generation = seed
	if backend != nil {
		model.consoleBackend, _ = backend.(ConsoleBackend)
		model.overviewLoading = true
		model.browseLoading = true
	}
	if credentials != nil {
		model.jevLoading = true
	}
	return model
}

func (m Model) Init() tea.Cmd {
	var commands []tea.Cmd
	if m.backend != nil {
		commands = append(commands, loadOverviewCmd(m.context, m.backend, m.overviewGeneration), loadRecentCmd(m.context, m.backend, m.browseGeneration))
	}
	if m.jevCredentials != nil {
		commands = append(commands, loadJevStatusCmd(m.context, m.jevCredentials, m.jevGeneration))
	}
	if len(commands) == 0 {
		return nil
	}
	return tea.Batch(commands...)
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
	if pasted, ok := message.(tea.PasteMsg); ok && m.console.filtering {
		if len(m.console.filter)+len(pasted.Content) <= 256 {
			m.console.filter += sanitizeTerminalText(pasted.Content, false)
			m.console.cursor = 0
		}
		return m, nil
	}
	if pasted, ok := message.(tea.PasteMsg); ok && m.ux.palette != 0 {
		if len(m.ux.paletteQuery)+len(pasted.Content) <= 256 {
			m.ux.paletteQuery += sanitizeTerminalText(pasted.Content, false)
			m.ux.paletteCursor = 0
		}
		return m, nil
	}
	if edited, ok := message.(editorResultMsg); ok {
		if edited.generation != m.console.generation || m.console.form == nil || edited.cursor >= len(m.console.form.values) {
			return m, nil
		}
		if edited.err != nil {
			m.console.err = edited.err.Error()
		} else {
			m.console.form.values[edited.cursor] = edited.text
			m.console.form.caret = -1
			m.console.form.preferredColumn = -1
			m.console.err = ""
		}
		return m, nil
	}
	if switched, ok := message.(workspaceSelectedMsg); ok {
		if switched.generation != m.console.generation {
			return m, nil
		}
		if switched.err != nil {
			m.console.loading = false
			m.console.err = sanitizeTerminalText(switched.err.Error(), false)
			return m, nil
		}
		next := NewModelWithCredentials(m.context, switched.workspace, switched.backend, m.jevCredentials)
		next.width, next.height = m.width, m.height
		return next, next.Init()
	}
	if loaded, ok := message.(consoleLoadedMsg); ok {
		return m.acceptConsoleResult(loaded), nil
	}
	if pasted, ok := message.(tea.PasteMsg); ok && m.console.form != nil && !m.console.confirm && !m.console.busy {
		return m.updateConsoleInput(pasted.Content), nil
	}
	if pasted, ok := message.(tea.PasteMsg); ok {
		if m.searchFocused && len(m.query)+len(pasted.Content) <= 4096 {
			m.query += sanitizeTerminalText(pasted.Content, false)
		}
		if m.jevInput && len(m.jevDraft)+len(pasted.Content) <= 4096 && !strings.ContainsAny(pasted.Content, "\r\n") {
			m.jevDraft += pasted.Content
		}
		return m, nil
	}

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
	case jevStatusMsg:
		if loaded.generation != m.jevGeneration {
			return m, nil
		}
		m.jevLoading = false
		if loaded.err != nil {
			m.jevError = "Cannot read local Jev credential status."
		} else {
			m.jevConfigured = loaded.configured
			m.jevError = ""
		}
		return m, nil
	case jevMutationMsg:
		m.jevBusy = false
		m.jevLoading = false
		if loaded.err != nil {
			m.jevError = "Jev credential change failed; check local permissions."
			m.jevNotice = ""
		} else {
			m.jevConfigured = loaded.configured
			m.jevError = ""
			if loaded.configured {
				m.jevNotice = "Jev token saved."
			} else {
				m.jevNotice = "Jev token removed."
			}
		}
		return m, nil
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		if m.ux.navFocused && (size.Width < 100 || size.Height < 20) {
			m.ux.navFocused = false
			m.ux.palette = 1
			m.ux.paletteQuery = ""
			m.ux.paletteCursor = m.ux.navCursor
		}
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
	if next, command, handled := m.updateWorkspaceUXKey(key); handled {
		return next, command
	}
	if !m.searchFocused {
		if next, command, handled := m.updateConsoleKey(key); handled {
			return next, command
		}
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
	if next, command, handled := m.updateJevKey(key); handled {
		return next, command
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
		return m.cycleWorkspaceDestination(1)
	case "shift+tab":
		return m.cycleWorkspaceDestination(-1)
	case "1":
		m.destination = DestinationHome
	case "2":
		m.destination = DestinationSearch
	case "3":
		m.destination = DestinationBrowse
	case "4":
		m.destination = DestinationJev
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
	case DestinationJev:
		if m.jevCredentials == nil {
			return m, nil
		}
		m.jevLoading = true
		m.jevGeneration++
		return m, loadJevStatusCmd(m.context, m.jevCredentials, m.jevGeneration)
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
