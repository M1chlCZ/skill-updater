package main

import (
	"context"
	"os"
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
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd != nil || !next.(screen).confirm {
		t.Fatal("a should show a confirmation before running an update")
	}
	next, cmd = next.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if cmd != nil || next.(screen).confirm {
		t.Fatal("n should cancel without running an update")
	}
	next, _ = next.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
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
	next, check := next.Update(batch[1]())
	next, _ = next.Update(check())
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
	checkView("f Available", "› gamma", "a update all")
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
	checkView("f Modified", "› beta", "a update all")
	press('a', "a")
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
	if view := m.View().Content; !strings.Contains(view, "› demo") || !strings.Contains(view, "u update · a all") {
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

func TestScreenShowsInventoryBeforeSourceCheck(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	home, repo := t.TempDir(), committedRepo(t)
	cfg := config{
		home: home, lock: filepath.Join(home, ".agents", ".skill-lock.json"),
		codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude"),
	}
	checkout := filepath.Join(home, "checkout")
	fixtureGit(t, home, "clone", repo, checkout)
	fixtureGit(t, checkout, "remote", "set-url", "origin", filepath.Join(home, "missing-remote"))
	write(t, filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md"), "local\n")
	link := filepath.Join(cfg.codex, "skills", "demo")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(checkout, "skills", "demo"), link); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := screen{ctx: ctx, cancel: cancel, cfg: cfg, busy: true, activity: newActivity(), search: newSearch()}
	batch := m.Init()().(tea.BatchMsg)
	var next tea.Model = m
	next, _ = next.Update(tea.WindowSizeMsg{Width: 32, Height: 12})
	next, check := next.Update(batch[1]())
	waiting := next.View().Content
	if check == nil || !strings.Contains(waiting, "Checking") ||
		!strings.Contains(waiting, "q quit") || !strings.Contains(waiting, "alpha") ||
		strings.Contains(waiting, "Error") {
		t.Fatalf("local rows should appear before the remote check:\n%s", waiting)
	}
	next, _ = next.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if view := next.View().Content; !strings.Contains(view, "f Available") || !strings.Contains(view, "alpha") {
		t.Fatalf("status filter should keep pending rows visible:\n%s", view)
	}
	next, _ = next.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	next, _ = next.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if view := next.View().Content; !strings.Contains(view, "› demo") {
		t.Fatalf("inventory should be navigable during the check:\n%s", view)
	}
	next, _ = next.Update(check())
	finished := next.View().Content
	if strings.Contains(finished, "Checking") || !strings.Contains(finished, "Error") ||
		!strings.Contains(finished, "› demo") || !strings.Contains(finished, "alpha") {
		t.Fatalf("source failure should retain local rows and selection:\n%s", finished)
	}
	for _, line := range strings.Split(finished, "\n") {
		if ansi.StringWidth(line) > 32 || strings.Contains(line, "\x1b") {
			t.Fatalf("narrow view overflows or uses color: %q", line)
		}
	}
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if view := next.View().Content; !strings.Contains(view, "› alpha") {
		t.Fatalf("inventory should be navigable after the failure:\n%s", view)
	}

	next, refresh := next.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	next, check = next.Update(refresh().(tea.BatchMsg)[1]())
	next, _ = next.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	_, quit := next.Update(check())
	if ctx.Err() != context.Canceled || quit == nil {
		t.Fatal("q should cancel the source check and quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("a cancelled check should quit")
	}
}

func TestScreenSelectedCheckoutScope(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	demo := &skill{name: "demo", path: "/bundle/skills/demo", checkout: "/bundle", status: available, source: "example source"}
	sibling := &skill{name: "sibling", path: "/bundle/skills/sibling", checkout: "/bundle", status: available}
	other := &skill{name: "other", path: "/elsewhere", status: available}
	var m tea.Model = screen{rows: []*skill{demo, sibling, other}, selected: demo, search: newSearch()}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	view := m.View().Content
	if !strings.Contains(view, "example source") || !strings.Contains(view, "│") {
		t.Fatalf("wide view should show details beside the list:\n%s", view)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 32, Height: 12})
	view = m.View().Content
	if !strings.Contains(view, "› demo") || strings.Contains(view, "│") {
		t.Fatalf("narrow view should give the list full width:\n%s", view)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: '/'})
	m, _ = m.Update(tea.PasteMsg{Content: "demo"})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'u'})
	view = m.View().Content
	for _, part := range []string{"Whole Git checkout", "/bundle", "demo", "sibling", "y apply", "n/esc cancel"} {
		if !strings.Contains(view, part) {
			t.Fatalf("missing %q in confirmation:\n%s", part, view)
		}
	}
	if cmd != nil || strings.Contains(view, "other") {
		t.Fatalf("confirmation should cover exactly the shared checkout:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 32 || strings.Contains(line, "\x1b") {
			t.Fatalf("confirmation overflows or uses color: %q", line)
		}
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 32, Height: 1})
	m, cmd = m.Update(tea.KeyPressMsg{Code: 'y'})
	if cmd != nil {
		t.Fatal("confirmation must have room to show the scope before applying")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 32, Height: 3})
	if view := m.View().Content; !strings.Contains(view, "Whole Git checkout") || !strings.Contains(view, "y apply") {
		t.Fatalf("short confirmation must show both the scope and the controls:\n%s", view)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if view := m.View().Content; !strings.Contains(view, "sibling") {
		t.Fatalf("short confirmation must scroll to show the affected skills:\n%s", view)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 32, Height: 12})
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil || !strings.Contains(m.View().Content, "› demo") {
		t.Fatal("escape should cancel and return to the same selection")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'u'})
	m, cmd = m.Update(tea.KeyPressMsg{Code: 'y'})
	if cmd == nil || !strings.Contains(m.View().Content, "Updating checkout") {
		t.Fatal("y should start the confirmed checkout update")
	}
}
