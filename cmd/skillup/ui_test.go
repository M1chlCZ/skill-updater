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
	if view := m.View().Content; !strings.Contains(view, "Codex") || !strings.Contains(view, "Update available") {
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
	checkView("[Updates]", "› gamma", "a update all")
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
	checkView("[Local changes]", "› beta", "a update all")
	press('a', "a")
	checkView("All available updates")
	press('n', "n")
	press('f', "f")
	checkView("[All]", "› beta")
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
	if view := m.View().Content; !strings.Contains(view, "› demo") || !strings.Contains(view, "↵ actions") {
		t.Fatalf("narrow view must show the selected row and its actions:\n%s", view)
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
	if view := next.View().Content; !strings.Contains(view, "[Updates]") || !strings.Contains(view, "alpha") {
		t.Fatalf("status filter should keep pending rows visible:\n%s", view)
	}
	next, _ = next.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	next, _ = next.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	next, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if view := next.View().Content; !strings.Contains(view, next.(screen).activityCursor()+" demo") {
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
	for _, part := range []string{"Whole Git checkout", "/bundle", "demo", "sibling", "y apply", "Esc back"} {
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

func TestEnterOpensActionsAndConfirmsUpdate(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	demo := &skill{name: "demo", path: "/bundle/skills/demo", status: available, checkout: "/bundle"}
	sibling := &skill{name: "sibling", path: "/bundle/skills/sibling", status: current, checkout: "/bundle"}
	m := screen{ctx: context.Background(), width: 32, height: 12, search: newSearch(),
		rows: []*skill{demo, sibling}, selected: demo, filter: available}
	m.search.SetValue("demo")
	press := func(code rune) tea.Cmd {
		t.Helper()
		next, cmd := m.Update(tea.KeyPressMsg{Code: code})
		m = next.(screen)
		return cmd
	}
	if view := m.View().Content; !strings.Contains(view, "Enter actions") {
		t.Fatalf("dashboard should advertise Enter: %s", view)
	}
	if cmd := press(tea.KeyEnter); cmd != nil || m.confirm || m.applying ||
		!strings.Contains(m.View().Content, "› Update skill") {
		t.Fatalf("first Enter must show the skill's action: %s", m.View().Content)
	}
	if cmd := press(tea.KeyEnter); cmd != nil || !m.confirm || m.applying {
		t.Fatal("second Enter must show the update scope before writing")
	}
	for _, want := range []string{"Whole Git checkout", "demo", "sibling", "y apply", "Esc back"} {
		if !strings.Contains(m.View().Content, want) {
			t.Fatalf("confirmation missing %q: %s", want, m.View().Content)
		}
	}
	press(tea.KeyEscape)
	if m.confirm || !strings.Contains(m.View().Content, "› Update skill") {
		t.Fatal("cancel should return to the skill actions")
	}
	press(tea.KeyEscape)
	if m.selected != demo || m.search.Value() != "demo" || m.filter != available {
		t.Fatal("closing actions changed the dashboard selection or filters")
	}
	press(tea.KeyEnter)
	press(tea.KeyEnter)
	if cmd := press(tea.KeyEnter); cmd != nil || m.applying {
		t.Fatal("Enter must not apply to a shared checkout")
	}
	if cmd := press('y'); cmd == nil || !m.applying {
		t.Fatal("y should apply the reviewed checkout scope")
	}
	result := *demo
	result.status = updated
	next, _ := m.Update(scanResult{rows: []*skill{&result, sibling}})
	if view := next.View().Content; !strings.Contains(view, "Updated demo") || !strings.Contains(view, "No matching skills") {
		t.Fatalf("success should stay visible even under the updates filter: %s", view)
	}
}

func TestSkillActionsScrollAndExplainUnavailableUpdate(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	row := &skill{name: "managed", path: "/skills/managed", status: skipped, manager: "Skild",
		detail: "Use skild update", source: "example.test/owner/managed"}
	for _, name := range []string{"Codex", "Agents", "Claude", "Last location"} {
		row.locations = append(row.locations, location{name, "/example/long/path/to/installed/skills/managed"})
	}
	var m tea.Model = screen{rows: []*skill{row}, selected: row, search: newSearch()}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 32, Height: 9})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if v := m.View().Content; !strings.Contains(v, "› managed") || !strings.Contains(v, "› View details") {
		t.Fatalf("menu lost dashboard or selected row: %s", v)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.View().Content
	if !strings.Contains(view, "Reason  Use skild update") || strings.Contains(view, "› Update skill") {
		t.Fatalf("managed skill should explain why update is unavailable:\n%s", view)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if view := m.View().Content; !strings.Contains(view, "Last location") {
		t.Fatalf("last location should be reachable by scrolling:\n%s", view)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 104, Height: 18})
	for _, line := range strings.Split(m.View().Content, "\n") {
		if ansi.StringWidth(line) > 104 || strings.Contains(line, "\x1b") {
			t.Fatalf("resized action panel overflows or uses color: %q", line)
		}
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.(screen).selected != row || !strings.Contains(m.View().Content, "› managed") {
		t.Fatal("close should return to the same selected row")
	}
}

func TestActionsPaletteSearchUsesSelectedUpdateScope(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	row := &skill{name: "demo", path: "/skills/demo", status: available}
	var m tea.Model = screen{width: 32, height: 12, rows: []*skill{row}, selected: row, search: newSearch()}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if view := m.View().Content; !strings.Contains(view, "Command palette") {
		t.Fatalf("Ctrl+P should open the command palette:\n%s", view)
	}
	m, _ = m.Update(tea.PasteMsg{Content: "selected"})
	view := m.View().Content
	if !strings.Contains(view, "Update selected skill") || strings.Contains(view, "Update all") {
		t.Fatalf("palette should filter by action name:\n%s", view)
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	s := m.(screen)
	if cmd != nil || !s.confirm || s.selection.approved == nil || s.selection.path != row.path {
		t.Fatal("palette should open the normal selected update confirmation before changing files")
	}
}

func TestSingleSkillCanConfirmWithEnter(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	row := &skill{name: "demo", path: "/skills/demo", status: available}
	var m tea.Model = screen{rows: []*skill{row}, selected: row, search: newSearch()}
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	m, _ = m.Update(enter)
	m, _ = m.Update(enter)
	if view := m.View().Content; !strings.Contains(view, "Enter apply") {
		t.Fatalf("single skill should be confirmable with Enter:\n%s", view)
	}
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, IsRepeat: true})
	if cmd != nil || !m.(screen).confirm {
		t.Fatal("holding Enter must not confirm the update")
	}
	m, cmd = m.Update(enter)
	if cmd == nil || !m.(screen).applying {
		t.Fatal("pressing Enter again should start the confirmed update")
	}
}

func TestFirstCheckSuggestsAnUpdateWithoutStealingSelection(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, browsed := range []bool{false, true} {
		local := &skill{name: "local", path: "/skills/local", status: checkingStatus}
		demo := &skill{name: "demo", path: "/skills/demo", status: checkingStatus}
		var m tea.Model = screen{ctx: context.Background(), busy: true, autoSelect: true,
			rows: []*skill{local, demo}, selected: local, search: newSearch()}
		if browsed {
			m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
		}
		finishedLocal, finishedDemo := *local, *demo
		finishedLocal.status, finishedDemo.status = skipped, available
		m, _ = m.Update(scanResult{rows: []*skill{&finishedDemo, &finishedLocal}})
		want := "demo"
		if browsed {
			want = "local"
		}
		if selected := m.(screen).selected; selected == nil || selected.name != want {
			t.Fatalf("browsed=%t: expected %s, got %+v", browsed, want, selected)
		}
	}
}
