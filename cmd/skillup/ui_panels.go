package main

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type panelMode int

const (
	dashboardPanel panelMode = iota
	skillPanel
	commandPanel
	helpPanel
	menuPanel
	sourcePanel
	panelMaxWidth = 74
)

type commandAction int

const (
	commandSelected commandAction = iota
	commandRefresh
	commandAll
	commandSearch
	commandHelp
)

type panelCommand struct {
	action commandAction
	label  string
	key    string
}

func (m screen) canUpdateSelected() bool {
	return !m.busy && m.selected != nil && m.selected.status == available
}

func (m *screen) beginSelectedUpdate() {
	if !m.canUpdateSelected() {
		return
	}
	scope := scopeFor(m.selected, m.rows)
	m.selection = skillSelection{name: m.selected.name, path: m.selected.path, approved: &scope}
	m.confirm, m.confirmOffset = true, 0
}

func (m *screen) beginAllUpdates() {
	if !m.busy && slices.ContainsFunc(m.rows, func(row *skill) bool { return row.status == available }) {
		m.confirm, m.confirmOffset = true, 0
	}
}

func (m screen) focusSearch() (screen, tea.Cmd) {
	m.panel, m.autoSelect = dashboardPanel, false
	cmd := m.search.Focus()
	return m, cmd
}

func resizeInput(input *textinput.Model, width int) {
	position := input.Position()
	input.SetWidth(width)
	input.CursorEnd()
	input.SetCursor(position)
}

func (m screen) openCommands() (screen, tea.Cmd) {
	m.search.Blur()
	m.panel, m.commandReturn, m.commandIndex = commandPanel, m.panel, 0
	m.autoSelect = false
	m.commandSearch = newSearch()
	m.commandSearch.Prompt = "› "
	m.commandSearch.Placeholder = "Type to filter actions"
	width, _ := m.size()
	resizeInput(&m.commandSearch, max(1, min(width, panelMaxWidth)-3))
	cmd := m.commandSearch.Focus()
	return m, cmd
}

func (m screen) commands() []panelCommand {
	var commands []panelCommand
	if m.canUpdateSelected() {
		commands = append(commands, panelCommand{commandSelected, "Update selected skill", "u"})
	}
	if !m.busy {
		commands = append(commands, panelCommand{commandRefresh, "Check for updates", "r"})
		if slices.ContainsFunc(m.rows, func(row *skill) bool { return row.status == available }) {
			commands = append(commands, panelCommand{commandAll, "Update all available skills", "a"})
		}
	}
	commands = append(commands, panelCommand{commandSearch, "Search skills", "/"}, panelCommand{commandHelp, "Show keyboard shortcuts", "?"})
	query := strings.ToLower(strings.TrimSpace(m.commandSearch.Value()))
	return slices.DeleteFunc(commands, func(command panelCommand) bool {
		return !strings.Contains(strings.ToLower(command.label), query)
	})
}

func (m screen) editCommands(msg tea.Msg) (screen, tea.Cmd) {
	before := m.commandSearch.Value()
	var cmd tea.Cmd
	m.commandSearch, cmd = m.commandSearch.Update(msg)
	if m.commandSearch.Value() != before {
		m.commandIndex = 0
	}
	return m, cmd
}

func (m screen) handlePanel(msg tea.KeyPressMsg) (screen, tea.Cmd) {
	key := msg.String()
	if key == "esc" || m.panel == helpPanel && key == "?" {
		if m.panel == commandPanel {
			m.panel = m.commandReturn
		} else {
			m.panel = dashboardPanel
		}
		return m, nil
	}
	if m.panel == menuPanel {
		return m.handleMenu(msg)
	}
	if m.panel == commandPanel {
		commands := m.commands()
		index := min(m.commandIndex, max(0, len(commands)-1))
		switch key {
		case "up":
			m.commandIndex = max(0, index-1)
		case "down":
			m.commandIndex = min(index+1, max(0, len(commands)-1))
		case "enter":
			if len(commands) > 0 && !msg.IsRepeat {
				return m.runCommand(commands[index].action)
			}
		default:
			return m.editCommands(msg)
		}
		return m, nil
	}
	if m.panel == skillPanel && key == "enter" && !msg.IsRepeat {
		m.beginSelectedUpdate()
		return m, nil
	}
	view := m.panelViewport()
	switch key {
	case "home":
		view.GotoTop()
	case "end":
		view.GotoBottom()
	default:
		next, cmd := view.Update(msg)
		m.panelScroll = next.YOffset()
		return m, cmd
	}
	m.panelScroll = view.YOffset()
	return m, nil
}

