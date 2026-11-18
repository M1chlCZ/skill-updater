package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	opts := []string{
		"--no-optional-locks", "-C", dir,
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "submodule.recurse=false",
		"-c", "protocol.ext.allow=never",
	}
	cmd := exec.CommandContext(ctx, "git", append(opts, args...)...)
	cmd.WaitDelay = 2 * time.Second
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
			"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_TERMINAL_PROMPT", "GIT_NO_REPLACE_OBJECTS":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
	}
	return data, err
}

func gitText(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := git(ctx, dir, args...)
	return strings.TrimSpace(string(out)), err
}

func linkedCheckout(ctx context.Context, target string) string {
	root, err := gitText(ctx, target, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, target)
	if err == nil && (rel == "." || rel == "skills" || strings.HasPrefix(rel, "skills"+string(os.PathSeparator))) {
		return root
	}
	return ""
}

func inspectCheckout(ctx context.Context, row *skill, repo string, apply bool) {
	row.source = "Git checkout"
	row.current, _ = gitText(ctx, repo, "rev-parse", "--verify", "HEAD")
	branch, err := gitText(ctx, repo, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil || row.current == "" {
		row.mark(skipped, "Checkout has no active branch")
		return
	}
	remote, err := gitText(ctx, repo, "config", "--get", "branch."+branch+".remote")
	ref, refErr := gitText(ctx, repo, "config", "--get", "branch."+branch+".merge")
	if err != nil || refErr != nil || remote == "." || remote == "" || !strings.HasPrefix(ref, "refs/heads/") || strings.ContainsAny(ref, " \r\n\t") {
		row.mark(skipped, "Branch has no remote upstream")
		return
	}
	if address, err := gitText(ctx, repo, "remote", "get-url", "--", remote); err == nil {
		row.source = address
		if parsed, err := url.Parse(address); err == nil && parsed.Host != "" && parsed.User != nil {
			parsed.User = nil
			row.source = parsed.String()
		}
	}
	dirty, err := gitText(ctx, repo, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		row.mark(failed, "Could not check whether the checkout is clean: "+err.Error())
		return
	}
	if dirty != "" {
		row.mark(modified, "Git checkout contains local changes")
		return
	}
	tracked, err := git(ctx, repo, "ls-files", "-v", "-z")
	if err != nil {
		row.mark(failed, "Could not inspect checkout files: "+err.Error())
		return
	}
	for record := range strings.SplitSeq(string(tracked), "\x00") {
		if record != "" && (record[0] == 'S' || record[0] >= 'a' && record[0] <= 'z') {
			row.mark(skipped, "Checkout has tracked files excluded from Git change detection")
			return
		}
	}
	reply, err := gitText(ctx, repo, "ls-remote", "--exit-code", "--refs", "--", remote, ref)
	sha, remoteRef, ok := strings.Cut(reply, "\t")
	if err != nil {
		row.mark(failed, "Upstream lookup failed: "+err.Error())
		return
	}
	if !ok || remoteRef != ref || !validOID(sha) {
		row.mark(failed, "Upstream returned an unexpected revision")
		return
	}
	row.latest = sha
	if row.current == sha {
		row.mark(current, "Upstream revision is installed")
		return
	}
	// Preview only uses existing objects. An unseen upstream is fetched on apply.
	if apply {
		if _, err := git(ctx, repo, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", remote, sha); err != nil {
			row.mark(failed, "Could not fetch the upstream revision: "+err.Error())
			return
		}
	}
	if _, err := git(ctx, repo, "cat-file", "-e", sha+"^{commit}"); err == nil {
		if _, err := git(ctx, repo, "merge-base", "--is-ancestor", row.current, sha); err != nil {
			row.mark(skipped, "Local branch is ahead of or has diverged from upstream")
			return
		}
	} else if apply {
		row.mark(failed, "Fetched revision is unavailable")
		return
	}
	if !apply {
		row.mark(available, "Upstream differs. Apply requires a fast-forward.")
		return
	}
	head, headErr := gitText(ctx, repo, "rev-parse", "--verify", "HEAD")
	active, branchErr := gitText(ctx, repo, "symbolic-ref", "-q", "--short", "HEAD")
	state, stateErr := gitText(ctx, repo, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if headErr != nil || branchErr != nil || stateErr != nil || head != row.current || active != branch || state != "" {
		row.mark(modified, "Checkout changed during the update")
		return
	}
	if _, err := git(ctx, repo, "merge", "--ff-only", "--no-overwrite-ignore", sha); err != nil {
		row.mark(failed, "Fast-forward failed: "+err.Error())
		return
	}
	row.current = sha
	row.mark(updated, "Fast-forwarded the linked checkout")
}
