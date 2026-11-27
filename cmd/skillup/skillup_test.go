package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixtureGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Tester", "-c", "user.email=test@example.test"}, args...)...)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s (%v)", args, data, err)
	}
	return strings.TrimSpace(string(data))
}

func committedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	fixtureGit(t, repo, "init", "-b", "main")
	write(t, filepath.Join(repo, "skills", "demo", "SKILL.md"), "first\n")
	write(t, filepath.Join(repo, "skills", "demo", "metadata.json"), "{}\n")
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "commit", "-m", "first")
	return repo
}

func changeRemote(t *testing.T, repo string) {
	t.Helper()
	write(t, filepath.Join(repo, "skills", "demo", "references", "note.md"), "new resource\n")
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "commit", "-m", "update a resource")
}

func rowNamed(t *testing.T, rows []*skill, name string) *skill {
	t.Helper()
	for _, row := range rows {
		if row.name == name {
			return row
		}
	}
	t.Fatalf("missing skill %s", name)
	return nil
}

func TestGlobalGitHubApply(t *testing.T) {
	for _, tc := range []struct {
		name, local, pin, status string
	}{
		{"clean", "first\n", "", "Updated"},
		{"edited", "my changes\n", "", "Modified"},
		{"pinned", "first\n", "v1.0.0", "Pinned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := t.TempDir(), committedRepo(t)
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+repo+".insteadOf")
			t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/test/demo.git")
			hash := fixtureGit(t, repo, "rev-parse", "HEAD:skills/demo")
			changeRemote(t, repo)
			newHash := fixtureGit(t, repo, "rev-parse", "HEAD:skills/demo")
			dir := filepath.Join(home, ".agents", "skills", "demo")
			file := filepath.Join(dir, "SKILL.md")
			write(t, file, tc.local)
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(home, ".claude", "skills", "demo")
			if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dir, link); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(home, ".agents", ".skill-lock.json")
			record := map[string]any{"version": 3, "futureField": "keep", "skills": map[string]any{
				"demo": map[string]string{"source": "test/demo", "sourceType": "github", "skillPath": "skills/demo/SKILL.md", "skillFolderHash": hash, "pinnedRef": tc.pin, "unknown": "preserve"},
			}}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			write(t, lock, string(data))
			cfg := config{home: home, lock: lock, codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude")}
			rows, err := scan(context.Background(), cfg, false, skillSelection{})
			if err != nil {
				t.Fatal(err)
			}
			row := rowNamed(t, rows, "demo")
			if len(row.locations) != 2 {
				t.Fatalf("locations = %v", row.locations)
			}
			if tc.name == "clean" && (row.status != "Available" || row.current != hash || row.latest != newHash) {
				t.Fatalf("preview = %+v", row)
			}
			if b, _ := os.ReadFile(lock); string(b) != string(data) {
				t.Fatal("preview wrote the lock")
			}
			rows, err = scan(context.Background(), cfg, true, skillSelection{})
			if err != nil {
				t.Fatal(err)
			}
			if row := rowNamed(t, rows, "demo"); row.status != tc.status {
				t.Fatalf("apply = %+v", row)
			}
			res, _ := os.ReadFile(lock)
			if tc.name == "clean" {
				info, err := os.Stat(dir)
				if err != nil || info.Mode().Perm()&0077 != 0 {
					t.Fatalf("updated dir should remain private: %v, %v", info, err)
				}
				note, err := os.ReadFile(filepath.Join(link, "references", "note.md"))
				if err != nil || string(note) != "new resource\n" {
					t.Fatalf("resource through original link = %q, %v", note, err)
				}
				if !strings.Contains(string(res), newHash) || !strings.Contains(string(res), "preserve") || !strings.Contains(string(res), "futureField") {
					t.Fatalf("lock = %s", res)
				}
				rows, err = scan(context.Background(), cfg, false, skillSelection{})
				if err != nil || rowNamed(t, rows, "demo").status != "Current" {
					t.Fatalf("second check: %v, %v", rows, err)
				}
			} else if string(res) != string(data) {
				t.Fatal("skipped skill changed lock")
			}
			if b, _ := os.ReadFile(file); string(b) != tc.local {
				t.Fatalf("local SKILL.md = %q", b)
			}
		})
	}
}

