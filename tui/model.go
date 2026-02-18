package tui

import (
	"strings"
	"time"

	dnspkg "dns-prop-test/dns"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View represents which sub-view is currently active.
type View int

const (
	ViewForm View = iota
	ViewResults
	ViewHistory
	ViewConfig
)

// Model is the top-level Bubble Tea model that manages view state and routing.
type Model struct {
	currentView View
	config      *dnspkg.Config
	configPath  string
	width       int
	height      int
	checkActive bool // true when a DNS check is running
	form        FormModel
	results     ResultsModel
	history     HistoryModel
	configView  ConfigModel
	historyData []HistoryEntry
	help        help.Model
	showHelp    bool // true when the full help overlay is visible

	// Track current check's form data for history entry
	lastFormMsg FormSubmitMsg
}

// New creates a new top-level TUI model with the given config.
func New(cfg *dnspkg.Config, configPath string) Model {
	return Model{
		currentView: ViewForm,
		config:      cfg,
		configPath:  configPath,
		form:        NewFormModel(cfg.Defaults.Timeout, cfg.Defaults.Retry),
		history:     NewHistoryModel(nil),
		help:        help.New(),
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return m.form.Init()
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.form.SetSize(msg.Width, msg.Height)
		m.results.SetSize(msg.Width, msg.Height)
		m.history.SetSize(msg.Width, msg.Height)
		m.configView.SetSize(msg.Width, msg.Height)
		return m, nil

	case FormSubmitMsg:
		m.lastFormMsg = msg
		m.results = NewResultsModel(msg)
		m.results.SetSize(m.width, m.height)
		m.currentView = ViewResults
		m.checkActive = true
		cmd := m.results.StartCheck(m.config, msg)
		return m, cmd

	case CheckCompleteMsg, CheckTimeoutMsg, CheckCancelledMsg, CheckErrorMsg:
		m.checkActive = false
		// Delegate to results view for handling
		var cmd tea.Cmd
		m.results, cmd = m.results.Update(msg)
		// Record history entry
		m.appendHistory(msg)
		return m, cmd

	case tea.KeyMsg:
		// Help overlay: ? or esc dismisses, all other keys consumed
		if m.showHelp {
			if msg.String() == "?" || msg.String() == "esc" {
				m.showHelp = false
			}
			return m, nil
		}

		// Toggle help overlay with ?
		if msg.String() == "?" {
			// Don't intercept ? in text input views
			inputActive := m.currentView == ViewForm || (m.currentView == ViewConfig && m.configView.isEditing())
			if !inputActive {
				m.showHelp = true
				return m, nil
			}
		}

		// Esc during active check: cancel the check (stay in TUI)
		if msg.String() == "esc" && m.checkActive && m.currentView == ViewResults {
			m.results.Cancel()
			return m, nil
		}

		// Ctrl+C handling
		if msg.String() == "ctrl+c" {
			// During active check: cancel first (second ctrl+c will quit since checkActive becomes false)
			if m.checkActive && m.currentView == ViewResults {
				m.results.Cancel()
				return m, nil
			}
			// No active check: quit
			return m, tea.Quit
		}

		// q key: quit (except in views with text inputs where it's a valid character)
		if msg.String() == "q" {
			if m.currentView == ViewForm || (m.currentView == ViewConfig && m.configView.isEditing()) {
				break // fall through to sub-view
			}
			return m, tea.Quit
		}

		// Global navigation keys (only when no check is running and not in text-input views)
		inputActive := m.currentView == ViewForm || (m.currentView == ViewConfig && m.configView.isEditing())
		if !m.checkActive && !inputActive {
			switch msg.String() {
			case "n":
				m.currentView = ViewForm
				m.form = NewFormModel(m.config.Defaults.Timeout, m.config.Defaults.Retry)
				return m, m.form.Init()
			case "h":
				m.currentView = ViewHistory
				m.history = NewHistoryModel(m.historyData)
				m.history.SetSize(m.width, m.height)
				return m, nil
			case "c":
				m.currentView = ViewConfig
				m.configView = NewConfigModel(m.config, m.configPath)
				m.configView.SetSize(m.width, m.height)
				return m, nil
			}
		}

	case HistorySelectMsg:
		if msg.Index >= 0 && msg.Index < len(m.historyData) {
			m.results = m.historyData[msg.Index].Results
			m.results.SetSize(m.width, m.height)
			m.currentView = ViewResults
		}
		return m, nil

	case HistoryRerunMsg:
		formMsg := FormSubmitMsg{
			Domain:  msg.Entry.Domain,
			Type:    strings.ToLower(msg.Entry.RecordType),
			Match:   msg.Entry.Match,
			Timeout: msg.Entry.Timeout,
			Retry:   msg.Entry.Retry,
		}
		m.lastFormMsg = formMsg
		m.results = NewResultsModel(formMsg)
		m.results.SetSize(m.width, m.height)
		m.currentView = ViewResults
		m.checkActive = true
		cmd := m.results.StartCheck(m.config, formMsg)
		return m, cmd
	}

	// Delegate to active sub-view
	var cmd tea.Cmd
	switch m.currentView {
	case ViewForm:
		m.form, cmd = m.form.Update(msg)
		return m, cmd
	case ViewResults:
		m.results, cmd = m.results.Update(msg)
		return m, cmd
	case ViewHistory:
		m.history, cmd = m.history.Update(msg)
		return m, cmd
	case ViewConfig:
		m.configView, cmd = m.configView.Update(msg)
		return m, cmd
	}

	return m, nil
}

// minWidth and minHeight define the minimum supported terminal size.
const (
	minWidth  = 80
	minHeight = 24
)

// View implements tea.Model.
func (m Model) View() string {
	// Check for minimum terminal size
	if m.width > 0 && m.height > 0 && (m.width < minWidth || m.height < minHeight) {
		msg := lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true).
			Align(lipgloss.Center).
			Render("Terminal too small. Resize to at least 80x24.")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
	}

	if m.showHelp {
		return renderHelpOverlay(m.width, m.height)
	}

	var content string

	switch m.currentView {
	case ViewForm:
		content = m.form.View()
	case ViewResults:
		content = m.results.View()
	case ViewHistory:
		content = m.history.View()
	case ViewConfig:
		content = m.configView.View()
	}

	configEditing := m.currentView == ViewConfig && m.configView.isEditing()
	m.help.Width = m.width
	helpBar := m.help.View(keyMapForView(m.currentView, m.checkActive, configEditing))

	return lipgloss.JoinVertical(lipgloss.Left,
		HeaderStyle.Render("dns-prop-test TUI"),
		"",
		content,
		"",
		helpBar,
	)
}

