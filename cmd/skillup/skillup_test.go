package main

import (
	"context"
	"encoding/json"
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
			rows, err := scan(context.Background(), cfg, false)
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
			rows, err = scan(context.Background(), cfg, true)
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
				rows, err = scan(context.Background(), cfg, false)
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
	rows, err := scan(context.Background(), cfg, false)
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
	rows, err := scan(context.Background(), cfg, false)
	if err != nil || rowNamed(t, rows, "demo").status != "Available" || fixtureGit(t, checkout, "rev-parse", "HEAD") != head {
		t.Fatalf("preview: %v", err)
	}
	demo := filepath.Join(checkout, "skills", "demo", "SKILL.md")
	write(t, demo, "local edit\n")
	rows, err = scan(context.Background(), cfg, true)
	if err != nil || rowNamed(t, rows, "demo").status != "Modified" || fixtureGit(t, checkout, "rev-parse", "HEAD") != head {
		t.Fatalf("dirty apply: %v", err)
	}
	fixtureGit(t, checkout, "restore", "--", demo)
	rows, err = scan(context.Background(), cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	row := rowNamed(t, rows, "demo")
	if row.status != "Updated" || fixtureGit(t, checkout, "rev-parse", "HEAD") != fixtureGit(t, repo, "rev-parse", "HEAD") {
		t.Fatalf("checkout apply: %+v", row)
	}
}