func TestCheckoutDetectedThroughLaterAlias(t *testing.T) {
	home, remote := t.TempDir(), committedRepo(t)
	dir := filepath.Join(home, ".agents", "skills", "bundle")
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, home, "clone", remote, dir)
	link := filepath.Join(home, ".claude", "skills", "bundle")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	cfg := config{home: home, lock: filepath.Join(home, ".agents", ".skill-lock.json"), codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude")}
	rows, err := scan(context.Background(), cfg, false, skillSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if row := rowNamed(t, rows, "demo"); row.status != current || len(row.locations) != 2 {
		t.Fatalf("aliased checkout = %+v", row)
	}
}

func TestLinkedCheckout(t *testing.T) {
	home, repo := t.TempDir(), committedRepo(t)
	checkout := filepath.Join(home, "bundle")
	fixtureGit(t, home, "clone", repo, checkout)
	link := filepath.Join(home, ".codex", "skills", "bundle")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(checkout, "skills"), link); err != nil {
		t.Fatal(err)
	}
	cfg := config{home: home, lock: filepath.Join(home, ".agents", ".skill-lock.json"), codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude")}
	changeRemote(t, repo)
	head := fixtureGit(t, checkout, "rev-parse", "HEAD")
	rows, err := scan(context.Background(), cfg, false, skillSelection{})
	if err != nil || rowNamed(t, rows, "demo").status != "Available" || fixtureGit(t, checkout, "rev-parse", "HEAD") != head {
		t.Fatalf("preview: %v", err)
	}
	demo := filepath.Join(checkout, "skills", "demo", "SKILL.md")
	write(t, demo, "local edit\n")
	rows, err = scan(context.Background(), cfg, true, skillSelection{})
	if err != nil || rowNamed(t, rows, "demo").status != "Modified" || fixtureGit(t, checkout, "rev-parse", "HEAD") != head {
		t.Fatalf("dirty apply: %v", err)
	}
	fixtureGit(t, checkout, "restore", "--", demo)
	rows, err = scan(context.Background(), cfg, true, skillSelection{})
	if err != nil {
		t.Fatal(err)
	}
	row := rowNamed(t, rows, "demo")
	if row.status != "Updated" || fixtureGit(t, checkout, "rev-parse", "HEAD") != fixtureGit(t, repo, "rev-parse", "HEAD") {
		t.Fatalf("checkout apply: %+v", row)
	}
}

func TestSkillupProcess(t *testing.T) {
	if os.Getenv("SKILLUP_TEST_PROCESS") == "1" {
		os.Args = append(os.Args[:1], os.Args[3:]...)
		flag.CommandLine = flag.NewFlagSet("skillup", flag.ExitOnError)
		os.Exit(run())
	}
}

func TestTargetedCLI(t *testing.T) {
	home, remote := t.TempDir(), committedRepo(t)
	write(t, filepath.Join(remote, "skills", "sibling", "SKILL.md"), "first\n")
	fixtureGit(t, remote, "add", ".")
	fixtureGit(t, remote, "commit", "-m", "add sibling")
	demoRepo := filepath.Join(home, "demo-checkout")
	otherRepo := filepath.Join(home, "other-checkout")
	fixtureGit(t, home, "clone", remote, demoRepo)
	fixtureGit(t, home, "clone", remote, otherRepo)
	codex := filepath.Join(home, ".codex")
	claude := filepath.Join(home, ".claude")
	demoLink := filepath.Join(codex, "skills", "demo")
	otherLink := filepath.Join(claude, "skills", "demo")
	link := func(target, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	link(filepath.Join(demoRepo, "skills", "demo"), demoLink)
	link(filepath.Join(demoRepo, "skills", "demo"), filepath.Join(home, ".agents", "skills", "alias"))
	link(filepath.Join(otherRepo, "skills", "demo"), otherLink)
	changeRemote(t, remote)
	before := fixtureGit(t, otherRepo, "rev-parse", "HEAD")

	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestSkillupProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "SKILLUP_TEST_PROCESS=1", "HOME="+home,
			"CODEX_HOME="+codex, "CLAUDE_CONFIG_DIR="+claude, "XDG_STATE_HOME="+filepath.Join(home, "state"))
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("--apply", "--skill", "demo"); err == nil || !strings.Contains(out, demoLink) || !strings.Contains(out, otherLink) {
		t.Fatalf("ambiguous apply must list both install paths: %v\n%s", err, out)
	}
	for _, repo := range []string{demoRepo, otherRepo} {
		if head := fixtureGit(t, repo, "rev-parse", "HEAD"); head != before {
			t.Fatalf("ambiguous apply advanced %s", repo)
		}
	}
	if out, err := run("--check", "--skill", "Demo"); err == nil || !strings.Contains(out, "Demo") {
		t.Fatalf("skill names must match exactly: %v\n%s", err, out)
	}
	if out, err := run("--check", "--skill", "demo", "--path", demoLink); err != nil ||
		!strings.Contains(out, "Available") || strings.Contains(out, otherLink) {
		t.Fatalf("targeted check: %v\n%s", err, out)
	}
	if out, err := run("--apply", "--skill", "demo", "--path", demoLink); err != nil || !strings.Contains(out, "Updated") {
		t.Fatalf("targeted apply: %v\n%s", err, out)
	}
	if head := fixtureGit(t, demoRepo, "rev-parse", "HEAD"); head == before {
		t.Fatal("selected checkout did not advance")
	}
	if head := fixtureGit(t, otherRepo, "rev-parse", "HEAD"); head != before {
		t.Fatal("another installation advanced")
	}
	afterDemo := fixtureGit(t, demoRepo, "rev-parse", "HEAD")

	write(t, filepath.Join(remote, "skills", "sibling", "SKILL.md"), "new sibling\n")
	fixtureGit(t, remote, "commit", "-am", "change sibling")
	if out, err := run("--apply", "--skill", "demo", "--path", demoLink); err == nil || !strings.Contains(out, "outside") {
		t.Fatalf("CLI must refuse changes outside the selected skill: %v\n%s", err, out)
	}
	cfg := config{home: home, codex: codex, claude: claude, lock: filepath.Join(home, "missing-lock")}
	beforeLink, err := scan(t.Context(), cfg, false, skillSelection{name: "demo", path: demoLink})
	if err != nil {
		t.Fatal(err)
	}
	selected := beforeLink[0]
	approved := scopeFor(selected, beforeLink)
	selection := skillSelection{name: selected.name, path: selected.path, approved: &approved}
	link(filepath.Join(demoRepo, "skills", "sibling"), filepath.Join(codex, "skills", "sibling"))
	if out, err := run("--apply", "--skill", "demo", "--path", demoLink); err == nil || !strings.Contains(out, "sibling") {
		t.Fatalf("CLI must show the shared checkout scope: %v\n%s", err, out)
	}
	if _, err := scan(t.Context(), cfg, true, selection); err == nil || !strings.Contains(err.Error(), "scope changed") {
		t.Fatalf("a newly linked sibling must invalidate confirmation: %v", err)
	}
	if head := fixtureGit(t, demoRepo, "rev-parse", "HEAD"); head != afterDemo {
		t.Fatal("refused apply advanced the shared checkout")
	}
}

func TestTargetedLockedSkill(t *testing.T) {
	home, repo := t.TempDir(), committedRepo(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+repo+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/test/demo.git")
	write(t, filepath.Join(repo, "skills", "demo", "nested", "SKILL.md"), "nested skill\n")
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "commit", "-m", "add nested skill")
	hash := fixtureGit(t, repo, "rev-parse", "HEAD:skills/demo")
	changeRemote(t, repo)
	cfg := config{home: home, lock: filepath.Join(home, ".agents", ".skill-lock.json"),
		codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude")}
	skills := make(map[string]map[string]string)
	for _, name := range []string{"demo", "other"} {
		dir := filepath.Join(home, ".agents", "skills", name)
		write(t, filepath.Join(dir, "SKILL.md"), "first\n")
		write(t, filepath.Join(dir, "nested", "SKILL.md"), "nested skill\n")
		skills[name] = map[string]string{"source": "test/demo", "sourceType": "github",
			"skillPath": "skills/demo/SKILL.md", "skillFolderHash": hash}
	}
	data, err := json.Marshal(map[string]any{"version": 3, "skills": skills})
	if err != nil {
		t.Fatal(err)
	}
	write(t, cfg.lock, string(data))
	nested := filepath.Join(home, ".agents", "skills", "demo", "nested")
	link := filepath.Join(cfg.codex, "skills", "nested")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}
	if _, err := scan(t.Context(), cfg, true, skillSelection{name: "demo"}); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("targeted apply must identify a separate installation inside the target directory: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	rows, err := scan(t.Context(), cfg, true, skillSelection{name: "demo"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected one result: %v, %v", rows, err)
	}
	if row := rows[0]; row.status != updated {
		t.Fatalf("selected skill was not updated: %s, %s", row.status, row.detail)
	}
	other := filepath.Join(home, ".agents", "skills", "other", "references", "note.md")
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatalf("other skill's files must stay unchanged: %v", err)
	}
	lock, err := readLock(cfg.lock)
	if err != nil {
		t.Fatal(err)
	}
	var entry lockEntry
	if err := json.Unmarshal(lock.skills["other"], &entry); err != nil || entry.SkillFolderHash != hash {
		t.Fatalf("other skill's lock entry changed: %+v, %v", entry, err)
	}
}

