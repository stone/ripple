package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	dnspkg "ripple/dns"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Tea messages for DNS check lifecycle ---

// AuthServerDiscoveredMsg is sent when authoritative servers are found.
type AuthServerDiscoveredMsg struct {
	Servers []dnspkg.ResolverStatus
}

// ResolverInitializedMsg is sent when the resolver list is initialized.
type ResolverInitializedMsg struct {
	Resolvers []dnspkg.ResolverStatus
}

// ServerPropagatedMsg is sent when a server's propagation status is updated.
type ServerPropagatedMsg struct {
	Name       string
	Addr       string
	Propagated bool
	FoundAt    time.Duration
	Record     string
	IsAuth     bool // true = authoritative, false = resolver
}

// CheckCompleteMsg is sent when all servers have propagated.
type CheckCompleteMsg struct {
	Elapsed time.Duration
}

// CheckTimeoutMsg is sent when the check times out before full propagation.
type CheckTimeoutMsg struct {
	Elapsed time.Duration
}

// CheckCancelledMsg is sent when the user cancels a running check.
type CheckCancelledMsg struct{}

// CheckErrorMsg is sent when an error occurs during the check.
type CheckErrorMsg struct {
	Err error
}

// ResultsModel holds the state for the results dashboard view.
type ResultsModel struct {
	domain        string
	recordType    string
	match         string
	authoritative []dnspkg.ResolverStatus
	resolvers     []dnspkg.ResolverStatus
	width         int
	height        int

	// Live check state
	checking  bool
	startTime time.Time
	elapsed   time.Duration
	done      bool   // check finished (complete or timeout)
	status    string // "complete", "timeout", "cancelled", "error"
	errorMsg  string

	// Scroll offset for server lists when they exceed available space
	scrollOffset int

	// Spinner for active check indication
	spinner spinner.Model

	// Channel for receiving updates from the check goroutine
	updateCh chan tea.Msg
	cancel   context.CancelFunc
}

// NewResultsModel creates a results model from form submission data.
func NewResultsModel(msg FormSubmitMsg) ResultsModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(StatusYellow),
	)
	return ResultsModel{
		domain:     msg.Domain,
		recordType: strings.ToUpper(msg.Type),
		match:      msg.Match,
		spinner:    s,
		updateCh:   make(chan tea.Msg, 64),
	}
}

// SetAuthoritativeServers sets the authoritative server statuses.
func (m *ResultsModel) SetAuthoritativeServers(servers []dnspkg.ResolverStatus) {
	m.authoritative = servers
}

// SetResolvers sets the public resolver statuses.
func (m *ResultsModel) SetResolvers(servers []dnspkg.ResolverStatus) {
	m.resolvers = servers
}

