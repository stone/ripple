package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HistoryEntry stores a completed check's parameters and summary.
type HistoryEntry struct {
	Domain     string
	RecordType string
	Match      string
	Timeout    string
	Retry      string
	Timestamp  time.Time
	Status     string // "complete", "timeout", "cancelled", "error"
	Elapsed    time.Duration

	AuthPropagated int
	AuthTotal      int
	ResPropagated  int
	ResTotal       int

	// Full results for viewing details
	Results ResultsModel
}

// historyItem adapts HistoryEntry for bubbles/list.
type historyItem struct {
	entry HistoryEntry
	index int
}

func (i historyItem) Title() string {
	return fmt.Sprintf("%s  %s  %s", i.entry.Domain, i.entry.RecordType, i.entry.Match)
}

func (i historyItem) Description() string {
	ts := i.entry.Timestamp.Format("15:04:05")
	return fmt.Sprintf("%s  %s", ts, i.entry.summaryText())
}

func (i historyItem) FilterValue() string {
	return i.entry.Domain
}

// summaryText returns a short summary like "14/14 propagated" or "Timed out: 8/14".
func (e HistoryEntry) summaryText() string {
	totalProp := e.AuthPropagated + e.ResPropagated
	total := e.AuthTotal + e.ResTotal
	switch e.Status {
	case "complete":
		return StatusGreen.Render(fmt.Sprintf("✓ %d/%d propagated in %s", totalProp, total, formatElapsed(e.Elapsed)))
	case "timeout":
		return StatusYellow.Render(fmt.Sprintf("⏱ Timed out: %d/%d auth, %d/%d resolvers",
			e.AuthPropagated, e.AuthTotal, e.ResPropagated, e.ResTotal))
	case "cancelled":
		return MutedStyle.Render(fmt.Sprintf("Cancelled: %d/%d propagated", totalProp, total))
	case "error":
		return StatusRed.Render("✗ Error")
	default:
		return ""
	}
}

// HistorySelectMsg is sent when the user selects a history entry to view details.
type HistorySelectMsg struct {
	Index int
}

// HistoryRerunMsg is sent when the user wants to re-run a check from history.
type HistoryRerunMsg struct {
	Entry HistoryEntry
}

// HistoryModel holds the state for the history view.
type HistoryModel struct {
	list    list.Model
	entries []HistoryEntry
	width   int
	height  int
}

// NewHistoryModel creates a history model from a slice of entries.
func NewHistoryModel(entries []HistoryEntry) HistoryModel {
	items := historyItems(entries)

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(colorHeader).
		BorderForeground(colorHeader)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(colorMuted).
		BorderForeground(colorHeader)

	l := list.New(items, delegate, 0, 0)
	l.Title = "Check History"
	l.Styles.Title = HeaderStyle
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit.SetEnabled(false) // we handle quit at top-level

	return HistoryModel{
		list:    l,
		entries: entries,
	}
}

func historyItems(entries []HistoryEntry) []list.Item {
	items := make([]list.Item, len(entries))
	for i, e := range entries {
		items[i] = historyItem{entry: e, index: i}
	}
	return items
}

// SetSize updates the history view dimensions.
func (m *HistoryModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	// Reserve space for header, nav bar, and padding
	m.list.SetSize(w-4, h-8)
}

// Update handles messages for the history view.
func (m HistoryModel) Update(msg tea.Msg) (HistoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if item, ok := m.list.SelectedItem().(historyItem); ok {
				return m, func() tea.Msg {
					return HistorySelectMsg{Index: item.index}
				}
			}
		case "r":
			if item, ok := m.list.SelectedItem().(historyItem); ok {
				return m, func() tea.Msg {
					return HistoryRerunMsg{Entry: item.entry}
				}
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// View renders the history view.
func (m HistoryModel) View() string {
	if len(m.entries) == 0 {
		return m.emptyView()
	}

	var b strings.Builder
	b.WriteString(m.list.View())
	b.WriteString("\n")
	b.WriteString(MutedStyle.Render("enter: view details  r: re-run  ↑/↓: navigate"))
	return b.String()
}

func (m HistoryModel) emptyView() string {
	msg := lipgloss.NewStyle().
		Foreground(colorMuted).
		Align(lipgloss.Center).
		Render("No checks yet. Press n to start a new check.")

	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width-4, m.height-8, lipgloss.Center, lipgloss.Center, msg)
	}
	return msg
}
