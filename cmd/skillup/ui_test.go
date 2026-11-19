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