// SetSize updates the results view dimensions.
func (m *ResultsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// StartCheck begins a DNS propagation check in a background goroutine.
// It returns a tea.Cmd that starts polling for updates.
func (m *ResultsModel) StartCheck(cfg *dnspkg.Config, formMsg FormSubmitMsg) tea.Cmd {
	m.checking = true
	m.startTime = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	ch := m.updateCh

	// Parse timeout and retry from form data
	timeout, err := time.ParseDuration(formMsg.Timeout)
	if err != nil {
		timeout = 1 * time.Minute
	}
	retry, err := time.ParseDuration(formMsg.Retry)
	if err != nil {
		retry = 5 * time.Second
	}

	domain := formMsg.Domain
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}
	recordType := strings.ToLower(formMsg.Type)
	match := formMsg.Match
	dnsType := dnspkg.ParseRecordType(recordType)

	rootServers := cfg.RootServers
	publicResolvers := cfg.PublicResolvers

	go func() {
		startTime := time.Now()

		// Find authoritative servers
		authServers, err := dnspkg.FindAuthoritativeServers(domain, rootServers)
		if err != nil {
			select {
			case ch <- CheckErrorMsg{Err: fmt.Errorf("finding auth servers: %w", err)}:
			case <-ctx.Done():
			}
			return
		}

		// Send discovered auth servers
		authStatuses := make([]dnspkg.ResolverStatus, len(authServers))
		for i, s := range authServers {
			authStatuses[i] = *s
		}
		select {
		case ch <- AuthServerDiscoveredMsg{Servers: authStatuses}:
		case <-ctx.Done():
			return
		}

		// Build resolver list
		resolverPtrs := make([]*dnspkg.ResolverStatus, 0, len(publicResolvers)+1)
		resolverStatuses := make([]dnspkg.ResolverStatus, 0, len(publicResolvers)+1)
		for _, addr := range publicResolvers {
			r := &dnspkg.ResolverStatus{
				Name: strings.Split(addr, ":")[0],
				Addr: addr,
			}
			resolverPtrs = append(resolverPtrs, r)
			resolverStatuses = append(resolverStatuses, *r)
		}
		localResolver := &dnspkg.ResolverStatus{Name: "local", Addr: ""}
		resolverPtrs = append(resolverPtrs, localResolver)
		resolverStatuses = append(resolverStatuses, *localResolver)

		select {
		case ch <- ResolverInitializedMsg{Resolvers: resolverStatuses}:
		case <-ctx.Done():
			return
		}

		// Set up timeout context
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, timeout)
		defer timeoutCancel()

		var mu sync.Mutex
		ticker := time.NewTicker(retry)
		defer ticker.Stop()

		for {
			// Check authoritative servers
			dnspkg.CheckAuthoritativeAllSilent(authServers, domain, dnsType, match, startTime, &mu)

			// Send updates for auth servers
			mu.Lock()
			for _, s := range authServers {
				if s.Propagated {
					select {
					case ch <- ServerPropagatedMsg{
						Name:       s.Name,
						Addr:       s.Addr,
						Propagated: true,
						FoundAt:    s.FoundAt,
						Record:     s.Record,
						IsAuth:     true,
					}:
					default: // non-blocking
					}
				}
			}
			mu.Unlock()

			// Check resolvers
			dnspkg.CheckResolverAllSilent(resolverPtrs, domain, recordType, match, startTime, &mu)

			// Send updates for resolvers
			mu.Lock()
			for _, r := range resolverPtrs {
				if r.Propagated {
					select {
					case ch <- ServerPropagatedMsg{
						Name:       r.Name,
						Addr:       r.Addr,
						Propagated: true,
						FoundAt:    r.FoundAt,
						Record:     r.Record,
						IsAuth:     false,
					}:
					default: // non-blocking
					}
				}
			}

			// Check if all propagated
			allDone := true
			for _, s := range authServers {
				if !s.Propagated {
					allDone = false
					break
				}
			}
			if allDone {
				for _, r := range resolverPtrs {
					if !r.Propagated {
						allDone = false
						break
					}
				}
			}
			mu.Unlock()

			if allDone {
				select {
				case ch <- CheckCompleteMsg{Elapsed: time.Since(startTime)}:
				case <-ctx.Done():
				}
				return
			}

			select {
			case <-timeoutCtx.Done():
				if ctx.Err() != nil {
					// Parent context cancelled (user cancellation)
					return
				}
				// Timeout
				select {
				case ch <- CheckTimeoutMsg{Elapsed: time.Since(startTime)}:
				case <-ctx.Done():
				}
				return
			case <-ticker.C:
				continue
			}
		}
	}()

	// Return commands to start polling the channel and start the spinner
	return tea.Batch(waitForUpdate(ch), m.spinner.Tick)
}

