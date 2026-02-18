package tui

import (
	"fmt"
	"strings"

	dnspkg "dns-prop-test/dns"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// configField identifies a config field by index.
type configField int

const (
	configFieldTimeout configField = iota
	configFieldRetry
	configFieldRecordType
	configFieldResolvers // start of resolver list
)

// resolverEntry tracks a resolver and its enabled state.
type resolverEntry struct {
	Addr    string
	Enabled bool
}

// ConfigModel holds the state for the configuration view.
type ConfigModel struct {
	config *dnspkg.Config

	// Editable fields
	timeoutInput  textinput.Model
	retryInput    textinput.Model
	recordTypeIdx int
	resolvers     []resolverEntry // full list with enabled state

	// Navigation
	focused        configField
	resolverCursor int // index within resolver list
	width          int
	height         int

	// Save confirmation
	confirmSave bool
	saveMsg     string
	configPath  string
}

// ConfigSavedMsg is sent after config is saved to disk.
type ConfigSavedMsg struct {
	Err error
}

// NewConfigModel creates a config model from the current runtime config.
func NewConfigModel(cfg *dnspkg.Config, configPath string) ConfigModel {
	ti := textinput.New()
	ti.SetValue(cfg.Defaults.Timeout)
	ti.CharLimit = 10
	ti.Width = 10

	ri := textinput.New()
	ri.SetValue(cfg.Defaults.Retry)
	ri.CharLimit = 10
	ri.Width = 10

	resolvers := make([]resolverEntry, len(cfg.PublicResolvers))
	for i, addr := range cfg.PublicResolvers {
		resolvers[i] = resolverEntry{Addr: addr, Enabled: true}
	}

	rtIdx := 0
	for i, rt := range recordTypes {
		if strings.EqualFold(rt, cfg.Defaults.RecordType) {
			rtIdx = i
			break
		}
	}

	m := ConfigModel{
		config:        cfg,
		timeoutInput:  ti,
		retryInput:    ri,
		recordTypeIdx: rtIdx,
		resolvers:     resolvers,
		focused:       configFieldTimeout,
		configPath:    configPath,
	}
	m.timeoutInput.Focus()
	return m
}

// isEditing returns true when a text input is focused (letters should go to input, not nav).
func (m *ConfigModel) isEditing() bool {
	return m.focused == configFieldTimeout || m.focused == configFieldRetry
}

// SetSize updates the config view dimensions.
func (m *ConfigModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Update handles messages for the config view.
func (m ConfigModel) Update(msg tea.Msg) (ConfigModel, tea.Cmd) {
	switch msg := msg.(type) {
	case ConfigSavedMsg:
		if msg.Err != nil {
			m.saveMsg = fmt.Sprintf("Error: %s", msg.Err.Error())
		} else {
			m.saveMsg = "Config saved!"
		}
		m.confirmSave = false
		return m, nil

	case tea.KeyMsg:
		// Handle save confirmation
		if m.confirmSave {
			switch msg.String() {
			case "y":
				m.confirmSave = false
				return m, m.saveConfig()
			case "n", "esc":
				m.confirmSave = false
				m.saveMsg = ""
				return m, nil
			}
			return m, nil
		}

		// Clear save message on any key
		m.saveMsg = ""

		switch msg.String() {
		case "s":
			if m.configPath != "" {
				m.confirmSave = true
				return m, nil
			}
			m.saveMsg = "No config file path — use -c flag"
			return m, nil

		case "tab", "down", "j":
			m.moveNext()
			return m, nil

		case "shift+tab", "up", "k":
			m.movePrev()
			return m, nil

		case "enter", " ":
			if m.focused == configFieldRecordType {
				m.recordTypeIdx = (m.recordTypeIdx + 1) % len(recordTypes)
				m.config.Defaults.RecordType = strings.ToLower(recordTypes[m.recordTypeIdx])
				return m, nil
			}
			if m.focused == configFieldResolvers && m.resolverCursor < len(m.resolvers) {
				m.resolvers[m.resolverCursor].Enabled = !m.resolvers[m.resolverCursor].Enabled
				m.syncResolvers()
				return m, nil
			}
		}
	}

	// Update focused text input
	var cmd tea.Cmd
	switch m.focused {
	case configFieldTimeout:
		m.timeoutInput, cmd = m.timeoutInput.Update(msg)
		m.config.Defaults.Timeout = m.timeoutInput.Value()
		return m, cmd
	case configFieldRetry:
		m.retryInput, cmd = m.retryInput.Update(msg)
		m.config.Defaults.Retry = m.retryInput.Value()
		return m, cmd
	}

	return m, nil
}

func (m *ConfigModel) moveNext() {
	m.blurAll()
	switch m.focused {
	case configFieldTimeout:
		m.focused = configFieldRetry
		m.retryInput.Focus()
	case configFieldRetry:
		m.focused = configFieldRecordType
	case configFieldRecordType:
		if len(m.resolvers) > 0 {
			m.focused = configFieldResolvers
			m.resolverCursor = 0
		}
	case configFieldResolvers:
		if m.resolverCursor < len(m.resolvers)-1 {
			m.resolverCursor++
		}
	}
}

func (m *ConfigModel) movePrev() {
	m.blurAll()
	switch m.focused {
	case configFieldTimeout:
		m.timeoutInput.Focus()
	case configFieldRetry:
		m.focused = configFieldTimeout
		m.timeoutInput.Focus()
	case configFieldRecordType:
		m.focused = configFieldRetry
		m.retryInput.Focus()
	case configFieldResolvers:
		if m.resolverCursor > 0 {
			m.resolverCursor--
		} else {
			m.focused = configFieldRecordType
		}
	}
}

func (m *ConfigModel) blurAll() {
	m.timeoutInput.Blur()
	m.retryInput.Blur()
}

// syncResolvers rebuilds config.PublicResolvers from the enabled entries.
func (m *ConfigModel) syncResolvers() {
	enabled := make([]string, 0, len(m.resolvers))
	for _, r := range m.resolvers {
		if r.Enabled {
			enabled = append(enabled, r.Addr)
		}
	}
	m.config.PublicResolvers = enabled
}

func (m *ConfigModel) saveConfig() tea.Cmd {
	path := m.configPath
	cfg := *m.config
	return func() tea.Msg {
		err := dnspkg.SaveConfig(&cfg, path)
		return ConfigSavedMsg{Err: err}
	}
}

// View renders the config view.
func (m ConfigModel) View() string {
	var b strings.Builder

	b.WriteString(HeaderStyle.Render("Configuration"))
	b.WriteString("\n\n")

	labelStyle := lipgloss.NewStyle().Width(20).Foreground(colorMuted)
	focusedLabel := lipgloss.NewStyle().Width(20).Foreground(colorHeader).Bold(true)

	// Default Timeout
	label := labelStyle
	if m.focused == configFieldTimeout {
		label = focusedLabel
	}
	b.WriteString(label.Render("Default Timeout"))
	b.WriteString(m.timeoutInput.View())
	b.WriteString("\n\n")

	// Default Retry
	label = labelStyle
	if m.focused == configFieldRetry {
		label = focusedLabel
	}
	b.WriteString(label.Render("Default Retry"))
	b.WriteString(m.retryInput.View())
	b.WriteString("\n\n")

	// Record Type
	label = labelStyle
	if m.focused == configFieldRecordType {
		label = focusedLabel
	}
	b.WriteString(label.Render("Default Record Type"))
	b.WriteString(m.renderRecordType())
	b.WriteString("\n\n")

	// Public Resolvers
	b.WriteString(HeaderStyle.Render("Public Resolvers"))
	b.WriteString("\n\n")

	for i, r := range m.resolvers {
		isFocused := m.focused == configFieldResolvers && m.resolverCursor == i

		prefix := "  "
		if isFocused {
			prefix = focusedLabel.Width(2).Render("> ")
		}

		var toggle string
		if r.Enabled {
			toggle = StatusGreen.Render("[x]")
		} else {
			toggle = MutedStyle.Render("[ ]")
		}

		addrText := r.Addr
		if !r.Enabled {
			addrText = MutedStyle.Render(strikethrough(r.Addr))
		}

		b.WriteString(fmt.Sprintf("%s %s %s\n", prefix, toggle, addrText))
	}

	b.WriteString("\n")

	// Save status / confirmation
	if m.confirmSave {
		b.WriteString(StatusYellow.Render("Save to config file? (y/n)"))
	} else if m.saveMsg != "" {
		if strings.HasPrefix(m.saveMsg, "Error") {
			b.WriteString(StatusRed.Render(m.saveMsg))
		} else {
			b.WriteString(StatusGreen.Render(m.saveMsg))
		}
	}
	b.WriteString("\n\n")

	b.WriteString(MutedStyle.Render("tab/j/k: navigate  enter/space: toggle  s: save to file"))

	return BorderStyle.Render(b.String())
}

func (m ConfigModel) renderRecordType() string {
	var parts []string
	for i, rt := range recordTypes {
		if i == m.recordTypeIdx {
			style := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fff")).
				Background(colorHeader).
				Padding(0, 1).
				Bold(true)
			parts = append(parts, style.Render(rt))
		} else {
			style := lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 1)
			parts = append(parts, style.Render(rt))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, parts...)
}

func strikethrough(s string) string {
	var result strings.Builder
	for _, r := range s {
		result.WriteRune(r)
		result.WriteRune('\u0336')
	}
	return result.String()
}
