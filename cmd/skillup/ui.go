package main

import (
	"context"
	"os"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type screen struct {
	cfg                     config
	ctx                     context.Context
	cancel                  context.CancelFunc
	rows                    []*skill
	selected, width, height int
	busy, confirm, applying bool
	err                     error
	activity                spinner.Model
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

func (m screen) start(apply bool) (screen, tea.Cmd) {
	m.busy, m.applying, m.err = true, apply, nil
	m.activity = newActivity()
	return m, tea.Batch(m.activity.Tick, m.load(apply))
}

func (m screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
		m.selected = 0
	case tea.KeyPressMsg:
		key := msg.String()
		if m.confirm {
			m.confirm = false
			if key == "y" {
				return m.start(true)
			}
			return m, nil
		}
		if (key == "q" || key == "ctrl+c") && !m.applying {
			if m.busy {
				m.cancel()
				return m, nil
			}
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		switch key {
		case "down", "j":
			m.selected = max(0, min(m.selected+1, len(m.rows)-1))
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "home":
			m.selected = 0
		case "end":
			m.selected = max(0, len(m.rows)-1)
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
	help := "↑/↓ select · r refresh · u update all · q quit"
	switch {
	case m.confirm:
		help = "Apply all available updates?  y apply · n cancel"
	case m.applying:
		help = m.activity.View() + " Applying updates…"
	case m.busy:
		help = m.activity.View() + " Checking installed skills and sources… · q quit"
		if m.ctx != nil && m.ctx.Err() != nil {
			help = m.activity.View() + " Cancelling…"
		}
	}
	lines = append(lines, ansi.Truncate(help, width, "…"))
	if height < 3 || width < 12 {
		return strings.Join(lines[:min(height, len(lines))], "\n")
	}
	if m.err != nil {
		lines = append(lines, ansi.Truncate(printable(m.err.Error()), width, "…"))
	}
	if len(m.rows) == 0 {
		if !m.busy && m.err == nil {
			lines = append(lines, "No user skills found")
		}
		return strings.Join(lines, "\n")
	}
	lines = append(lines, strings.Split(ansi.Hardwrap(summary(m.rows), width, false), "\n")...)

	row := m.rows[m.selected]
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
	wrapped = wrapped[:min(len(wrapped), room/2+1)]
	count := min(len(m.rows), max(0, room-len(wrapped)))
	start := max(0, m.selected-count+1)
	if count > 0 {
		nameWidth := min(36, max(10, width-14))
		headers := "  " + column("Skill", nameWidth) + "Status      Location"
		lines = append(lines, ansi.Truncate(styled(headers, lipgloss.NewStyle().Bold(true)), width, "…"))
		for i := start; i < start+count; i++ {
			row := m.rows[i]
			var labels []string
			for _, loc := range row.locations {
				labels = append(labels, loc.label)
			}
			cursor := "  "
			name := column(row.name, nameWidth)
			if i == m.selected {
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