// appendHistory records the completed check in the history (most-recent-first).
func (m *Model) appendHistory(msg tea.Msg) {
	authProp, authTotal := m.results.countPropagated(m.results.authoritative)
	resProp, resTotal := m.results.countPropagated(m.results.resolvers)

	entry := HistoryEntry{
		Domain:         m.lastFormMsg.Domain,
		RecordType:     strings.ToUpper(m.lastFormMsg.Type),
		Match:          m.lastFormMsg.Match,
		Timeout:        m.lastFormMsg.Timeout,
		Retry:          m.lastFormMsg.Retry,
		Timestamp:      time.Now(),
		AuthPropagated: authProp,
		AuthTotal:      authTotal,
		ResPropagated:  resProp,
		ResTotal:       resTotal,
		Results:        m.results,
	}

	switch msg.(type) {
	case CheckCompleteMsg:
		entry.Status = "complete"
		entry.Elapsed = m.results.elapsed
	case CheckTimeoutMsg:
		entry.Status = "timeout"
		entry.Elapsed = m.results.elapsed
	case CheckCancelledMsg:
		entry.Status = "cancelled"
	case CheckErrorMsg:
		entry.Status = "error"
	}

	// Prepend for most-recent-first ordering
	m.historyData = append([]HistoryEntry{entry}, m.historyData...)
}