func (m screen) runCommand(action commandAction) (screen, tea.Cmd) {
	m.panel = dashboardPanel
	switch action {
	case commandSelected:
		m.beginSelectedUpdate()
	case commandRefresh:
		return m.start(false)
	case commandAll:
		m.beginAllUpdates()
	case commandSearch:
		return m.focusSearch()
	case commandHelp:
		m.panel, m.panelScroll = helpPanel, 0
	}
	return m, nil
}

func (m screen) handleConfirmation(msg tea.KeyPressMsg) (screen, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		width, height := m.size()
		if !msg.IsRepeat && width >= 18 && height >= 3 &&
			(msg.String() == "y" || m.enterConfirms()) {
			m.confirm = false
			return m.start(true)
		}
	case "n", "esc", "q", "ctrl+c":
		m.confirm = false
		m.selection = skillSelection{}
	case "down", "j", "up", "k":
		_, _, maxOffset := m.confirmationPage()
		delta := 1
		if msg.String() == "up" || msg.String() == "k" {
			delta = -1
		}
		m.confirmOffset = min(maxOffset, max(0, m.confirmOffset+delta))
	}
	return m, nil
}

func (m screen) enterConfirms() bool {
	return m.selection.approved != nil && m.selection.approved.checkout == ""
}

func (m screen) confirmationPage() (details []string, room, maxOffset int) {
	width, height := m.size()
	lines := []string{"All available updates", "Includes skills hidden by search or filters."}
	if scope := m.selection.approved; scope != nil {
		lines = scope.installationDescriptions()
		if scope.checkout != "" {
			affected := []string{"Whole Git checkout"}
			for _, row := range scope.rows {
				affected = append(affected, "• "+row.name)
			}
			affected = append(affected, "These skills update together.", "Checkout  "+scope.checkout)
			lines = append(affected, lines...)
		}
	}
	details = wrapLines(lines, min(width, panelMaxWidth))
	room = max(0, height-1)
	if height >= 5 {
		room-- // Keep the dialog title separate from the scrollable scope.
	}
	if len(details) > room && height >= 4 {
		room--
	}
	return details, room, max(0, len(details)-max(1, room))
}

func (m screen) confirmationView() string {
	width, height := m.size()
	details, room, maxOffset := m.confirmationPage()
	controls := "y apply · Esc back"
	if m.enterConfirms() {
		controls = "Enter apply · Esc back"
	}
	if width < 18 || height < 3 {
		return centeredPanel([]string{"Esc back"}, width, height)
	}
	if width < 24 && m.enterConfirms() {
		controls = "↵ apply · Esc back"
	}
	offset := min(m.confirmOffset, maxOffset)
	lines := []string{accent(controls)}
	if height >= 5 {
		lines = append([]string{panelTitle("Confirm update")}, lines...)
	}
	if maxOffset > 0 && height >= 4 {
		lines = append(lines, muted("↑/↓ scroll scope"))
	}
	lines = append(lines, details[offset:min(len(details), offset+room)]...)
	return centeredPanel(lines, width, height)
}

func updateNotice(rows []*skill) string {
	var names []string
	var failures int
	var lastFailure *skill
	for _, row := range rows {
		switch row.status {
		case updated:
			names = append(names, printable(row.name))
		case failed:
			failures++
			lastFailure = row
		}
	}
	switch {
	case failures == 1 && len(names) == 0:
		return "Update failed for " + printable(lastFailure.name) + " · " + printable(lastFailure.detail)
	case failures > 0:
		return fmt.Sprintf("%d updated · %d failed", len(names), failures)
	case len(names) == 0:
		return "No skills updated"
	case len(names) == 1:
		return "Updated " + names[0]
	default:
		return fmt.Sprintf("%d skills updated", len(names))
	}
}