// waitForUpdate returns a tea.Cmd that reads one message from the update channel.
func waitForUpdate(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// Update handles messages for the results view.
func (m ResultsModel) Update(msg tea.Msg) (ResultsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case AuthServerDiscoveredMsg:
		m.authoritative = msg.Servers
		return m, waitForUpdate(m.updateCh)

	case ResolverInitializedMsg:
		m.resolvers = msg.Resolvers
		return m, waitForUpdate(m.updateCh)

	case ServerPropagatedMsg:
		if msg.IsAuth {
			for i := range m.authoritative {
				if m.authoritative[i].Addr == msg.Addr && !m.authoritative[i].Propagated {
					m.authoritative[i].Propagated = msg.Propagated
					m.authoritative[i].FoundAt = msg.FoundAt
					m.authoritative[i].Record = msg.Record
				}
			}
		} else {
			for i := range m.resolvers {
				if m.resolvers[i].Addr == msg.Addr && !m.resolvers[i].Propagated {
					m.resolvers[i].Propagated = msg.Propagated
					m.resolvers[i].FoundAt = msg.FoundAt
					m.resolvers[i].Record = msg.Record
				}
			}
		}
		return m, waitForUpdate(m.updateCh)

	case CheckCompleteMsg:
		m.checking = false
		m.done = true
		m.status = "complete"
		m.elapsed = msg.Elapsed
		return m, nil

	case CheckTimeoutMsg:
		m.checking = false
		m.done = true
		m.status = "timeout"
		m.elapsed = msg.Elapsed
		return m, nil

	case CheckCancelledMsg:
		m.checking = false
		m.done = true
		m.status = "cancelled"
		return m, nil

	case CheckErrorMsg:
		m.checking = false
		m.done = true
		m.status = "error"
		m.errorMsg = msg.Err.Error()
		return m, nil

	case spinner.TickMsg:
		if m.checking {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		totalLines := len(m.authoritative) + len(m.resolvers)
		switch msg.String() {
		case "up", "k":
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
		case "down", "j":
			if m.scrollOffset < totalLines {
				m.scrollOffset++
			}
		case "pgup":
			m.scrollOffset -= 10
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}
		case "pgdown":
			m.scrollOffset += 10
			if m.scrollOffset > totalLines {
				m.scrollOffset = totalLines
			}
		}
		return m, nil
	}

	return m, nil
}

// Cancel cancels the running check if any.
// It cancels the context and sends a CheckCancelledMsg through the update channel.
func (m *ResultsModel) Cancel() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.checking {
		select {
		case m.updateCh <- CheckCancelledMsg{}:
		default:
		}
	}
}

// IsChecking returns whether a check is currently running.
func (m *ResultsModel) IsChecking() bool {
	return m.checking
}

// View renders the results dashboard.
func (m ResultsModel) View() string {
	var b strings.Builder

	// Header with check parameters
	b.WriteString(HeaderStyle.Render("Results"))
	b.WriteString("\n")
	paramLine := fmt.Sprintf("Domain: %s  |  Type: %s  |  Match: %s", m.domain, m.recordType, m.match)
	if m.width > 0 && len(paramLine) > m.width-2 {
		paramLine = truncate(paramLine, m.width-2)
	}
	paramStyle := lipgloss.NewStyle().Foreground(colorMuted)
	b.WriteString(paramStyle.Render(paramLine))
	b.WriteString("\n\n")

	headerLines := 4 // "Results" + param line + 2 blank lines

	// Status banner
	bannerLines := 0
	if m.done {
		b.WriteString(m.renderBanner())
		b.WriteString("\n\n")
		bannerLines = 2
	} else if m.checking {
		b.WriteString(m.spinner.View())
		b.WriteString(" Checking...")
		b.WriteString("\n\n")
		bannerLines = 2
	}

	// Error display
	if m.status == "error" {
		b.WriteString(StatusRed.Render(fmt.Sprintf("Error: %s", m.errorMsg)))
		b.WriteString("\n\n")
		bannerLines += 2
	}

	// Calculate panel width — proportional to terminal width
	panelWidth := max(m.width-4, 40)

	// Calculate available height for panels (minus header, banner, help bar, spacing)
	// Top-level View adds: header(1) + blank(1) + content + blank(1) + help(1) = 4 overhead lines
	availableHeight := m.height - 4 - headerLines - bannerLines
	if availableHeight < 10 {
		availableHeight = 10
	}

	// Split available height between the two panels (including 1 line for gap between them)
	authPanelHeight := availableHeight / 2
	resPanelHeight := availableHeight - authPanelHeight - 1

	// Determine scroll distribution between the two panels
	authScroll, resScroll := m.distributeScroll(authPanelHeight, resPanelHeight)

	// Authoritative Nameservers panel
	authPanel := m.renderPanel("Authoritative Nameservers", m.authoritative, panelWidth, authPanelHeight, authScroll)

	// Public Resolvers panel
	resolverPanel := m.renderPanel("Public Resolvers", m.resolvers, panelWidth, resPanelHeight, resScroll)

	b.WriteString(authPanel)
	b.WriteString("\n")
	b.WriteString(resolverPanel)

	return b.String()
}

