package main

import (
	"context"
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type screen struct {
	cfg                     config
	ctx                     context.Context
	cancel                  context.CancelFunc
	rows                    []*skill
	selected                *skill
	width, height           int
	busy, confirm, applying bool
	checking, autoSelect    bool
	selection               skillSelection
	confirmOffset           int
	err                     error
	activity                spinner.Model
	search                  textinput.Model
	filter, notice          string
	panel, commandReturn    panelMode
	panelScroll             int
	commandSearch           textinput.Model
	commandIndex            int
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
	selected := m.selected
	if selected != nil {
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
	if m.panel == skillPanel && (m.selected == nil || selected == nil ||
		m.selected.path != selected.path || m.selected.name != selected.name) {
		m.panel = dashboardPanel
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
	m.busy, m.applying, m.checking, m.err = true, apply, false, nil
	m.panel, m.notice = dashboardPanel, ""
	m.activity = newActivity()
	return m, tea.Batch(m.activity.Tick, m.load(apply))
}

func (m screen) finish(result scanResult) (screen, tea.Cmd) {
	if m.ctx.Err() != nil {
		return m, tea.Quit
	}
	if m.applying && result.err == nil {
		m.notice = updateNotice(result.rows)
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
	if m.autoSelect && result.err == nil {
		if index := slices.IndexFunc(result.rows, func(row *skill) bool { return row.status == available }); index >= 0 {
			m.selected = result.rows[index]
		}
	}
	m.autoSelect = false
	m.selection = skillSelection{}
	m.err, m.busy, m.applying, m.checking = result.err, false, false, false
	m.setRows(result.rows)
	return m, nil
}

func (m screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		resizeInput(&m.search, max(1, m.width-3))
		resizeInput(&m.commandSearch, max(1, min(m.width, panelMaxWidth)-3))
		if m.panel == skillPanel || m.panel == helpPanel {
			view := m.panelViewport()
			m.panelScroll = view.YOffset()
		}
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
			return m.handleConfirmation(msg)
		}
		if (key == "ctrl+c" || key == "q" && !m.search.Focused() && m.panel != commandPanel) && !m.applying {
			if m.busy {
				m.cancel()
				return m, nil
			}
			return m, tea.Quit
		}
		if m.applying || m.busy && m.ctx.Err() != nil {
			return m, nil
		}
		if key == "ctrl+p" {
			if m.panel == commandPanel {
				m.panel = m.commandReturn
				return m, nil
			}
			return m.openCommands()
		}
		if m.panel != dashboardPanel {
			return m.handlePanel(msg)
		}
		if key == "esc" {
			if m.search.Focused() || m.search.Value() != "" {
				m.search.Blur()
				m.search.Reset()
			} else {
				m.filter = ""
			}
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
			m.autoSelect = false
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
		case "enter":
			if !msg.IsRepeat {
				m.autoSelect = false
				m.keepSelection()
				if m.selected != nil {
					m.panel, m.panelScroll = skillPanel, 0
				}
			}
		case "?":
			m.panel, m.panelScroll = helpPanel, 0
		case "/":
			return m.focusSearch()
		case "f":
			m.autoSelect = false
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
			m.beginSelectedUpdate()
		case "a":
			m.beginAllUpdates()
		}
	default:
		if m.panel == commandPanel {
			return m.editCommands(msg)
		}
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
	view.KeyboardEnhancements.ReportEventTypes = true
	return view
}

func (m screen) size() (int, int) {
	if m.width == 0 {
		return 80, 24
	}
	return m.width, m.height
}

func styled(text string, style lipgloss.Style) string {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return text
	}
	return style.Render(text)
}
