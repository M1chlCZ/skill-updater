package main

import (
	"context"
	"encoding/json"
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
	manager, githubPath     string
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

type lockedInstall struct {
	row       *skill
	name, dir string
}

func catalog(cfg config, lock *globalLock, rows []*skill, byLocation map[string]*skill) ([]*skill, []lockedInstall) {
	if lock == nil {
		return rows, nil
	}
	names := make([]string, 0, len(lock.skills))
	for name := range lock.skills {
		names = append(names, name)
	}
	slices.Sort(names)
	var installs []lockedInstall
	githubInstalls := map[string][]*skill{}
	for _, row := range rows {
		if row.githubPath != "" {
			name := filepath.Base(row.path)
			githubInstalls[name] = append(githubInstalls[name], row)
		}
	}
	for _, name := range names {
		if !validName(name) {
			row := &skill{name: name, manager: skillsLock}
			row.mark(skipped, "Invalid name in the global skills lock")
			rows = append(rows, row)
			continue
		}
		dir := filepath.Join(cfg.home, ".agents", "skills", name)
		row := byLocation[dir]
		if row != nil && row.managedExternally() {
			continue
		}
		var entry lockEntry
		ambiguous := len(githubInstalls[name]) > 0 && json.Unmarshal(lock.skills[name], &entry) == nil &&
			slices.ContainsFunc(githubInstalls[name], func(install *skill) bool { return install.matchesGitHubLock(entry) })
		if row == nil {
			if ambiguous {
				continue // The lock already describes a visible GitHub CLI installation.
			}
			row = &skill{name: name, path: dir, locations: []location{{"Agents", dir}}}
			rows = append(rows, row)
		}
		row.checkout, row.manager = "", skillsLock
		if ambiguous {
			row.mark(skipped, "Ambiguous shared lock")
			continue
		}
		installs = append(installs, lockedInstall{row, name, dir})
	}
	return rows, installs
}

func scan(ctx context.Context, cfg config, apply bool, selection skillSelection) ([]*skill, error) {
	if apply {
		runLock := filepath.Join(cfg.home, ".skillup.lock")
		if err := os.Mkdir(runLock, 0700); err != nil {
			return nil, fmt.Errorf("cannot acquire update lock %s: %w", runLock, err)
		}
		defer os.Remove(runLock)
	}
	lock, err := readLock(cfg.lock)
	if err != nil {
		return nil, err
	}
	rows, byLocation, err := inventory(ctx, cfg)
	if err != nil {
		return nil, err
	}
	rows, installs := catalog(cfg, lock, rows, byLocation)
	allRows := rows
	var checkoutLimit string
	if selection.name != "" {
		row, err := selection.resolve(rows)
		if err != nil {
			return nil, err
		}
		rows = []*skill{row}
		installs = slices.DeleteFunc(installs, func(install lockedInstall) bool { return install.row != row })
		if apply {
			scope := scopeFor(row, allRows)
			switch {
			case scope.checkout == "" && len(scope.rows) > 1:
				return nil, fmt.Errorf("the selected skill folder overlaps another installation\n%s", strings.Join(scope.description(), "\n"))
			case selection.approved != nil:
				if !scope.matches(*selection.approved) {
					return nil, fmt.Errorf("update scope changed; refresh the dashboard and confirm again")
				}
				rows = scope.rows
			case len(scope.rows) > 1:
				return nil, fmt.Errorf("updating %q would advance a shared Git checkout; confirm the whole checkout in the dashboard\n%s",
					printable(row.name), strings.Join(scope.description(), "\n"))
			default:
				if row.checkout != "" {
					checkoutLimit = row.path
				}
			}
			for _, affected := range scope.rows {
				if affected.checkout != scope.checkout {
					return nil, fmt.Errorf("checkout contains a skill with a separate installation or manager at %s", printable(affected.path))
				}
			}
		}
	}
	if apply && lock != nil {
		if err := lock.unchanged(); err != nil {
			return rows, err
		}
	}
	return inspectSources(ctx, lock, rows, allRows, installs, apply, checkoutLimit)
}

func inspectSources(ctx context.Context, lock *globalLock, rows, allRows []*skill, installs []lockedInstall, apply bool, checkoutLimit string) ([]*skill, error) {
	cache := sourceCache{repos: map[string]gitSource{}}
	defer func() {
		if cache.dir != "" {
			os.RemoveAll(cache.dir)
		}
	}()
	for _, install := range installs {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		inspectLocked(ctx, lock, &cache, install.row, install.dir, install.name, apply)
	}

	var managed []*skill
	for _, row := range allRows {
		if row.managedExternally() {
			managed = append(managed, row)
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
			if len(managed) > 0 && len(scopeFor(row, managed).rows) > 1 {
				info.mark(skipped, "Checkout contains a skill with a separate manager")
			} else {
				inspectCheckout(ctx, info, row.checkout, apply, checkoutLimit)
			}
			checkouts[row.checkout] = info
		}
		row.source, row.current, row.latest = info.source, info.current, info.latest
		row.mark(info.status, info.detail)
	}
	sortRows(rows)
	return rows, nil
}

func sortRows(rows []*skill) {
	slices.SortStableFunc(rows, func(a, b *skill) int {
		switch {
		case a.status == available && b.status != available:
			return -1
		case a.status != available && b.status == available:
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
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
				row = &skill{name: filepath.Base(loc.path), path: real, manager: "Unknown"}
				row.mark(skipped, "No supported source recorded")
				row.readInstallerMetadata()
				byPath[real] = row
				rows = append(rows, row)
			}
			if row.checkout == "" && checkout != "" && !row.managedExternally() {
				row.checkout, row.manager = checkout, gitCheckout
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
