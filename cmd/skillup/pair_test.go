package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPairFromMenuAndUpdate(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	home, repo := t.TempDir(), committedRepo(t)
	cfg := config{home: home, codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude"), lock: filepath.Join(home, "installer.json")}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+repo+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/example/demo.git")
	for _, skill := range []string{"family-one", "family-edited", "family-duplicate", "family-owned"} {
		write(t, filepath.Join(repo, "skills", skill, "SKILL.md"), skill)
		write(t, filepath.Join(cfg.codex, "skills", skill, "SKILL.md"), skill)
	}
	write(t, filepath.Join(repo, "examples", "family-duplicate", "SKILL.md"), "family-duplicate")
	write(t, filepath.Join(cfg.codex, "skills", "family-edited", "SKILL.md"), "local edit")
	write(t, filepath.Join(cfg.codex, "skills", "family-owned", ".skild", "install.json"), "{}")
	demo := filepath.Join(cfg.claude, "skills", "demo")
	write(t, filepath.Join(demo, "SKILL.md"), "first\n")
	changeRemote(t, repo)
	ctx := context.Background()
	// The selected installation must match before any sibling association is saved.
	if n, err := pairSource(ctx, cfg, filepath.Join(cfg.codex, "skills", "family-edited"), "example/demo"); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(cfg.pairFile()); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	rows, err := scan(ctx, cfg, false, skillSelection{})
	if err != nil {
		t.Fatal(err)
	}
	m := screen{width: 48, height: 14, ctx: ctx, cfg: cfg, rows: rows, selected: rowNamed(t, rows, "demo"), search: newSearch()}
	send := func(msg tea.Msg) tea.Cmd { next, cmd := m.Update(msg); m = next.(screen); return cmd }
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	send(enter)
	if v := m.View().Content; !strings.Contains(v, "› demo") || !strings.Contains(v, "› Link GitHub source") {
		t.Fatal(v)
	}
	send(enter)
	send(tea.PasteMsg{Content: "https://github.com/example/demo.git"})
	batch := send(enter)().(tea.BatchMsg)
	if !strings.Contains(m.View().Content, "Verifying repository") {
		t.Fatal(m.View().Content)
	}
	result := batch[1]().(pairResult)
	if result.err != nil || result.count != 2 {
		t.Fatalf("%+v", result)
	}
	send(result)
	rows, err = scan(ctx, cfg, false, skillSelection{})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"family-edited": skipped, "family-duplicate": skipped, "family-owned": skipped, "family-one": current, "demo": available} {
		if got := rowNamed(t, rows, name); got.status != want {
			t.Fatalf("%s: %+v", name, got)
		}
	}
	for i, want := range []string{updated, current} {
		rows, err = scan(ctx, cfg, i == 0, skillSelection{name: "demo"})
		if err != nil || len(rows) != 1 || rows[0].status != want {
			t.Fatalf("scan %d: %+v %v", i, rows, err)
		}
	}
	if _, err := os.Stat(cfg.lock); !os.IsNotExist(err) {
		t.Fatal("unexpected installer lock", err)
	}
}
