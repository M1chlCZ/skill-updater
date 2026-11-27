package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type skillSelection struct {
	name, path string
	approved   *updateScope
}

func (s skillSelection) resolve(rows []*skill) (*skill, error) {
	var matches []*skill
	for _, row := range rows {
		if s.approved != nil {
			if row.name == s.name && row.path == s.path {
				matches = append(matches, row)
			}
			continue
		}
		if row.name != s.name && !slices.ContainsFunc(row.locations, func(loc location) bool {
			return filepath.Base(loc.path) == s.name
		}) {
			continue
		}
		if s.path == "" || samePath(s.path, row.path) || slices.ContainsFunc(row.locations, func(loc location) bool {
			return samePath(s.path, loc.path)
		}) {
			matches = append(matches, row)
		}
	}
	switch len(matches) {
	case 0:
		if s.path != "" {
			return nil, fmt.Errorf("skill %q was not found at %q", printable(s.name), printable(s.path))
		}
		return nil, fmt.Errorf("skill %q was not found", printable(s.name))
	case 1:
		return matches[0], nil
	default:
		var choices []string
		for _, row := range matches {
			for _, loc := range row.locations {
				choices = append(choices, "  "+loc.label+"  "+printable(loc.path))
			}
		}
		return nil, fmt.Errorf("multiple installations of %q; choose one with --path:\n%s", printable(s.name), strings.Join(choices, "\n"))
	}
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	left, leftErr := filepath.Abs(a)
	right, rightErr := filepath.Abs(b)
	return leftErr == nil && rightErr == nil && left == right
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type updateScope struct {
	checkout string
	rows     []*skill
}

func scopeFor(row *skill, rows []*skill) updateScope {
	scope := updateScope{checkout: row.checkout, rows: []*skill{row}}
	root := row.path
	if scope.checkout != "" {
		root = scope.checkout
	}
	if root == "" {
		return scope
	}
	paths := map[string]bool{}
	for _, other := range rows {
		if other == row || other.path == "" {
			continue
		}
		if inside(root, other.path) || inside(other.path, root) || slices.ContainsFunc(other.locations, func(loc location) bool {
			path, err := filepath.Abs(loc.path)
			return err == nil && pathThrough(root, path, paths)
		}) {
			scope.rows = append(scope.rows, other)
		}
	}
	return scope
}

// A checkout can own an installation's link even if the skill itself resolves elsewhere.
func pathThrough(root, path string, paths map[string]bool) bool {
	if inside(root, path) {
		return true
	}
	if through, seen := paths[path]; seen {
		return through
	}
	paths[path] = false
	parent := filepath.Dir(path)
	if parent == path {
		return false
	}
	if pathThrough(root, parent, paths) {
		paths[path] = true
		return true
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return false
	}
	link := filepath.Join(realParent, filepath.Base(path))
	if inside(root, link) {
		paths[path] = true
		return true
	}
	if target, err := os.Readlink(link); err == nil {
		paths[path] = linkTargetThrough(root, realParent, target, paths)
	}
	return paths[path]
}

// Resolve symlinks before walking up so a link followed by ".." keeps its real parent.
func linkTargetThrough(root, parent, target string, paths map[string]bool) bool {
	if filepath.IsAbs(target) {
		parent = string(filepath.Separator)
	}
	for component := range strings.SplitSeq(target, string(filepath.Separator)) {
		switch component {
		case "", ".":
			continue
		case "..":
			real, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return false
			}
			parent = filepath.Dir(real)
		default:
			parent = filepath.Join(parent, component)
		}
		if pathThrough(root, parent, paths) {
			return true
		}
	}
	return false
}

func (s updateScope) matches(approved updateScope) bool {
	if s.checkout != approved.checkout || len(s.rows) != len(approved.rows) {
		return false
	}
	for _, row := range s.rows {
		if !slices.ContainsFunc(approved.rows, func(other *skill) bool {
			return row.name == other.name && row.path == other.path && row.checkout == other.checkout &&
				slices.Equal(row.locations, other.locations)
		}) {
			return false
		}
	}
	return true
}

func (s updateScope) description() []string {
	var lines []string
	if s.checkout != "" {
		var names []string
		for _, row := range s.rows {
			names = append(names, printable(row.name))
		}
		lines = append(lines, "Whole Git checkout", "Skills  "+strings.Join(names, ", "), printable(s.checkout))
	}
	for _, row := range s.rows {
		lines = append(lines, printable(row.name))
		for _, loc := range row.locations {
			lines = append(lines, loc.label+"  "+printable(loc.path))
		}
	}
	return lines
}
