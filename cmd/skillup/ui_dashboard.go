package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m screen) content() string {
	switch {
	case m.confirm:
		return m.confirmationView()
	case m.panel == menuPanel || m.panel == sourcePanel:
		return m.popupView()
	case m.panel == commandPanel:
		return m.commandsView()
	case m.panel == skillPanel || m.panel == helpPanel:
		return m.detailsView()
	default:
		return m.dashboardView()
	}
}

func (m screen) dashboardView() string {
	width, height := m.size()
	rows := m.visibleRows()
	header := []string{spaced(panelTitle("Skillup"), m.dashboardSummary(rows, width), width)}

	footer := m.dashboardKeys(len(rows) != 0, width)
	switch {
	case m.busy:
		footer = m.activityLine()
	case m.search.Focused():
		footer = "Enter done · Esc clear"
		if width < 23 {
			footer = "Esc clear · ↵ done"
		}
	}

	if height > 3 && width >= 12 && !m.applying {
		header = append(header, m.searchAndFilter(width))
	}
	if width >= 80 && height >= 14 {
		header = append(header, "")
	}
	if height > len(header)+1 {
		switch {
		case m.err != nil:
			header = append(header, styled(printable(m.err.Error()), lipgloss.NewStyle().Foreground(lipgloss.Color("1"))))
		case m.notice != "":
			header = append(header, accent(m.notice))
		}
	}

	room := max(0, height-len(header)-1)
	body := m.dashboardBody(rows, width, room)
	if m.busy {
		for i, line := range body {
			if strings.HasPrefix(ansi.Strip(line), "› ") {
				body[i] = m.activityCursor() + ansi.Cut(line, 1, width)
				break
			}
		}
	}
	if len(body) < room {
		body = append(body, make([]string, room-len(body))...)
	}
	if height == 1 {
		return fitLines([]string{footer}, width, height)
	}
	lines := append(header, body...)
	lines = append(lines, footer)
	return fitLines(lines, width, height)
}

func (m screen) dashboardSummary(rows []*skill, width int) string {
	if width < 21 {
		return ""
	}
	if len(rows) != len(m.rows) {
		return fmt.Sprintf("%d of %d skills", len(rows), len(m.rows))
	}
	count := fmt.Sprintf("%d skills", len(rows))
	if len(rows) == 1 {
		count = "1 skill"
	}
	if m.busy {
		return count
	}
	updates := 0
	for _, row := range rows {
		if row.status == available {
			updates++
		}
	}
	if width >= 46 && updates > 0 {
		return fmt.Sprintf("%s  ·  %d to update", count, updates)
	}
	if updates > 0 {
		return fmt.Sprintf("%d to update", updates)
	}
	return count
}

func (m screen) searchAndFilter(width int) string {
	search := "/ Search skills"
	if m.search.Focused() || m.search.Value() != "" {
		search = strings.TrimRight(m.search.View(), " ")
	}
	if m.search.Focused() {
		return search
	}
	filter := "All"
	switch m.filter {
	case available:
		filter = "Updates"
	case modified:
		filter = "Local changes"
	}
	if width >= 72 {
		var tabs []string
		for _, name := range []string{"All", "Updates", "Local changes"} {
			if name == filter {
				tabs = append(tabs, accent("["+name+"]"))
			} else {
				tabs = append(tabs, name)
			}
		}
		return spaced(search, "f  "+strings.Join(tabs, "  "), width)
	}
	if width >= 24 {
		return spaced(search, "f ["+filter+"]", width)
	}
	return search
}

func (m screen) dashboardKeys(hasSkills bool, width int) string {
	if width < 17 {
		return "q quit"
	}
	if !hasSkills {
		if width < 30 {
			return "/ search · q quit"
		}
		return "/ search · ? help · q quit"
	}
	bulk := ""
	if slices.ContainsFunc(m.rows, func(row *skill) bool { return row.status == available }) {
		bulk = " · a update all"
	}
	switch {
	case width >= 100:
		return "Enter actions · ↑/↓ move · / search" + bulk + " · Ctrl+P commands · ? help · q quit"
	case width >= 60:
		return "Enter actions · ↑/↓ move" + bulk + " · ? help · q quit"
	case width >= 32:
		return "Enter actions · ? help · q quit"
	default:
		return "↵ actions · q quit"
	}
}

func (m screen) activityLine() string {
	if m.pairing {
		return m.activity.View() + " Verifying repository matches…"
	}
	if m.applying {
		label := "Applying updates…"
		if scope := m.selection.approved; scope != nil {
			label = "Updating skill…"
			if scope.checkout != "" {
				label = "Updating checkout…"
			}
		}
		return m.activity.View() + " " + label
	}
	quit := "q quit"
	if m.search.Focused() || m.panel == commandPanel {
		quit = "^C quit"
	}
	label := "Reading skills…"
	if m.checking {
		label = "Checking sources…"
	}
	if m.ctx != nil && m.ctx.Err() != nil {
		label = "Cancelling…"
	}
	return quit + " · " + m.activity.View() + " " + label
}

