package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type screen struct {
	cfg                     config
	ctx                     context.Context
	cancel                  context.CancelFunc
	rows                    []*skill
	selected                *skill
	width, height           int
	busy, confirm, applying bool
	err                     error
	activity                spinner.Model
	search                  textinput.Model
	filter                  string
}

type scanResult struct {
	rows []*skill
	err  error
}

func (m screen) load(apply bool) tea.Cmd {
	return func() tea.Msg {
		rows, err := scan(m.ctx, m.cfg, apply)
		return scanResult{rows, err}
	}
}

func (m screen) Init() tea.Cmd {
	return tea.Batch(m.activity.Tick, m.load(false))
}

func newActivity() spinner.Model {
	activity := spinner.New(spinner.WithSpinner(spinner.Dot))
	if _, noColor := os.LookupEnv("NO_COLOR"); !noColor {
		activity.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	}
	return activity
}

func newSearch() textinput.Model {
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "Name or location"
	search.SetWidth(77)
	styles := textinput.Styles{}
	if _, noColor := os.LookupEnv("NO_COLOR"); !noColor {
		styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		styles.Blurred.Prompt = styles.Focused.Prompt
	}
	search.SetStyles(styles)
	return search
}

func (m screen) visibleRows() []*skill {
	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	if query == "" && m.filter == "" {
		return m.rows
	}
	matches := func(text string) bool {
		return strings.Contains(strings.ToLower(text), query)
	}
	var rows []*skill
	for _, row := range m.rows {
		if m.filter != "" && row.status != m.filter {
			continue
		}
		if matches(row.name) || slices.ContainsFunc(row.locations, func(loc location) bool {
			return matches(loc.label) || matches(loc.path)
		}) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m *screen) keepSelection() {
	rows := m.visibleRows()
	if len(rows) == 0 {
		m.selected = nil
	} else if !slices.Contains(rows, m.selected) {
		m.selected = rows[0]
	}
}

func (m screen) editSearch(msg tea.Msg) (screen, tea.Cmd) {
	var cmd tea.Cmd
	before := m.search.Value()
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != before {
		m.keepSelection()
	}
	return m, cmd
}

func (m screen) start(apply bool) (screen, tea.Cmd) {
	m.busy, m.applying, m.err = true, apply, nil
	m.activity = newActivity()
	return m, tea.Batch(m.activity.Tick, m.load(apply))
}

func (m screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		position := m.search.Position()
		m.search.SetWidth(max(1, m.width-3))
		m.search.CursorEnd()
		m.search.SetCursor(position)
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.activity, cmd = m.activity.Update(msg)
			return m, cmd
		}
	case scanResult:
		if m.ctx.Err() != nil {
			return m, tea.Quit
		}
		m.rows, m.err, m.busy, m.applying = msg.rows, msg.err, false, false
		m.selected = nil
	case tea.KeyPressMsg:
		key := msg.String()
		if m.confirm {
			m.confirm = false
			if key == "y" {
				return m.start(true)
			}
			return m, nil
		}
		if (key == "ctrl+c" || key == "q" && !m.search.Focused()) && !m.applying {
			if m.busy {
				m.cancel()
				return m, nil
			}
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		if key == "esc" {
			m.search.Blur()
			m.search.Reset()
			m.keepSelection()
			return m, nil
		}
		if m.search.Focused() {
			if key == "enter" {
				m.search.Blur()
				return m, nil
			}
			return m.editSearch(msg)
		}
		switch key {
		case "down", "j", "up", "k", "home", "end":
			rows := m.visibleRows()
			if len(rows) == 0 {
				return m, nil
			}
			index := max(0, slices.Index(rows, m.selected))
			switch key {
			case "down", "j":
				index = min(index+1, len(rows)-1)
			case "up", "k":
				index = max(0, index-1)
			case "home":
				index = 0
			case "end":
				index = len(rows) - 1
			}
			m.selected = rows[index]
		case "/":
			cmd := m.search.Focus()
			return m, cmd
		case "f":
			switch m.filter {
			case "":
				m.filter = available
			case available:
				m.filter = modified
			case modified:
				m.filter = ""
			}
			m.keepSelection()
		case "r":
			return m.start(false)
		case "u":
			for _, row := range m.rows {
				if row.status == available {
					m.confirm = true
					break
				}
			}
		}
	default:
		if m.search.Focused() {
			return m.editSearch(msg)
		}
	}
	return m, nil
}

func (m screen) View() tea.View {
	view := tea.NewView(m.content())
	view.AltScreen = true
	view.WindowTitle = "skillup"
	return view
}

