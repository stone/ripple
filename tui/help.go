package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// --- Key bindings per view ---

type formKeyMap struct{}

func (k formKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next")),
		key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	}
}

func (k formKeyMap) FullHelp() [][]key.Binding { return nil }

type resultsKeyMap struct {
	checking bool
}

func (k resultsKeyMap) ShortHelp() []key.Binding {
	bindings := []key.Binding{}
	if k.checking {
		bindings = append(bindings,
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		)
	} else {
		bindings = append(bindings,
			key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new check")),
			key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "history")),
		)
	}
	bindings = append(bindings,
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "scroll")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	)
	return bindings
}

func (k resultsKeyMap) FullHelp() [][]key.Binding { return nil }

type historyKeyMap struct{}

func (k historyKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "re-run")),
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "navigate")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	}
}

func (k historyKeyMap) FullHelp() [][]key.Binding { return nil }

type configKeyMap struct {
	editing bool
}

func (k configKeyMap) ShortHelp() []key.Binding {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab/j/k", "navigate")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "toggle")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	}
	if !k.editing {
		bindings = append(bindings,
			key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		)
	}
	return bindings
}

func (k configKeyMap) FullHelp() [][]key.Binding { return nil }

// keyMapForView returns the appropriate KeyMap for the current view state.
func keyMapForView(view View, checkActive bool, configEditing bool) help.KeyMap {
	switch view {
	case ViewForm:
		return formKeyMap{}
	case ViewResults:
		return resultsKeyMap{checking: checkActive}
	case ViewHistory:
		return historyKeyMap{}
	case ViewConfig:
		return configKeyMap{editing: configEditing}
	default:
		return formKeyMap{}
	}
}

// --- Help overlay ---

// renderHelpOverlay renders the full-screen help overlay showing all keybindings.
func renderHelpOverlay(width, height int) string {
	titleStyle := HeaderStyle
	groupStyle := lipgloss.NewStyle().Foreground(colorHeader).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#e0e0e0")).Width(16)
	descStyle := lipgloss.NewStyle().Foreground(colorMuted)

	var b strings.Builder

	b.WriteString(titleStyle.Render("Keybindings"))
	b.WriteString("\n\n")

	groups := []struct {
		name     string
		bindings []struct{ key, desc string }
	}{
		{
			name: "Global",
			bindings: []struct{ key, desc string }{
				{"q / ctrl+c", "Quit"},
				{"n", "New check"},
				{"h", "History"},
				{"c", "Config"},
				{"?", "Toggle help"},
			},
		},
		{
			name: "Form",
			bindings: []struct{ key, desc string }{
				{"tab / shift+tab", "Next / previous field"},
				{"enter", "Submit form / advance"},
				{"enter / space", "Cycle record type"},
			},
		},
		{
			name: "Results",
			bindings: []struct{ key, desc string }{
				{"esc", "Cancel check"},
				{"ctrl+c", "Cancel (or quit if idle)"},
				{"↑/↓ / j/k", "Scroll server list"},
				{"pgup/pgdn", "Scroll page"},
			},
		},
		{
			name: "History",
			bindings: []struct{ key, desc string }{
				{"↑ / ↓", "Navigate list"},
				{"enter", "View check details"},
				{"r", "Re-run check"},
			},
		},
		{
			name: "Config",
			bindings: []struct{ key, desc string }{
				{"tab / j / k", "Navigate fields"},
				{"enter / space", "Toggle resolver"},
				{"s", "Save to config file"},
			},
		},
	}

	for _, g := range groups {
		b.WriteString(groupStyle.Render(g.name))
		b.WriteString("\n")
		for _, kb := range g.bindings {
			b.WriteString("  ")
			b.WriteString(keyStyle.Render(kb.key))
			b.WriteString(descStyle.Render(kb.desc))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(MutedStyle.Render("Press ? or esc to close"))

	content := BorderStyle.
		Width(max(width-4, 40)).
		Render(b.String())

	if width > 0 && height > 0 {
		return lipgloss.Place(width, height-2, lipgloss.Center, lipgloss.Center, content)
	}
	return content
}