func (m screen) dashboardBody(rows []*skill, width, room int) []string {
	if room == 0 || width < 10 {
		return nil
	}
	if len(rows) == 0 {
		if m.busy || m.err != nil {
			return nil
		}
		if m.filter != "" || strings.TrimSpace(m.search.Value()) != "" {
			return []string{"No matching skills", "Esc clear search or filter"}[:min(room, 2)]
		}
		return []string{"No installed skills found"}
	}

	selected := max(0, slices.Index(rows, m.selected))
	row := rows[selected]
	details := skillDetails(row, m.cfg.home)
	if width >= 80 {
		listWidth := (width - 3) / 2
		list := skillList(rows, selected, listWidth, room)
		detailWidth := width - listWidth - 3
		preview := []string{panelTitle(printable(row.name)), statusBadge(row.status, false)}
		preview = append(preview, wrapLines(details, detailWidth)...)
		preview = append(preview, "", accent("Enter  Open skill menu"))
		lines := make([]string, min(room, max(len(list), len(preview))))
		for i := range lines {
			left, right := "", ""
			if i < len(list) {
				left = list[i]
			}
			if i < len(preview) {
				right = preview[i]
			}
			lines[i] = left + strings.Repeat(" ", listWidth-ansi.StringWidth(left)) + muted(" │ ") + right
		}
		return lines
	}

	detailRoom := min(max(0, room-3), 2)
	detail := wrapLines(details, width)
	detail = detail[:min(detailRoom, len(detail))]
	listRoom := room
	if len(detail) > 0 {
		listRoom -= len(detail) + 1
	}
	lines := skillList(rows, selected, width, listRoom)
	if len(detail) > 0 {
		lines = append(lines, muted(strings.Repeat("─", width)))
		lines = append(lines, detail...)
	}
	return lines
}

func skillDetails(row *skill, home string) []string {
	displayPath := func(value string) string {
		if home != "" {
			if rel, ok := strings.CutPrefix(value, home+string(filepath.Separator)); ok {
				return "~" + string(filepath.Separator) + rel
			}
		}
		return value
	}
	var lines []string
	if row.manager != "" {
		lines = append(lines, "Manager  "+row.manager)
	}
	if row.detail != "" && row.status != available && row.status != current && row.status != updated {
		label := "Status"
		if row.status == skipped || row.status == "Pinned" || row.status == modified || row.status == failed {
			label = "Reason"
		}
		lines = append(lines, label+"  "+row.detail)
	}
	if row.source != "" {
		lines = append(lines, "Source  "+displayPath(row.source))
	}
	if row.current != "" {
		revisions := short(row.current)
		if row.latest != "" && row.latest != row.current {
			revisions += " → " + short(row.latest)
		}
		lines = append(lines, "Revision  "+revisions)
	}
	for _, loc := range row.locations {
		lines = append(lines, loc.label+"  "+displayPath(loc.path))
	}
	return lines
}

func skillList(rows []*skill, selected, width, height int) []string {
	if height <= 0 {
		return nil
	}
	if height == 1 {
		row := rows[selected]
		return []string{highlighted(spaced("› "+printable(row.name), statusBadge(row.status, true), width))}
	}
	count := min(len(rows), height-1)
	lines := []string{muted("Installed skills")}
	start := max(0, selected-count+1)
	for i := start; i < start+count; i++ {
		row := rows[i]
		status := statusBadge(row.status, width < 40)
		name := printable(row.name)
		if i == selected {
			lines = append(lines, highlighted(spaced("› "+name, ansi.Strip(status), width)))
		} else {
			lines = append(lines, spaced("  "+name, status, width))
		}
	}
	return lines
}

func statusBadge(status string, compact bool) string {
	label, color := status, ""
	switch status {
	case available:
		label, color = "↑ Update available", "3"
		if compact {
			label = "↑ Update"
		}
	case current:
		label, color = "✓ Up to date", "2"
		if compact {
			label = "✓ Current"
		}
	case updated:
		label, color = "✓ Updated", "2"
	case modified:
		label, color = "• Local changes", "5"
		if compact {
			label = "• Edited"
		}
	case failed:
		label, color = "⚠ Error", "1"
	}
	if color == "" {
		return muted(label)
	}
	return styled(label, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)))
}

func spaced(left, right string, width int) string {
	if right == "" || ansi.StringWidth(right)+6 >= width {
		return ansi.Truncate(left, width, "…")
	}
	left = ansi.Truncate(left, width-ansi.StringWidth(right)-2, "…")
	return left + strings.Repeat(" ", width-ansi.StringWidth(left)-ansi.StringWidth(right)) + right
}

func fitLines(lines []string, width, height int) string {
	for i, line := range lines[:min(len(lines), height)] {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

func wrapLines(lines []string, width int) []string {
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(printable(line), width, "/"), "\n")...)
	}
	return wrapped
}

func accent(text string) string {
	return styled(text, lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true))
}

func muted(text string) string {
	return styled(text, lipgloss.NewStyle().Foreground(lipgloss.Color("8")))
}

func highlighted(text string) string {
	return styled(text, lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("23")).Bold(true))
}

func (m screen) activityCursor() string {
	return accent(strings.TrimSpace(ansi.Strip(m.activity.View())))
}
