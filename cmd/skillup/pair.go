package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (c config) pairFile() string { return filepath.Join(c.home, ".skillup", "sources.json") }

func catalogSources(cfg config, lock *globalLock, rows []*skill, byLocation map[string]*skill) ([]*skill, []lockedInstall, error) {
	rows, installs := catalog(cfg, lock, rows, byLocation)
	pairs, err := readLock(cfg.pairFile())
	if err != nil || pairs == nil {
		return rows, installs, err
	}
	for _, r := range rows {
		if _, ok := pairs.skills[r.path]; ok && r.manager == "Unknown" {
			r.manager = "Skillup"
			installs = append(installs, lockedInstall{r, r.path, r.path, pairs})
		}
	}
	return rows, installs, nil
}

func githubSource(input string) (string, error) {
	source := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(input), "https://github.com/"), "/"), ".git")
	if !repoName.MatchString(source) {
		return "", fmt.Errorf("Enter a GitHub URL or owner/repository")
	}
	return source, nil
}

// Read immutable Git objects without checking out or executing repository files.
func pairSource(ctx context.Context, cfg config, selected, source string) (int, error) {
	runLock := filepath.Join(cfg.home, ".skillup.lock")
	if err := os.Mkdir(runLock, 0700); err != nil {
		return 0, fmt.Errorf("cannot acquire update lock: %w", err)
	}
	defer os.Remove(runLock)
	cache := sourceCache{repos: map[string]gitSource{}}
	defer func() {
		if cache.dir != "" {
			os.RemoveAll(cache.dir)
		}
	}()
	source, err := githubSource(source)
	if err != nil {
		return 0, err
	}
	repo, err := cache.fetch(ctx, source)
	if err != nil {
		return 0, err
	}
	entries, err := git(ctx, repo, "ls-tree", "-rz", "--name-only", "HEAD")
	if err != nil {
		return 0, err
	}
	paths := map[string][]string{}
	for file := range strings.SplitSeq(strings.TrimSuffix(string(entries), "\x00"), "\x00") {
		if path.Base(file) != "SKILL.md" || !fs.ValidPath(file) || strings.ContainsAny(file, "\\:\r\n") {
			continue
		}
		dir := path.Dir(file)
		name := path.Base(dir)
		if dir == "." {
			name = path.Base(source)
		}
		paths[name] = append(paths[name], dir)
	}
	rows, loc, err := inventory(ctx, cfg)
	if err != nil {
		return 0, err
	}
	installer, err := readLock(cfg.lock)
	if err != nil {
		return 0, err
	}
	rows, _, err = catalogSources(cfg, installer, rows, loc)
	if err != nil {
		return 0, err
	}
	pairs, err := readLock(cfg.pairFile())
	if err != nil {
		return 0, err
	}
	records := map[string]json.RawMessage{}
	if pairs != nil {
		records = pairs.skills
	}
	count := 0
	selectedMatched := false
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if r.manager != "Unknown" {
			continue
		}
		dirs := paths[r.name]
		// A single-skill repository often has a different name from its installed folder.
		if len(dirs) == 0 && r.path == selected && len(paths) == 1 {
			for _, candidate := range paths {
				dirs = candidate
			}
		}
		if len(dirs) != 1 {
			continue
		}
		tree, err := matchingTree(ctx, repo, dirs[0], r.path)
		if err != nil {
			return 0, err
		}
		if tree == "" {
			continue
		}
		records[r.path], err = json.Marshal(lockEntry{Source: source, SourceType: "github", SkillPath: path.Join(dirs[0], "SKILL.md"), SkillFolderHash: tree})
		if err != nil {
			return 0, err
		}
		count++
		if r.path == selected {
			selectedMatched = true
		}
	}
	if !selectedMatched {
		return 0, fmt.Errorf("No verified match for this skill. Check the repository, folder name and local changes.")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	raw, err := json.MarshalIndent(map[string]any{"version": 3, "skills": records}, "", "  ")
	if err != nil {
		return 0, err
	}
	if pairs != nil {
		err = pairs.write(raw)
	} else {
		filename := cfg.pairFile()
		if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			return 0, err
		}
		tmp, e := os.CreateTemp(filepath.Dir(filename), ".sources-*")
		if e != nil {
			return 0, e
		}
		defer os.Remove(tmp.Name())
		_, err = tmp.Write(raw)
		closeErr := tmp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Link(tmp.Name(), filename)
		}
	}
	return count, err
}

func matchingTree(ctx context.Context, repo, dir, local string) (string, error) {
	ref := func(commit string) string {
		if dir == "." {
			return commit + "^{tree}"
		}
		return commit + ":" + dir
	}
	seen := map[string]bool{}
	match := func(commit string) string {
		tree, err := gitText(ctx, repo, "rev-parse", "--verify", ref(commit))
		if err != nil || !validOID(tree) || seen[tree] {
			return ""
		}
		seen[tree] = true
		files, err := treeFiles(ctx, repo, tree)
		if err == nil && matchFiles(local, files) == nil {
			return tree
		}
		return ""
	}
	if tree := match("HEAD"); tree != "" {
		return tree, nil
	}
	history, err := gitText(ctx, repo, "--literal-pathspecs", "log", "--format=%H", "HEAD", "--", dir)
	if err != nil {
		return "", err
	}
	for commit := range strings.FieldsSeq(history) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if tree := match(commit); tree != "" {
			return tree, nil
		}
	}
	return "", nil
}
