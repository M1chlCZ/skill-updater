package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestScreenRequiresConfirmation(t *testing.T) {
	m := screen{
		rows: []*skill{{name: "demo", status: available, locations: []location{{"Codex", "/test/demo"}}}},
	}
	if view := m.View().Content; !strings.Contains(view, "Codex") || !strings.Contains(view, "Available") {
		t.Fatalf("missing install status in view: %s", view)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if cmd != nil || !next.(screen).confirm {
		t.Fatal("u should show a confirmation before running an update")
	}
	next, cmd = next.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if cmd != nil || next.(screen).confirm {
		t.Fatal("n should cancel without running an update")
	}
	next, _ = next.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	next, cmd = next.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil || !next.(screen).applying {
		t.Fatal("y should start the update")
	}
	before := next.View().Content
	next, cmd = next.Update(next.(screen).activity.Tick())
	if cmd == nil || next.View().Content == before {
		t.Fatal("an in-progress update should animate")
	}
}

func TestRefreshAnimatesUntilScanCompletes(t *testing.T) {
	home := t.TempDir()
	m := screen{
		ctx: context.Background(),
		cfg: config{
			home:   home,
			lock:   filepath.Join(home, ".agents", ".skill-lock.json"),
			codex:  filepath.Join(home, ".codex"),
			claude: filepath.Join(home, ".claude"),
		},
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("refresh must start an animation alongside the scan")
	}
	before := next.View().Content
	tick := batch[0]()
	next, nextTick := next.Update(tick)
	if nextTick == nil || next.View().Content == before {
		t.Fatal("waiting screen should animate and schedule another frame")
	}
	next, _ = next.Update(batch[1]())
	after := next.View().Content
	next, cmd = next.Update(tick)
	if cmd != nil || next.View().Content != after {
		t.Fatal("animation must stop after the scan")
	}
}

func TestScreenFitsNarrowTerminalWithoutColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, width := range []int{4, 20, 80} {
		m := screen{
			width: width, height: 24,
			rows: []*skill{{name: "example-with-long-name", status: modified}},
		}
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > width || strings.Contains(line, "\x1b") {
				t.Fatalf("screen overflows or uses color at width %d: %q", width, line)
			}
		}
	}
}

func TestScreenSearchAndFilterNavigation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := screen{search: newSearch(), rows: []*skill{
		{name: "alpha", status: available, locations: []location{{"Codex", "/test/alpha"}}},
		{name: "beta", status: modified, locations: []location{{"Agents", "/test/team/beta"}}},
		{name: "gamma", status: available, locations: []location{{"Claude", "/test/gamma"}, {"Codex", "/test/team/gamma"}}},
	}}
	press := func(code rune, value string) {
		t.Helper()
		next, _ := m.Update(tea.KeyPressMsg{Code: code, Text: value})
		m = next.(screen)
	}
	checkView := func(want ...string) {
		t.Helper()
		view := m.View().Content
		for _, part := range want {
			if !strings.Contains(view, part) {
				t.Fatalf("missing %q in view:\n%s", part, view)
			}
		}
	}
	press(tea.KeyEnd, "")
	press('f', "f")
	checkView("f Available", "› gamma", "u update all")
	press(tea.KeyUp, "")
	checkView("› alpha")
	press(tea.KeyDown, "")
	checkView("› gamma")
	press('/', "/")
	press('T', "TEAM")
	press(tea.KeyEnter, "")
	checkView("/ TEAM", "› gamma")
	if strings.Contains(m.View().Content, "alpha") {
		t.Fatal("search must hide rows without a matching name or location")
	}

	press('f', "f")
	checkView("f Modified", "› beta", "u update all")
	press('u', "u")
	checkView("Update all? y/n")
	press('n', "n")
	press('f', "f")
	checkView("f All", "› beta")
	press(tea.KeyEscape, "")
	checkView("› beta")

	press('/', "/")
	press('A', "ALPHA")
	press(tea.KeyEnter, "")
	checkView("› alpha")
	press('f', "f")
	press('f', "f")
	checkView("No matching skills")
	press(tea.KeyDown, "")
	press(tea.KeyEnd, "")
	checkView("No matching skills")
}

func TestScreenSearchFitsNarrowTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var m tea.Model = screen{search: newSearch(), rows: []*skill{{name: "demo", status: available}}}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	if view := m.View().Content; !strings.Contains(view, "› demo") || !strings.Contains(view, "u update all") {
		t.Fatalf("narrow view must show the selected row and update scope:\n%s", view)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m, _ = m.Update(tea.PasteMsg{Content: "dlouhý název · 日本語 · skillqru"})
	view := m.View().Content
	if !strings.Contains(view, "skillqru") || !strings.Contains(view, "No matching skills") {
		t.Fatalf("missing query or empty state in narrow view:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if len(lines) > 8 {
		t.Fatalf("narrow search overflows vertically:\n%s", view)
	}
	// A monochrome search field still uses reverse video for its cursor.
	cursorStyle := strings.NewReplacer("\x1b[7m", "", "\x1b[m", "", "\x1b[0m", "")
	for _, line := range lines {
		withoutCursor := cursorStyle.Replace(line)
		if ansi.StringWidth(line) > 20 || strings.Contains(withoutCursor, "\x1b") {
			t.Fatalf("narrow search overflows or uses color: %q", line)
		}
	}
}