// distributeScroll distributes the scroll offset between the two panels.
// Scrolling first affects the auth panel, then the resolver panel.
func (m ResultsModel) distributeScroll(authHeight, resHeight int) (int, int) {
	// Content lines per panel = header(1) + blank(1) + entries + blank(1) + summary(1) = entries + 4
	// Inside the border, available rows for entries = panelHeight - border(2) - header(1) - blank(1) - blank(1) - summary(1) = panelHeight - 6
	authVisible := authHeight - 6
	if authVisible < 1 {
		authVisible = 1
	}
	authOverflow := len(m.authoritative) - authVisible
	if authOverflow < 0 {
		authOverflow = 0
	}

	authScroll := m.scrollOffset
	if authScroll > authOverflow {
		authScroll = authOverflow
	}

	resScroll := m.scrollOffset - authScroll
	resVisible := resHeight - 6
	if resVisible < 1 {
		resVisible = 1
	}
	resOverflow := len(m.resolvers) - resVisible
	if resOverflow < 0 {
		resOverflow = 0
	}
	if resScroll > resOverflow {
		resScroll = resOverflow
	}

	return authScroll, resScroll
}

// renderBanner renders the completion/timeout/cancelled banner.
func (m ResultsModel) renderBanner() string {
	switch m.status {
	case "complete":
		return StatusGreen.Render(fmt.Sprintf("✓ All propagated in %s", formatElapsed(m.elapsed)))
	case "timeout":
		authProp, authTotal := m.countPropagated(m.authoritative)
		resProp, resTotal := m.countPropagated(m.resolvers)
		return StatusYellow.Render(fmt.Sprintf("⏱ Timed out: %d/%d authoritative, %d/%d resolvers propagated",
			authProp, authTotal, resProp, resTotal))
	case "cancelled":
		return MutedStyle.Render("Cancelled — partial results shown")
	case "error":
		return StatusRed.Render("✗ Check failed")
	default:
		return ""
	}
}

func (m ResultsModel) countPropagated(servers []dnspkg.ResolverStatus) (int, int) {
	propagated := 0
	for _, s := range servers {
		if s.Propagated {
			propagated++
		}
	}
	return propagated, len(servers)
}

