package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type pairResult struct {
	count  int
	source string
	err    error
}

const (
	updateItem  = "Update skill"
	pairItem    = "Link GitHub source"
	detailsItem = "View details"
)

func (m screen) menuItems() []string {
	var items []string
	if m.canUpdateSelected() {
		items = append(items, updateItem)
	}
	if !m.busy && m.selected != nil && m.selected.manager == "Unknown" {
		items = append(items, pairItem)
	}
	return append(items, detailsItem)
}

func (m screen) handleMenu(msg tea.KeyPressMsg) (screen, tea.Cmd) {
	items := m.menuItems()
	index := min(m.menuIndex, len(items)-1)
	switch msg.String() {
	case "down", "j":
		m.menuIndex = min(len(items)-1, index+1)
	case "up", "k":
		m.menuIndex = max(0, index-1)
	case "enter":
		if msg.IsRepeat {
			return m, nil
		}
		switch items[index] {
		case updateItem:
			m.beginSelectedUpdate()
		case pairItem:
			m.panel = sourcePanel
			m.err = nil
			m.sourceInput = newSearch()
			m.sourceInput.Prompt = "› "
			m.sourceInput.Placeholder = "owner/repository"
			width, _ := m.size()
			m.sourceInput.SetWidth(max(1, min(width-6, 44)))
			cmd := m.sourceInput.Focus()
			return m, cmd
		case detailsItem:
			m.panel, m.panelScroll = skillPanel, 0
		}
	}
	return m, nil
}

func (m screen) handleSource(msg tea.KeyPressMsg) (screen, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.panel = menuPanel
		m.err = nil
		return m, nil
	case "enter":
		if msg.IsRepeat {
			return m, nil
		}
		source, err := githubSource(m.sourceInput.Value())
		if err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		m.busy, m.pairing = true, true
		m.activity = newActivity()
		selected := m.selected.path
		return m, tea.Batch(m.activity.Tick, func() tea.Msg {
			n, err := pairSource(m.ctx, m.cfg, selected, source)
			return pairResult{n, source, err}
		})
	}
	m.err = nil
	var cmd tea.Cmd
	m.sourceInput, cmd = m.sourceInput.Update(msg)
	return m, cmd
}

func (m screen) popupView() string {
	width, height := m.size()
	lines := strings.Split(m.dashboardView(), "\n")
	anchor := 0
	for i, line := range lines {
		if strings.HasPrefix(ansi.Strip(line), "› ") || m.busy && strings.HasPrefix(ansi.Strip(line), ansi.Strip(m.activityCursor())) {
			anchor = i
			break
		}
	}
	var contents []string
	boxWidth := min(width, 26)
	if m.panel == sourcePanel {
		boxWidth = min(width, 52)
		contents = []string{panelTitle("Link GitHub source"), strings.TrimSpace(m.sourceInput.View())}
		if m.pairing {
			contents = append(contents, m.activityLine())
		} else {
			if m.err != nil {
				contents = append(contents, wrapLines([]string{printable(m.err.Error())}, max(1, boxWidth-4))...)
			}
			contents = append(contents, wrapLines([]string{"Links all verified matches in this repository."}, max(1, boxWidth-4))...)
			contents = append(contents, accent("Enter link · Esc back"))
		}
	} else {
		items := m.menuItems()
		for i, item := range items {
			label := "  " + item
			if i == min(m.menuIndex, len(items)-1) {
				label = highlighted("› " + item)
			}
			contents = append(contents, label)
		}
	}
	if boxWidth < 5 || height < 3 {
		return fitLines(contents, width, height)
	}
	inner := boxWidth - 4
	contents = contents[:min(len(contents), height-2)]
	box := []string{muted("╭" + strings.Repeat("─", boxWidth-2) + "╮")}
	for _, line := range contents {
		line = ansi.Truncate(line, inner, "…")
		box = append(box, muted("│ ")+line+strings.Repeat(" ", inner-ansi.StringWidth(line))+muted(" │"))
	}
	box = append(box, muted("╰"+strings.Repeat("─", boxWidth-2)+"╯"))
	top := anchor + 1
	if top+len(box) > height-1 {
		top = max(0, anchor-len(box))
	}
	x := min(3, width-boxWidth)
	for i, line := range box {
		y := top + i
		base := lines[y] + strings.Repeat(" ", width)
		lines[y] = ansi.Cut(base, 0, x) + line + ansi.Cut(base, x+boxWidth, width)
	}
	if !m.busy && top+len(box) < height && m.panel == menuPanel {
		lines[height-1] = ansi.Truncate("↑/↓ move · Enter pick · Esc back", width, "…")
	}
	return strings.Join(lines, "\n")
}