func TestTargetedCheckoutIncludesExternalAlias(t *testing.T) {
	home, remote := t.TempDir(), t.TempDir()
	external := filepath.Join(home, "external")
	write(t, filepath.Join(external, "SKILL.md"), "external skill\n")
	fixtureGit(t, remote, "init", "-b", "main")
	write(t, filepath.Join(remote, "SKILL.md"), "root skill\n")
	write(t, filepath.Join(remote, "skills", "sub", ".keep"), "")
	sibling := filepath.Join(remote, "skills", "sibling")
	if err := os.Symlink(external, sibling); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, remote, "add", ".")
	fixtureGit(t, remote, "commit", "-m", "initial")
	checkout := filepath.Join(home, "checkout")
	fixtureGit(t, home, "clone", remote, checkout)
	cfg := config{home: home, lock: filepath.Join(home, "missing-lock"),
		codex: filepath.Join(home, ".codex"), claude: filepath.Join(home, ".claude")}
	installs := filepath.Join(cfg.codex, "skills")
	if err := os.MkdirAll(installs, 0755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"demo": checkout, "bundle": filepath.Join(checkout, "skills")} {
		if err := os.Symlink(target, filepath.Join(installs, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(sibling); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, remote, "commit", "-am", "remove the external alias")
	head := fixtureGit(t, checkout, "rev-parse", "HEAD")
	if _, err := scan(t.Context(), cfg, true, skillSelection{name: "demo"}); err == nil || !strings.Contains(err.Error(), "sibling") {
		t.Fatalf("targeted apply must disclose the skill exposed through a tracked symlink: %v", err)
	}
	if err := os.Remove(filepath.Join(installs, "bundle")); err != nil {
		t.Fatal(err)
	}
	jump := filepath.Join(home, "jump")
	if err := os.Symlink(filepath.Join(checkout, "skills", "sub"), jump); err != nil {
		t.Fatal(err)
	}
	crossing := filepath.Join(installs, "crossing")
	if err := os.Symlink(jump+"/../sibling", crossing); err != nil {
		t.Fatal(err)
	}
	if _, err := scan(t.Context(), cfg, true, skillSelection{name: "demo"}); err == nil || !strings.Contains(err.Error(), "crossing") {
		t.Fatalf("the checkout owns links reached through a symlink before ..: %v", err)
	}
	if err := os.Remove(crossing); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, "SKILL.md"), "skill containing the checkout\n")
	if err := os.Symlink(home, filepath.Join(installs, "outer")); err != nil {
		t.Fatal(err)
	}
	if _, err := scan(t.Context(), cfg, true, skillSelection{name: "demo"}); err == nil || !strings.Contains(err.Error(), "outer") {
		t.Fatalf("the checkout must not modify a containing skill: %v", err)
	}
	if after := fixtureGit(t, checkout, "rev-parse", "HEAD"); after != head {
		t.Fatal("targeted apply changed a checkout that overlaps another installation")
	}
}
