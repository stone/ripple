package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Record types available for selection.
var recordTypes = []string{"A", "AAAA", "TXT", "CNAME", "MX", "NS"}

// FormSubmitMsg is sent when the form is submitted with valid data.
type FormSubmitMsg struct {
	Domain   string
	Type     string
	Match    string
	Timeout  string
	Retry    string
}

// formField identifies a form field by index.
type formField int

const (
	fieldDomain formField = iota
	fieldRecordType
	fieldMatch
	fieldTimeout
	fieldRetry
	fieldSubmit
	fieldCount
)

// FormModel holds the state for the input form view.
type FormModel struct {
	inputs      []textinput.Model
	recordIdx   int // index into recordTypes
	focused     formField
	errors      map[formField]string
	width       int
	height      int
}

// NewFormModel creates a new form with defaults from config.
func NewFormModel(defaultTimeout, defaultRetry string) FormModel {
	inputs := make([]textinput.Model, fieldCount)

	// Domain
	inputs[fieldDomain] = textinput.New()
	inputs[fieldDomain].Placeholder = "example.com"
	inputs[fieldDomain].CharLimit = 253
	inputs[fieldDomain].Width = 40
	inputs[fieldDomain].Focus()

	// Record Type — not a real text input, but we keep a slot for layout consistency.
	// The actual value is managed via recordIdx.
	inputs[fieldRecordType] = textinput.New()
	inputs[fieldRecordType].Placeholder = ""
	inputs[fieldRecordType].Width = 10

	// Match Value
	inputs[fieldMatch] = textinput.New()
	inputs[fieldMatch].Placeholder = "1.2.3.4 or text to match"
	inputs[fieldMatch].CharLimit = 512
	inputs[fieldMatch].Width = 40

	// Timeout
	inputs[fieldTimeout] = textinput.New()
	inputs[fieldTimeout].Placeholder = "1m"
	inputs[fieldTimeout].SetValue(defaultTimeout)
	inputs[fieldTimeout].CharLimit = 10
	inputs[fieldTimeout].Width = 10

	// Retry Interval
	inputs[fieldRetry] = textinput.New()
	inputs[fieldRetry].Placeholder = "5s"
	inputs[fieldRetry].SetValue(defaultRetry)
	inputs[fieldRetry].CharLimit = 10
	inputs[fieldRetry].Width = 10

	// Submit — placeholder slot (not a real text input)
	inputs[fieldSubmit] = textinput.New()
	inputs[fieldSubmit].Width = 0

	return FormModel{
		inputs:    inputs,
		recordIdx: 0,
		focused:   fieldDomain,
		errors:    make(map[formField]string),
	}
}

// Init implements tea.Model (used as sub-model, called manually).
func (m FormModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles keypresses for the form view.
func (m FormModel) Update(msg tea.Msg) (FormModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Clear errors on any input
		delete(m.errors, m.focused)

		switch msg.String() {
		case "tab", "down":
			m.focused = (m.focused + 1) % fieldCount
			m.updateFocus()
			return m, nil

		case "shift+tab", "up":
			m.focused = (m.focused - 1 + fieldCount) % fieldCount
			m.updateFocus()
			return m, nil

		case "enter":
			if m.focused == fieldSubmit {
				return m.submit()
			}
			// Enter on last real field also submits
			if m.focused == fieldRetry {
				return m.submit()
			}
			// On record type, cycle forward
			if m.focused == fieldRecordType {
				m.recordIdx = (m.recordIdx + 1) % len(recordTypes)
				return m, nil
			}
			// On other fields, advance to next
			m.focused = (m.focused + 1) % fieldCount
			m.updateFocus()
			return m, nil

		case " ":
			// Space on record type cycles forward
			if m.focused == fieldRecordType {
				m.recordIdx = (m.recordIdx + 1) % len(recordTypes)
				return m, nil
			}
		}
	}

	// Update the currently focused text input
	if m.focused != fieldRecordType && m.focused != fieldSubmit {
		var cmd tea.Cmd
		m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
		return m, cmd
	}

	return m, nil
}

