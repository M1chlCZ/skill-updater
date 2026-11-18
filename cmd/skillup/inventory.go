package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	available = "Available"
	current   = "Current"
	updated   = "Updated"
	modified  = "Modified"
	skipped   = "Skipped"
	failed    = "Error"
)

type location struct {
	label, path string
}

type skill struct {
	name, path, checkout    string
	source, current, latest string
	status, detail          string
	locations               []location
}

func (s *skill) mark(status, detail string) {
	s.status, s.detail = status, detail
}

type config struct {
	home, codex, claude, lock string
}

func getConfig() (config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, err
	}
	lock := filepath.Join(home, ".agents", ".skill-lock.json")
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		lock = filepath.Join(state, "skills", ".skill-lock.json")
	}
	return config{home, envPath("CODEX_HOME", home, ".codex"), envPath("CLAUDE_CONFIG_DIR", home, ".claude"), lock}, nil
}

func envPath(key, home, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return filepath.Join(home, fallback)
}

func scan(ctx context.Context, cfg config, apply bool) ([]*skill, error) {
	lock, err := readLock(cfg.lock)
	if err != nil {
		return nil, err
	}
	rows, byLocation, err := inventory(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if apply {
		runLock := filepath.Join(cfg.home, ".skillup.lock")
		if err := os.Mkdir(runLock, 0700); err != nil {
			return rows, fmt.Errorf("cannot acquire update lock %s: %w", runLock, err)
		}
		defer os.Remove(runLock)
		if lock != nil {
			if err := lock.unchanged(); err != nil {
				return rows, err
			}
		}
	}

	cache := sourceCache{repos: map[string]gitSource{}}
	defer func() {
		if cache.dir != "" {
			os.RemoveAll(cache.dir)
		}
	}()
	if lock != nil {
		names := make([]string, 0, len(lock.skills))
		for name := range lock.skills {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return rows, err
			}
			dir := filepath.Join(cfg.home, ".agents", "skills", name)
			row := byLocation[dir]
			if !validName(name) {
				row = &skill{name: name}
				row.mark(skipped, "Invalid name in the global skills lock")
				rows = append(rows, row)
				continue
			}
			if row == nil {
				row = &skill{name: name, path: dir, locations: []location{{"Agents", dir}}}
				rows = append(rows, row)
			}
			row.checkout = ""
			inspectLocked(ctx, lock, &cache, row, dir, name, apply)
		}
	}

	checkouts := map[string]*skill{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		if row.checkout == "" {
			continue
		}
		info := checkouts[row.checkout]
		if info == nil {
			info = &skill{}
			inspectCheckout(ctx, info, row.checkout, apply)
			checkouts[row.checkout] = info
		}
		row.source, row.current, row.latest = info.source, info.current, info.latest
		row.mark(info.status, info.detail)
	}
	slices.SortStableFunc(rows, func(a, b *skill) int {
		switch {
		case a.status == available && b.status != available:
			return -1
		case a.status != available && b.status == available:
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	return rows, nil
}

func inventory(ctx context.Context, cfg config) ([]*skill, map[string]*skill, error) {
	var rows []*skill
	byPath := map[string]*skill{}
	byLocation := map[string]*skill{}
	roots := []location{
		{"Agents", filepath.Join(cfg.home, ".agents", "skills")},
		{"Codex", filepath.Join(cfg.codex, "skills")},
		{"Claude", filepath.Join(cfg.claude, "skills")},
	}
	active := map[string]bool{}
	var walk func(location, string) error
	walk = func(loc location, checkout string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(loc.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(loc.path)
		if err != nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			return err
		}
		if active[real] {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			info, err = os.Stat(real)
			if err != nil {
				return err
			}
			if info.IsDir() {
				checkout = linkedCheckout(ctx, real)
			}
		}
		if !info.IsDir() {
			return nil
		}
		if info, err := os.Stat(filepath.Join(real, "SKILL.md")); err == nil && info.Mode().IsRegular() {
			row := byPath[real]
			if row == nil {
				row = &skill{name: filepath.Base(loc.path), path: real}
				row.mark(skipped, "No supported source recorded")
				if _, err := os.Stat(filepath.Join(real, ".skild", "install.json")); err == nil {
					row.source = "Skild"
					row.detail = "Managed by Skild"
				}
				byPath[real] = row
				rows = append(rows, row)
			}
			if row.checkout == "" && row.source != "Skild" {
				row.checkout = checkout
			}
			row.locations = append(row.locations, loc)
			byLocation[loc.path] = row
			return nil
		}
		entries, err := os.ReadDir(real)
		if err != nil {
			return err
		}
		active[real] = true
		defer delete(active, real)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			if err := walk(location{loc.label, filepath.Join(loc.path, entry.Name())}, checkout); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := walk(root, ""); err != nil {
			return rows, byLocation, fmt.Errorf("scan %s: %w", root.path, err)
		}
	}
	return rows, byLocation, nil
}