func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d.Milliseconds()))
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// renderPanel renders a single server panel with border, entries, and summary.
// panelHeight is the total height including border. scrollOff is the scroll offset for entries.
func (m ResultsModel) renderPanel(title string, servers []dnspkg.ResolverStatus, width, panelHeight, scrollOff int) string {
	var b strings.Builder

	b.WriteString(HeaderStyle.Render(title))
	b.WriteString("\n\n")

	if len(servers) == 0 {
		b.WriteString(MutedStyle.Render("  No servers discovered yet."))
		b.WriteString("\n")
	} else {
		// Column headers
		headerLine := m.renderEntryLine("SERVER", "ADDRESS", "STATUS", "TIME", "RECORD", width)
		b.WriteString(MutedStyle.Render(headerLine))
		b.WriteString("\n")

		// Calculate visible entries based on panel height
		// Inside border: title(1) + blank(1) + column_header(1) + entries + blank(1) + summary(1) = entries + 5
		// Border adds 2 (top + bottom). So entries = panelHeight - 2 - 5 = panelHeight - 7
		visibleEntries := panelHeight - 7
		if visibleEntries < 1 {
			visibleEntries = 1
		}

		// Apply scroll offset
		start := scrollOff
		if start > len(servers) {
			start = len(servers)
		}
		end := start + visibleEntries
		if end > len(servers) {
			end = len(servers)
		}

		// Scroll indicator (top)
		if start > 0 {
			b.WriteString(MutedStyle.Render(fmt.Sprintf("  ↑ %d more above", start)))
			b.WriteString("\n")
			// This takes one line from visible entries
			end--
			if end < start {
				end = start
			}
		}

		// Server entries
		for _, s := range servers[start:end] {
			b.WriteString(m.renderServerEntry(s, width))
			b.WriteString("\n")
		}

		// Scroll indicator (bottom)
		if end < len(servers) {
			b.WriteString(MutedStyle.Render(fmt.Sprintf("  ↓ %d more below", len(servers)-end)))
			b.WriteString("\n")
		}

		// Summary line
		propagated := 0
		for _, s := range servers {
			if s.Propagated {
				propagated++
			}
		}
		b.WriteString("\n")
		summaryText := fmt.Sprintf("%d/%d propagated", propagated, len(servers))
		if propagated == len(servers) && len(servers) > 0 {
			b.WriteString(StatusGreen.Render(summaryText))
		} else {
			b.WriteString(StatusYellow.Render(summaryText))
		}
	}

	panelStyle := BorderStyle
	if width > 0 {
		panelStyle = panelStyle.Width(width)
	}

	return panelStyle.Render(b.String())
}

// renderServerEntry renders a single server row with status indicator.
func (m ResultsModel) renderServerEntry(s dnspkg.ResolverStatus, panelWidth int) string {
	var statusIcon string
	var timeStr string
	var recordStr string

	if s.Propagated {
		statusIcon = StatusGreen.Render("✓")
		timeStr = dnspkg.FormatDuration(s.FoundAt)
		recordStr = s.Record
	} else if s.Record == "error" {
		statusIcon = StatusRed.Render("✗")
		timeStr = "-"
		recordStr = ""
	} else if m.checking {
		statusIcon = m.spinner.View()
		timeStr = "-"
		recordStr = ""
	} else {
		statusIcon = StatusYellow.Render("—")
		timeStr = "-"
		recordStr = ""
	}

	addr := strings.TrimSuffix(s.Addr, ":53")
	if s.Addr == "" {
		addr = "system"
	}

	return m.renderEntryLine(s.Name, addr, statusIcon, timeStr, recordStr, panelWidth)
}

// renderEntryLine renders a formatted row with proportional column widths.
func (m ResultsModel) renderEntryLine(name, addr, status, timeVal, record string, panelWidth int) string {
	// Panel inner width = panelWidth - border(2) - padding(2) - leading indent(2) = panelWidth - 6
	innerWidth := panelWidth - 6
	if innerWidth < 40 {
		innerWidth = 40
	}

	// Fixed-width columns: status(4) + time(8) = 12
	// Remaining space split: name(35%) + addr(25%) + record(rest)
	remaining := innerWidth - 12
	nameW := remaining * 35 / 100
	addrW := remaining * 25 / 100
	recordW := remaining - nameW - addrW

	if nameW < 8 {
		nameW = 8
	}
	if addrW < 8 {
		addrW = 8
	}
	if recordW < 4 {
		recordW = 4
	}

	nameCol := lipgloss.NewStyle().Width(nameW)
	addrCol := lipgloss.NewStyle().Width(addrW)
	statusCol := lipgloss.NewStyle().Width(4)
	timeCol := lipgloss.NewStyle().Width(8)

	// Truncate values that exceed column width
	name = truncate(name, nameW-1)
	addr = truncate(addr, addrW-1)
	record = truncate(record, recordW)

	line := fmt.Sprintf("  %s%s%s%s%s",
		nameCol.Render(name),
		addrCol.Render(addr),
		statusCol.Render(status),
		timeCol.Render(timeVal),
		record,
	)
	return line
}

// truncate shortens a string to maxLen characters, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
