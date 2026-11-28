package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Tracking fields from gh skill install 2.106.0, with a synthetic source and tree.
const githubSkill = `---
description: A skill installed by GitHub CLI.
metadata:
    github-path: skills/demo
    github-ref: refs/heads/main
    github-repo: https://github.com/test/demo
    github-repo-id: "12345678"
    github-tree-sha: 76a98a285cb0434f3d39e1a873823556330e398b
name: demo
---
`

func TestInstallerProvenance(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	home, repo := t.TempDir(), committedRepo(t)
	cfg := config{home: home, codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude"),
		lock: filepath.Join(home, ".agents", ".skill-lock.json")}
	shared := filepath.Join(home, ".agents", "skills", "demo")
	github := filepath.Join(cfg.claude, "skills", "demo")
	skild := filepath.Join(cfg.codex, "skills", "demo")
	hash := fixtureGit(t, repo, "rev-parse", "HEAD:skills/demo")
	githubInstall := strings.Replace(githubSkill, "76a98a285cb0434f3d39e1a873823556330e398b", hash, 1)
	write(t, filepath.Join(shared, "SKILL.md"), "first\n")
	write(t, filepath.Join(github, "SKILL.md"), githubInstall)
	write(t, filepath.Join(skild, "SKILL.md"), "---\nname: demo\ndescription: Skild fixture\n---\n")
	// Shape of Skild 0.13.1 portable install metadata.
	write(t, filepath.Join(skild, ".skild", "install.json"), `{
  "schemaVersion": 1, "name": "demo", "platform": "codex", "scope": "global",
  "source": "/fixture/input/demo", "sourceType": "local", "installedAt": "2026-11-28T17:00:18Z",
  "contentHash": "ceb5de8fa97cf6c688bca209006021a0baac13e7f01e044a1352fa56f3766547",
  "hasSkillMd": true,
  "skill": {"frontmatter": {"name": "demo", "description": "Skild fixture"}, "validation": {"ok": true, "issues": []}}
}`)
	changeRemote(t, repo)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+repo+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/test/demo.git")
	lockFor := func(source string) string {
		return fmt.Sprintf(`{"version":3,"skills":{"demo":{"source":%q,"sourceType":"github","sourceUrl":"https://github.com/%s.git","skillPath":"skills/demo/SKILL.md","skillFolderHash":%q,"installedAt":"2026-11-28T17:00:27Z","updatedAt":"2026-11-28T17:00:27Z"}}}`, source, source, hash)
	}
	lock := lockFor("test/demo")
	write(t, cfg.lock, lock)

	rows, err := scan(context.Background(), cfg, true, skillSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected three separate installs, got %d", len(rows))
	}
	for _, want := range []struct{ path, status, manager, reason, source string }{
		{github, skipped, "GitHub CLI", "Use gh skill update", "https://github.com/test/demo"},
		{skild, skipped, "Skild", "Use skild update", "/fixture/input/demo"},
		{shared, skipped, "Skills lock", "Ambiguous shared lock", ""},
	} {
		row, err := (skillSelection{name: "demo", path: want.path}).resolve(rows)
		if err != nil {
			t.Fatal(err)
		}
		if row.status != want.status || row.detail != want.reason || (want.source != "" && row.source != want.source) {
			t.Errorf("%s = %+v", want.path, row)
		}
		for _, size := range [][2]int{{32, 12}, {100, 18}} {
			m := screen{width: size[0], height: size[1], rows: rows, selected: row}
			view := m.View().Content
			for _, field := range []string{"Manager  " + want.manager, "Reason  " + want.reason, "› demo", "q quit"} {
				if !strings.Contains(view, field) {
					t.Errorf("%dx%d view missing %q:\n%s", size[0], size[1], field, view)
				}
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] || strings.Contains(line, "\x1b") {
					t.Errorf("view overflows or uses color: %q", line)
				}
			}
		}
	}
	if got, _ := os.ReadFile(cfg.lock); string(got) != lock {
		t.Fatalf("ambiguous lock changed: %s", got)
	}
	if _, err := os.Stat(filepath.Join(shared, "references", "note.md")); !os.IsNotExist(err) {
		t.Fatalf("ambiguous skill should not have been updated: %v", err)
	}

	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/test/other.git")
	write(t, cfg.lock, lockFor("test/other"))
	rows, err = scan(context.Background(), cfg, true, skillSelection{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("apply: %d rows, %v", len(rows), err)
	}
	row, err := (skillSelection{name: "demo", path: shared}).resolve(rows)
	if err != nil || row.status != updated {
		t.Fatalf("separate shared install was not updated: %+v, %v", row, err)
	}
	if got, _ := os.ReadFile(filepath.Join(github, "SKILL.md")); string(got) != githubInstall {
		t.Fatalf("GitHub CLI install changed: %s", got)
	}
	if _, err := os.Stat(filepath.Join(skild, ".skild", "install.json")); err != nil {
		t.Fatalf("Skild marker disappeared: %v", err)
	}
}

