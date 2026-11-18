package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type treeFile struct {
	oid  string
	exec bool
}

func treeFiles(ctx context.Context, repo, tree string) (map[string]treeFile, error) {
	out, err := git(ctx, repo, "ls-tree", "-rz", tree)
	if err != nil {
		return nil, err
	}
	files := map[string]treeFile{}
	for record := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		fileInfo, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(fileInfo)
		if !ok || len(fields) != 3 || !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:\r\n") {
			return nil, fmt.Errorf("unexpected path in Git tree %s", tree)
		}
		if excludedByInstaller(name) {
			continue
		}
		for part := range strings.SplitSeq(name, "/") {
			if strings.EqualFold(part, ".git") {
				return nil, fmt.Errorf("unsupported Git directory at %q", name)
			}
		}
		mode, kind, oid := fields[0], fields[1], fields[2]
		if kind != "blob" || !validOID(oid) || mode != "100644" && mode != "100755" {
			return nil, fmt.Errorf("unsupported file type at %q", name)
		}
		files[name] = treeFile{oid, mode == "100755"}
	}
	if _, ok := files["SKILL.md"]; !ok {
		return nil, errors.New("tree has no SKILL.md")
	}
	return files, nil
}

func excludedByInstaller(name string) bool {
	for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
		switch path.Base(dir) {
		case ".git", "metadata.json", "__pycache__", "__pypackages__":
			return true
		}
	}
	return path.Base(name) == "metadata.json"
}

func matchFiles(dir string, expected map[string]treeFile) error {
	directories := map[string]bool{".": true}
	for name := range expected {
		for parent := path.Dir(name); !directories[parent]; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	seen := 0
	err := filepath.WalkDir(dir, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, filename)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() && directories[rel] {
			return nil
		}
		record, ok := expected[rel]
		if !ok || !entry.Type().IsRegular() {
			return fmt.Errorf("additional or non-regular local file %q", rel)
		}
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		// This SHA-1 is Git's blob identity, not a signature or trust decision.
		hash := sha1.New()
		fmt.Fprintf(hash, "blob %d\x00", info.Size())
		if _, err := io.Copy(hash, f); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != record.oid || (info.Mode()&0100 != 0) != record.exec {
			return fmt.Errorf("local file %q differs from the recorded Git tree", rel)
		}
		seen++
		return nil
	})
	if err == nil && seen != len(expected) {
		err = errors.New("local files are missing from the recorded skill")
	}
	return err
}

func updateSkill(ctx context.Context, lock *globalLock, repo, dest, name, tree, commit string, old, files map[string]treeFile) error {
	stage, err := os.MkdirTemp(filepath.Dir(filepath.Dir(dest)), ".skillup-"+name+"-")
	if err != nil {
		return err
	}
	clean := true
	defer func() {
		if clean {
			os.RemoveAll(stage)
		}
	}()
	newDir := filepath.Join(stage, "new")
	if err := os.Mkdir(newDir, 0700); err != nil {
		return err
	}
	for name, file := range files {
		contents, err := git(ctx, repo, "cat-file", "blob", file.oid)
		if err != nil {
			return err
		}
		filename := filepath.Join(newDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if file.exec {
			mode = 0755
		}
		out, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, writeErr := out.Write(contents)
		if err := errors.Join(writeErr, out.Close()); err != nil {
			return err
		}
	}
	if err := matchFiles(newDir, files); err != nil {
		return fmt.Errorf("staged files did not match the Git tree: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lock.unchanged(); err != nil {
		return err
	}
	if err := matchFiles(dest, old); err != nil {
		return err
	}
	backup := filepath.Join(stage, "old")
	if err := os.Rename(dest, backup); err != nil {
		return err
	}
	rollback := func(cause error) error {
		if err := os.Rename(backup, dest); err != nil {
			clean = false
			return errors.Join(cause, fmt.Errorf("could not restore the previous skill (saved at %s): %w", backup, err))
		}
		return cause
	}
	if err := matchFiles(backup, old); err != nil {
		return rollback(err)
	}
	if err := os.Rename(newDir, dest); err != nil {
		return rollback(err)
	}
	if err := lock.save(name, tree, commit); err != nil {
		if moveErr := os.Rename(dest, newDir); moveErr != nil {
			clean = false
			return errors.Join(err, fmt.Errorf("could not move the replacement (previous skill saved at %s): %w", backup, moveErr))
		}
		return rollback(err)
	}
	return nil
}