// submit validates and submits the form.
func (m FormModel) submit() (FormModel, tea.Cmd) {
	m.errors = make(map[formField]string)

	domain := strings.TrimSpace(m.inputs[fieldDomain].Value())
	matchVal := strings.TrimSpace(m.inputs[fieldMatch].Value())

	if domain == "" {
		m.errors[fieldDomain] = "domain is required"
	}
	if matchVal == "" {
		m.errors[fieldMatch] = "match value is required"
	}

	if len(m.errors) > 0 {
		// Focus the first errored field
		for _, f := range []formField{fieldDomain, fieldMatch} {
			if _, ok := m.errors[f]; ok {
				m.focused = f
				m.updateFocus()
				break
			}
		}
		return m, nil
	}

	timeout := strings.TrimSpace(m.inputs[fieldTimeout].Value())
	if timeout == "" {
		timeout = "1m"
	}
	retry := strings.TrimSpace(m.inputs[fieldRetry].Value())
	if retry == "" {
		retry = "5s"
	}

	return m, func() tea.Msg {
		return FormSubmitMsg{
			Domain:  domain,
			Type:    strings.ToLower(recordTypes[m.recordIdx]),
			Match:   matchVal,
			Timeout: timeout,
			Retry:   retry,
		}
	}
}

// updateFocus blurs all inputs and focuses the current one.
func (m *FormModel) updateFocus() {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	if m.focused != fieldRecordType && m.focused != fieldSubmit {
		m.inputs[m.focused].Focus()
	}
}

// SetSize updates the form dimensions.
func (m *FormModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// View renders the form.
func (m FormModel) View() string {
	var b strings.Builder

	title := HeaderStyle.Render("New Check")
	b.WriteString(title)
	b.WriteString("\n\n")

	labelStyle := lipgloss.NewStyle().Width(16).Foreground(colorMuted)
	focusedLabel := lipgloss.NewStyle().Width(16).Foreground(colorHeader).Bold(true)
	errorStyle := lipgloss.NewStyle().Foreground(colorRed)

	// Domain field
	label := labelStyle
	if m.focused == fieldDomain {
		label = focusedLabel
	}
	b.WriteString(label.Render("Domain"))
	b.WriteString(m.inputs[fieldDomain].View())
	if err, ok := m.errors[fieldDomain]; ok {
		b.WriteString("  ")
		b.WriteString(errorStyle.Render(err))
	}
	b.WriteString("\n\n")

	// Record Type field
	label = labelStyle
	if m.focused == fieldRecordType {
		label = focusedLabel
	}
	b.WriteString(label.Render("Record Type"))
	recTypeDisplay := m.renderRecordType()
	b.WriteString(recTypeDisplay)
	b.WriteString("\n\n")

	// Match Value field
	label = labelStyle
	if m.focused == fieldMatch {
		label = focusedLabel
	}
	b.WriteString(label.Render("Match Value"))
	b.WriteString(m.inputs[fieldMatch].View())
	if err, ok := m.errors[fieldMatch]; ok {
		b.WriteString("  ")
		b.WriteString(errorStyle.Render(err))
	}
	b.WriteString("\n\n")

	// Timeout field
	label = labelStyle
	if m.focused == fieldTimeout {
		label = focusedLabel
	}
	b.WriteString(label.Render("Timeout"))
	b.WriteString(m.inputs[fieldTimeout].View())
	b.WriteString("\n\n")

	// Retry Interval field
	label = labelStyle
	if m.focused == fieldRetry {
		label = focusedLabel
	}
	b.WriteString(label.Render("Retry Interval"))
	b.WriteString(m.inputs[fieldRetry].View())
	b.WriteString("\n\n")

	// Submit button
	btnStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Background(lipgloss.Color("#007bff")).
		Foreground(lipgloss.Color("#fff"))
	if m.focused == fieldSubmit {
		btnStyle = btnStyle.
			Background(lipgloss.Color("#0056b3")).
			Bold(true)
	}
	b.WriteString(lipgloss.NewStyle().Width(16).Render(""))
	b.WriteString(btnStyle.Render("Start Check"))
	b.WriteString("\n")

	return BorderStyle.Render(b.String())
}

// renderRecordType renders the record type selector.
func (m FormModel) renderRecordType() string {
	var parts []string
	for i, rt := range recordTypes {
		if i == m.recordIdx {
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
