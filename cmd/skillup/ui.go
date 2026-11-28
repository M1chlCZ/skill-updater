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
	checking                bool
	selection               skillSelection
	confirmOffset           int
	err                     error
	activity                spinner.Model
	search                  textinput.Model
	filter                  string
}

type scanResult struct {
	rows []*skill
	err  error
}

type inventoryResult struct {
	rows       []*skill
	byLocation map[string]*skill
	err        error
}

const (
	checkingStatus  = "Checking"
	uncheckedStatus = "Unchecked"
)

func (m screen) load(apply bool) tea.Cmd {
	return func() tea.Msg {
		if apply {
			rows, err := scan(m.ctx, m.cfg, true, m.selection)
			return scanResult{rows, err}
		}
		rows, byLocation, err := inventory(m.ctx, m.cfg)
		return inventoryResult{rows, byLocation, err}
	}
}

func (m screen) check(local inventoryResult) tea.Cmd {
	return func() tea.Msg {
		lock, err := readLock(m.cfg.lock)
		if err != nil {
			return scanResult{local.rows, err}
		}
		rows, installs := catalog(m.cfg, lock, local.rows, local.byLocation)
		rows, err = inspectSources(m.ctx, lock, rows, rows, installs, false, "")
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
		if m.filter != "" && row.status != m.filter && row.status != checkingStatus && row.status != uncheckedStatus {
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

func (m *screen) setRows(rows []*skill) {
	if selected := m.selected; selected != nil {
		m.selected = nil
		for _, row := range rows {
			if row.path == selected.path && row.name == selected.name {
				m.selected = row
				break
			}
		}
	}
	m.rows = rows
	m.keepSelection()
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
	m.busy, m.applying, m.checking, m.err = true, apply, false, nil
	m.activity = newActivity()
	return m, tea.Batch(m.activity.Tick, m.load(apply))
}

func (m screen) finish(result scanResult) (screen, tea.Cmd) {
	if m.ctx.Err() != nil {
		return m, tea.Quit
	}
	if result.err != nil && !m.applying {
		for _, row := range result.rows {
			row.mark(uncheckedStatus, "Could not complete the source check")
		}
	}
	if m.applying && result.err != nil && result.rows == nil {
		result.rows = m.rows
	} else if m.selection.approved != nil {
		rows := slices.Clone(m.rows)
		for _, updated := range result.rows {
			if index := slices.IndexFunc(rows, func(row *skill) bool {
				return row.name == updated.name && row.path == updated.path
			}); index >= 0 {
				rows[index] = updated
			}
		}
		sortRows(rows)
		result.rows = rows
	}
	m.selection = skillSelection{}
	m.err, m.busy, m.applying, m.checking = result.err, false, false, false
	m.setRows(result.rows)
	return m, nil
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
	case inventoryResult:
		if m.ctx.Err() != nil {
			return m, tea.Quit
		}
		if msg.err != nil {
			return m.finish(scanResult{msg.rows, msg.err})
		}
		// Source checks mutate the inventory rows. The view uses its own copies.
		rows := make([]*skill, len(msg.rows))
		for i, row := range msg.rows {
			preview := *row
			preview.mark(checkingStatus, "Waiting for source status")
			rows[i] = &preview
		}
		m.checking = true
		m.setRows(rows)
		return m, m.check(msg)
	case scanResult:
		return m.finish(msg)
	case tea.KeyPressMsg:
		key := msg.String()
		if m.confirm {
			switch key {
			case "y":
				_, height := m.size()
				if m.selection.approved != nil && height < 2 {
					return m, nil
				}
				m.confirm = false
				return m.start(true)
			case "n", "esc", "q", "ctrl+c":
				m.confirm = false
				m.selection = skillSelection{}
			case "down", "j":
				if m.selection.approved != nil {
					_, _, maxOffset := m.confirmationPage()
					m.confirmOffset = min(m.confirmOffset+1, maxOffset)
				}
			case "up", "k":
				m.confirmOffset = max(0, m.confirmOffset-1)
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
		if m.applying || m.busy && m.ctx.Err() != nil {
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
			if !m.busy {
				return m.start(false)
			}
		case "u":
			if !m.busy && m.selected != nil && m.selected.status == available {
				scope := scopeFor(m.selected, m.rows)
				m.selection = skillSelection{name: m.selected.name, path: m.selected.path, approved: &scope}
				m.confirm = true
				m.confirmOffset = 0
			}
		case "a":
			if m.busy {
				return m, nil
			}
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

func (m screen) size() (int, int) {
	if m.width == 0 {
		return 80, 24
	}
	return m.width, m.height
}

func (m screen) content() string {
	width, height := m.size()
	title := styled("skillup", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")))
	lines := []string{ansi.Truncate(title, width, "…")}
	if m.confirm && m.selection.approved != nil {
		return m.confirmation(lines)
	}
	help := "u update selected · a update all · ↑/↓ select · r refresh · q quit"
	if width < 60 {
		help = "u update · a all · r · q quit"
	}
	switch {
	case m.confirm:
		help = "Update all? y/n"
	case m.applying:
		label := "Applying updates…"
		if scope := m.selection.approved; scope != nil {
			label = "Updating skill…"
			if scope.checkout != "" {
				label = "Updating checkout…"
			}
		}
		help = m.activity.View() + " " + label
	case m.busy:
		quit := "q quit"
		if m.search.Focused() {
			quit = "^C quit"
		}
		label := "Reading skills…"
		if m.checking {
			label = "Checking sources…"
		}
		if m.ctx != nil && m.ctx.Err() != nil {
			label = "Cancelling…"
		}
		help = quit + " · " + m.activity.View() + " " + label
	case m.search.Focused():
		help = "↵ keep · esc clear"
	}
	lines = append(lines, ansi.Truncate(help, width, "…"))
	if height < 3 || width < 12 {
		return strings.Join(lines[:min(height, len(lines))], "\n")
	}
	if !m.applying {
		filter := m.filter
		if filter == "" {
			filter = "All"
		}
		controls := "f " + filter + " · / search"
		if m.busy && m.search.Focused() {
			controls = "↵ keep · esc clear"
		}
		lines = append(lines, ansi.Truncate(controls, width, "…"))
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
	} else if m.busy && !m.applying {
		status = fmt.Sprintf("%d skills", len(rows))
	}
	statusLines := strings.Split(ansi.Hardwrap(status, width, false), "\n")
	if len(statusLines) > max(1, height-len(lines)-3) {
		statusLines = []string{ansi.Truncate(status, width, "…")}
	}
	lines = append(lines, statusLines...)

	selected := max(0, slices.Index(rows, m.selected))
	row := rows[selected]
	details := []string{row.name + " · " + row.status}
	if row.manager != "" {
		details = append(details, "Manager  "+row.manager)
	}
	if row.detail != "" {
		label := "Status"
		if row.status == skipped || row.status == "Pinned" {
			label = "Reason"
		}
		details = append(details, label+"  "+row.detail)
	}
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

	room := max(0, height-len(lines))
	if width >= 90 && room > 0 {
		listWidth := width * 3 / 5
		list := skillList(rows, selected, listWidth, room)
		detail := wrapLines(details, width-listWidth-3)
		detail[0] = styled(detail[0], lipgloss.NewStyle().Bold(true))
		for i := range min(room, max(len(list), len(detail))) {
			left, right := "", ""
			if i < len(list) {
				left = list[i]
			}
			if i < len(detail) {
				right = detail[i]
			}
			lines = append(lines, left+strings.Repeat(" ", listWidth-ansi.StringWidth(left))+" │ "+right)
		}
	} else {
		// The selected row already shows its name and status in the list.
		detail := wrapLines(details[1:], width)
		detailRoom := min(max(0, room-3), max(2, (room-3)/3, room-len(rows)-2))
		detail = detail[:min(len(detail), detailRoom)]
		listRoom := room
		if len(detail) > 0 {
			listRoom -= len(detail) + 1
		}
		lines = append(lines, skillList(rows, selected, width, listRoom)...)
		if len(detail) > 0 {
			detail[0] = styled(detail[0], lipgloss.NewStyle().Bold(true))
			lines = append(lines, styled(strings.Repeat("─", width), lipgloss.NewStyle().Foreground(lipgloss.Color("8"))))
			lines = append(lines, detail...)
		}
	}
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

func wrapLines(lines []string, width int) []string {
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(printable(line), width, false), "\n")...)
	}
	return wrapped
}

func (m screen) confirmationPage() (details []string, room, maxOffset int) {
	width, height := m.size()
	details = wrapLines(m.selection.approved.description(), width)
	room = height - 2
	if height < 4 {
		room = height - 1
	}
	if len(details) > room && room > 1 {
		room-- // Leave a line for the scroll hint.
	}
	return details, max(0, room), max(0, len(details)-max(1, room))
}

func (m screen) confirmation(lines []string) string {
	width, height := m.size()
	details, room, maxOffset := m.confirmationPage()
	controls := "y apply · n/esc cancel"
	switch {
	case height < 2:
		controls = "n/esc cancel"
	case height == 2 && maxOffset > 0:
		controls = "y/n/esc · ↑/↓"
	case width < 22:
		controls = "y yes · n/esc no"
	}
	if height < 4 {
		lines = nil
	}
	lines = append(lines, ansi.Truncate(controls, width, "…"))
	if maxOffset > 0 && height >= 3 {
		lines = append(lines, ansi.Truncate("↑/↓ scroll scope", width, "…"))
	}
	offset := min(m.confirmOffset, maxOffset)
	lines = append(lines, details[offset:min(len(details), offset+room)]...)
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

func skillList(rows []*skill, selected, width, height int) []string {
	count := min(len(rows), height-1)
	if count < 1 {
		return nil
	}
	nameWidth := min(36, max(10, width-14))
	headers := "  " + column("Skill", nameWidth) + "Status      Location"
	lines := []string{ansi.Truncate(styled(headers, lipgloss.NewStyle().Bold(true)), width, "…")}
	start := max(0, selected-count+1)
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
	return lines
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
