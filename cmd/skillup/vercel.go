package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type globalLock struct {
	path   string
	before []byte
	fields map[string]json.RawMessage
	skills map[string]json.RawMessage
}

func readLock(filename string) (*globalLock, error) {
	raw, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lock := &globalLock{path: filename, before: raw}
	if err := json.Unmarshal(raw, &lock.fields); err != nil {
		return nil, fmt.Errorf("invalid global skills lock %s: %w", filename, err)
	}
	var version int
	if err := json.Unmarshal(lock.fields["version"], &version); err != nil || version != 3 {
		return nil, fmt.Errorf("unsupported global skills lock format in %s (expected version 3)", filename)
	}
	if err := json.Unmarshal(lock.fields["skills"], &lock.skills); err != nil || lock.skills == nil {
		return nil, fmt.Errorf("invalid skills map in %s", filename)
	}
	return lock, nil
}

func (l *globalLock) unchanged() error {
	info, err := os.Lstat(l.path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("expected a regular lockfile at %s", l.path)
	}
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, l.before) {
		return fmt.Errorf("%s changed during the update", l.path)
	}
	return nil
}

type lockEntry struct {
	Source, SourceType, SourceURL, SkillPath, SkillFolderHash, Ref, PinnedRef, InstallMode string
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
var objectID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func validOID(s string) bool { return objectID.MatchString(s) }

func validName(s string) bool {
	return s != "" && len(s) <= 128 && !strings.HasPrefix(s, ".") && !strings.ContainsAny(s, "/\\:\r\n\t\x00")
}

type gitSource struct {
	dir string
	err error
}

type sourceCache struct {
	dir   string
	repos map[string]gitSource
}

func (c *sourceCache) fetch(ctx context.Context, source string) (string, error) {
	if saved, ok := c.repos[source]; ok {
		return saved.dir, saved.err
	}
	if c.dir == "" {
		dir, err := os.MkdirTemp("", "skillup-git-")
		if err != nil {
			return "", err
		}
		c.dir = dir
	}
	dir, err := os.MkdirTemp(c.dir, "repo-")
	if err == nil {
		_, err = git(ctx, c.dir, "clone", "--bare", "--quiet", "--", "https://github.com/"+source+".git", dir)
	}
	c.repos[source] = gitSource{dir, err}
	return dir, err
}

func inspectLocked(ctx context.Context, lock *globalLock, cache *sourceCache, row *skill, dir, name string, apply bool) {
	var entry lockEntry
	if err := json.Unmarshal(lock.skills[name], &entry); err != nil {
		row.mark(skipped, "Invalid entry in the global skills lock")
		return
	}
	row.source, row.current = entry.Source, entry.SkillFolderHash
	if entry.SourceType != "github" || !repoName.MatchString(entry.Source) {
		row.mark(skipped, "Lock entry is not a GitHub skill")
		return
	}
	if entry.PinnedRef != "" {
		row.mark("Pinned", "Pinned by the installer to "+entry.PinnedRef)
		return
	}
	if entry.InstallMode != "" && entry.InstallMode != "symlink" {
		row.mark(skipped, "Installer uses per-agent copies")
		return
	}
	if !validOID(entry.SkillFolderHash) || !fs.ValidPath(entry.SkillPath) || strings.ContainsAny(entry.SkillPath, "\\:\r\n") {
		row.mark(skipped, "Lock entry has no verifiable Git tree or skill path")
		return
	}
	for _, parent := range []string{filepath.Dir(filepath.Dir(dir)), filepath.Dir(dir), dir} {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() {
			row.mark(skipped, "The shared installation is missing or is a symbolic link")
			return
		}
	}
	// The installation may have changed since the inventory was read.
	row.readInstallerMetadata()
	if row.managedExternally() {
		return
	}
	repo, err := cache.fetch(ctx, entry.Source)
	if err != nil {
		row.mark(failed, "Could not fetch GitHub source: "+err.Error())
		return
	}
	ref := "HEAD"
	if entry.Ref != "" {
		branch := strings.TrimPrefix(entry.Ref, "refs/heads/")
		ref = "refs/heads/" + branch
		if !strings.HasPrefix(entry.Ref, "refs/heads/") {
			if _, err := git(ctx, repo, "show-ref", "--verify", "--quiet", "refs/tags/"+branch); err == nil {
				row.mark("Pinned", "Source ref identifies a Git tag")
				return
			}
		}
		if _, err := git(ctx, repo, "show-ref", "--verify", "--quiet", ref); err != nil {
			row.mark("Pinned", "Source ref does not identify a branch")
			return
		}
	}
	commit, err := gitText(ctx, repo, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil || !validOID(commit) {
		row.mark(failed, "Could not resolve the source branch")
		return
	}
	dirInRepo := entry.SkillPath
	if path.Base(dirInRepo) == "SKILL.md" {
		dirInRepo = path.Dir(dirInRepo)
	}
	treeRef := commit + ":" + dirInRepo
	if dirInRepo == "." {
		treeRef = commit + "^{tree}"
	}
	newTree, err := gitText(ctx, repo, "rev-parse", "--verify", treeRef)
	if err != nil || !validOID(newTree) {
		row.mark(skipped, "Skill path is missing upstream")
		return
	}
	row.latest = newTree
	old, err := treeFiles(ctx, repo, entry.SkillFolderHash)
	if err != nil {
		row.mark(skipped, "Recorded Git tree cannot be reconstructed: "+err.Error())
		return
	}
	if err := matchFiles(dir, old); err != nil {
		row.mark(modified, err.Error())
		return
	}
	if newTree == entry.SkillFolderHash {
		row.mark(current, "Installed files match the upstream Git tree")
		return
	}
	files, err := treeFiles(ctx, repo, newTree)
	if err != nil {
		row.mark(skipped, "Upstream skill cannot be installed: "+err.Error())
		return
	}
	if !apply {
		row.mark(available, "A newer Git tree is available")
		return
	}
	err = updateSkill(ctx, lock, repo, dir, name, newTree, commit, old, files)
	if err != nil {
		row.mark(failed, "Update failed: "+err.Error())
		return
	}
	row.current = newTree
	row.mark(updated, "Installed the upstream Git tree")
}

func (l *globalLock) save(name, tree, commit string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(l.skills[name], &fields); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"skillFolderHash": tree,
		"commit":          commit,
		"updatedAt":       time.Now().UTC().Format(time.RFC3339Nano),
	} {
		fields[key], _ = json.Marshal(value)
	}
	entry, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	skills := maps.Clone(l.skills)
	skills[name] = entry
	contents, err := json.Marshal(skills)
	if err != nil {
		return err
	}
	top := maps.Clone(l.fields)
	top["skills"] = contents
	raw, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := l.write(raw); err != nil {
		return err
	}
	l.skills, l.fields, l.before = skills, top, raw
	return nil
}

func (l *globalLock) write(raw []byte) error {
	info, err := os.Stat(l.path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".skillup-lock-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	_, writeErr := tmp.Write(raw)
	err = errors.Join(writeErr, tmp.Sync(), tmp.Close())
	if err != nil {
		return err
	}
	if err := l.unchanged(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), l.path)
}