func TestInstallerProvenanceProtectsLinkedCheckout(t *testing.T) {
	home, remote := t.TempDir(), committedRepo(t)
	cfg := config{home: home, codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude"),
		lock: filepath.Join(home, ".agents", ".skill-lock.json")}
	write(t, filepath.Join(remote, "skills", "managed", "SKILL.md"), githubSkill)
	fixtureGit(t, remote, "add", ".")
	fixtureGit(t, remote, "commit", "-m", "Add externally managed skill")
	checkout := filepath.Join(home, "checkout")
	fixtureGit(t, home, "clone", remote, checkout)
	link := filepath.Join(cfg.codex, "skills")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(checkout, "skills"), link); err != nil {
		t.Fatal(err)
	}
	head := fixtureGit(t, checkout, "rev-parse", "HEAD")
	write(t, filepath.Join(remote, "skills", "managed", "SKILL.md"), "changed\n")
	fixtureGit(t, remote, "commit", "-am", "Change externally managed skill")

	for _, mode := range []struct {
		apply     bool
		selection skillSelection
	}{
		{selection: skillSelection{name: "demo"}},
		{apply: true},
	} {
		rows, err := scan(context.Background(), cfg, mode.apply, mode.selection)
		if err != nil {
			t.Fatal(err)
		}
		if row := rowNamed(t, rows, "demo"); row.status != skipped || !strings.Contains(row.detail, "separate manager") {
			t.Errorf("linked checkout was not protected: %+v", row)
		}
	}
	if got := fixtureGit(t, checkout, "rev-parse", "HEAD"); got != head {
		t.Fatalf("checkout advanced from %s to %s", head, got)
	}
}

func TestInstallerProvenanceKeepsGitHubPinsWithSeparateState(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("XDG_STATE_HOME", state)
	cfg, err := getConfig()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", "demo")
	pinned := strings.Replace(githubSkill, "    github-ref:", "    github-pinned: v1.0.0\n    github-ref:", 1)
	write(t, filepath.Join(dir, "SKILL.md"), pinned)
	for _, filename := range []string{filepath.Join(home, ".agents", ".skill-lock.json"), cfg.lock} {
		write(t, filename, `{"version":3,"skills":{"demo":{"source":"test/demo","sourceType":"github","skillPath":"skills/demo/SKILL.md","skillFolderHash":"76a98a285cb0434f3d39e1a873823556330e398b"}}}`)
	}
	rows, err := scan(context.Background(), cfg, true, skillSelection{name: "demo", path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].status != "Pinned" || rows[0].manager != "GitHub CLI" {
		t.Fatalf("GitHub CLI pin missing from inventory: %+v", rows)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != pinned {
		t.Fatalf("pinned install changed: %s", got)
	}
}