func (m screen) panelViewport() viewport.Model {
	width, height := m.size()
	width = min(width, panelMaxWidth)
	var lines []string
	header := 3
	if m.panel == helpPanel {
		header = 2
		lines = []string{
			"Browse skills", "↑/↓    Move between skills", "Enter  Open actions and details", "/      Search by name or location",
			"f      Switch the status filter", "Esc    Clear the search or filter",
			"", "Updates", "u      Review the selected update", "a      Review all available updates",
			"r      Check for updates again", "", "Navigation", "Ctrl+P Open the command palette",
			"?      Show keyboard shortcuts", "Esc    Go back or cancel", "q      Quit",
		}
	} else if m.selected != nil {
		lines = skillDetails(m.selected, "")
	}
	if m.busy {
		header++
	}
	view := viewport.New(viewport.WithWidth(width), viewport.WithHeight(max(0, height-header)))
	view.SetContentLines(wrapLines(lines, width))
	view.SetYOffset(m.panelScroll)
	return view
}

func (m screen) detailsView() string {
	width, height := m.size()
	if height < 3 {
		return centeredPanel([]string{"Esc back"}, width, height)
	}
	view := m.panelViewport()
	title := "Keyboard shortcuts"
	var header []string
	controls := "Esc back · ↑/↓ scroll"
	if m.panel == skillPanel && m.selected != nil {
		title = printable(m.selected.name)
		header = append(header, statusBadge(m.selected.status, width < 25))
		if m.canUpdateSelected() {
			switch {
			case width >= 45:
				controls = "Enter Update skill · Esc back · ↑/↓ scroll"
			case width >= 30:
				controls = "Enter Update skill · Esc back"
			case width >= 19:
				controls = "↵ update · Esc back"
			default:
				controls = "Esc back"
			}
		}
	}
	lines := append([]string{panelTitle(title)}, header...)
	cue := accent(controls)
	if m.canUpdateSelected() && m.panel == skillPanel && width >= 30 {
		cue = highlighted("Enter") + accent(" Update skill") + strings.TrimPrefix(controls, "Enter Update skill")
	}
	lines = append(lines, cue)
	if m.busy {
		lines = append(lines, m.activityLine())
	}
	if view.Height() > 0 {
		lines = append(lines, strings.Split(view.View(), "\n")...)
	}
	return centeredPanel(lines, width, height)
}

func (m screen) commandsView() string {
	width, height := m.size()
	if height < 3 {
		return centeredPanel([]string{"Esc back"}, width, height)
	}
	innerWidth := min(width, panelMaxWidth)
	lines := []string{panelTitle("Command palette"), strings.TrimRight(m.commandSearch.View(), " ")}
	footer := "↑/↓ choose · Enter · Esc back"
	if width < 30 {
		footer = "↵ choose · Esc back"
	}
	lines = append(lines, muted(footer))
	if m.busy {
		lines = append(lines, m.activityLine())
	}
	commands := m.commands()
	if len(commands) == 0 {
		lines = append(lines, "No matching actions")
	}
	index := min(m.commandIndex, max(0, len(commands)-1))
	space := max(0, height-len(lines))
	start := max(0, index-space+1)
	for i := start; i < min(len(commands), start+space); i++ {
		command := commands[i]
		cursor := "  "
		if i == index {
			cursor = "› "
		}
		line := spaced(cursor+command.label, command.key, innerWidth)
		if i == index {
			line = highlighted(line)
		}
		lines = append(lines, line)
	}
	return centeredPanel(lines, width, height)
}

func centeredPanel(lines []string, width, height int) string {
	innerWidth := min(width, panelMaxWidth)
	inset := strings.Repeat(" ", (width-innerWidth)/2)
	for i, line := range lines[:min(height, len(lines))] {
		lines[i] = inset + strings.TrimRight(ansi.Truncate(line, innerWidth, "…"), " ")
	}
	return strings.Join(lines[:min(height, len(lines))], "\n")
}

func panelTitle(title string) string {
	return styled(title, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")))
}