func (m screen) content() string {
	width, height := m.width, m.height
	if width == 0 {
		width, height = 80, 24
	}
	title := styled("skillup", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")))
	lines := []string{ansi.Truncate(title, width, "…")}
	help := "u update all · ↑/↓ select · r refresh · q quit"
	switch {
	case m.confirm:
		help = "Update all? y/n"
	case m.applying:
		help = m.activity.View() + " Applying updates…"
	case m.busy:
		help = m.activity.View() + " Checking installed skills and sources… · q quit"
		if m.ctx != nil && m.ctx.Err() != nil {
			help = m.activity.View() + " Cancelling…"
		}
	case m.search.Focused():
		help = "↵ keep · esc clear"
	}
	lines = append(lines, ansi.Truncate(help, width, "…"))
	if height < 3 || width < 12 {
		return strings.Join(lines[:min(height, len(lines))], "\n")
	}
	if !m.busy {
		filter := m.filter
		if filter == "" {
			filter = "All"
		}
		lines = append(lines, ansi.Truncate("f "+filter+" · / search", width, "…"))
		if m.search.Focused() || m.search.Value() != "" {
			lines = append(lines, ansi.Truncate(strings.TrimRight(m.search.View(), " "), width, "…"))
		}
	}
	if m.err != nil {
		lines = append(lines, ansi.Truncate(printable(m.err.Error()), width, "…"))
	}
	rows := m.visibleRows()
	if len(rows) == 0 {
		if !m.busy && m.err == nil {
			message := "No user skills found"
			if m.filter != "" || strings.TrimSpace(m.search.Value()) != "" {
				message = "No matching skills"
			}
			lines = append(lines, ansi.Truncate(message, width, "…"))
		}
		return strings.Join(lines[:min(len(lines), height)], "\n")
	}
	status := summary(rows)
	if len(rows) != len(m.rows) {
		status = fmt.Sprintf("%d of %d skills", len(rows), len(m.rows))
	}
	statusLines := strings.Split(ansi.Hardwrap(status, width, false), "\n")
	if len(statusLines) > max(1, height-len(lines)-3) {
		statusLines = []string{ansi.Truncate(status, width, "…")}
	}
	lines = append(lines, statusLines...)

	selected := max(0, slices.Index(rows, m.selected))
	row := rows[selected]
	details := []string{row.name + " · " + row.status + " · " + row.detail}
	if row.source != "" {
		details = append(details, "Source  "+row.source)
	}
	if row.current != "" {
		revisions := short(row.current)
		if row.latest != "" && row.latest != row.current {
			revisions += " → " + short(row.latest)
		}
		details = append(details, "Revision  "+revisions)
	}
	for _, loc := range row.locations {
		details = append(details, loc.label+"  "+loc.path)
	}

	var wrapped []string
	for _, detail := range details {
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(printable(detail), width, false), "\n")...)
	}
	room := max(0, height-len(lines)-2)
	wrapped = wrapped[:min(len(wrapped), room/2)]
	count := min(len(rows), room-len(wrapped))
	start := max(0, selected-count+1)
	if count > 0 {
		nameWidth := min(36, max(10, width-14))
		headers := "  " + column("Skill", nameWidth) + "Status      Location"
		lines = append(lines, ansi.Truncate(styled(headers, lipgloss.NewStyle().Bold(true)), width, "…"))
		for i := start; i < start+count; i++ {
			row := rows[i]
			var labels []string
			for _, loc := range row.locations {
				labels = append(labels, loc.label)
			}
			cursor := "  "
			name := column(row.name, nameWidth)
			if i == selected {
				cursor = styled("› ", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")))
				name = styled(name, lipgloss.NewStyle().Bold(true).Underline(true))
			}
			line := cursor + name + statusText(row.status) + strings.Join(labels, ", ")
			lines = append(lines, ansi.Truncate(line, width, "…"))
		}
	}
	lines = append(lines, styled(strings.Repeat("─", width), lipgloss.NewStyle().Foreground(lipgloss.Color("8"))))
	if len(wrapped) > 0 {
		wrapped[0] = styled(wrapped[0], lipgloss.NewStyle().Bold(true))
	}
	lines = append(lines, wrapped...)
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

func column(text string, width int) string {
	text = ansi.Truncate(printable(text), width-1, "…")
	return text + strings.Repeat(" ", width-ansi.StringWidth(text))
}

func statusText(status string) string {
	text := column(status, 12)
	var color string
	switch status {
	case available:
		color = "3"
	case current, updated:
		color = "2"
	case modified:
		color = "5"
	case failed:
		color = "1"
	default:
		return text
	}
	return styled(text, lipgloss.NewStyle().Foreground(lipgloss.Color(color)))
}

func styled(text string, style lipgloss.Style) string {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return text
	}
	return style.Render(text)
}
