# skillup

[![Release](https://img.shields.io/github/v/release/M1chlCZ/skill-updater?color=56d4dd)](https://github.com/M1chlCZ/skill-updater/releases)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Run `skillup` to see which AI agent skills you have, where they live, and
which ones have updates. Skillup is written in Go and checks the source with Git.

![skillup terminal dashboard](docs/demo.svg)

## Install

[Homebrew](https://github.com/M1chlCZ/homebrew-tap) puts `skillup` on your PATH.

```sh
brew install m1chlcz/tap/skillup
skillup
```

You can also build it with Go 1.25 or newer. Keep Git on your PATH.

```sh
go install github.com/M1chlCZ/skill-updater/cmd/skillup@v0.4.0
skillup
```

Go puts the binary in `GOBIN` or `$(go env GOPATH)/bin` by default. Add
that directory to your PATH if your shell can't find `skillup`.
The terminal interface was tested on macOS. The same Go and Git commands
work on Linux, including in WSL.

## Using skillup

| Command | What happens |
| --- | --- |
| `skillup` | Opens the dashboard, with available updates at the top. |
| `skillup --check` | Prints the installed skills and their status. Redirecting the output gives you the same text view. |
| `skillup --apply` | Applies eligible updates and prints what changed. |

Move with **↑/↓** or **j/k**. The selected row shows the source, revision
and install paths. Press **/** to search by skill name, location label, or
install path. Search ignores case and updates as you type. Press **Enter**
to keep the search and return to the list, or **Esc** to clear it. Press
**f** to cycle through All, Available, and Modified.

Press **r** to check again, **u** to open the update prompt, **y** to apply,
or **q** to quit. The search and status filter only change what you see.
The update prompt covers all eligible skills, including hidden rows.

The dashboard lists local skills while it checks their sources. You can
browse, search, or quit during the check. The status filter keeps rows
visible while their status is pending. If a source fails, its error appears
alongside the other results and the list stays available.

If two agents link to the same skill folder, you'll see one row with both
paths. A Git checkout can also supply several skills. Those skills share a
revision, and skillup advances their checkout once.

## Which skills get updated?

Skillup looks in `~/.agents/skills`, `$CODEX_HOME/skills` (default
`~/.codex/skills`), and `$CLAUDE_CONFIG_DIR/skills` (default
`~/.claude/skills`). It updates two kinds of installs.

- Global GitHub installs from Vercel's `skills` CLI. Skillup reads the
  version 3 lock at `~/.agents/.skill-lock.json` or
  `$XDG_STATE_HOME/skills/.skill-lock.json`. It checks the installed files
  against the recorded Git tree and compares the whole skill with the
  current source branch. It skips pins, separate agent copies, symlinked
  canonical targets, Git trees it can't reconstruct, and trees with
  symlinks or submodules.
- A skill or a `skills/` folder linked to a Git checkout. The checkout
  needs a clean working tree and a branch that tracks a remote branch.
  Skillup checks that Git can fast-forward before it moves the branch.
  A preview shows when the remote revision differs, even if that revision
  hasn't been fetched yet.

Local edits stay in place. Updates also skip manually copied skills,
plugin caches, project installs and installs managed by other tools.
Check the status for the reason. Git and filesystem errors give the
text command a nonzero exit status.

### What happens on disk

Checks fetch upstream data into temporary directories. During an update,
skillup prepares the new files, then swaps in a folder accessible only to
your user. It keeps the old folder until it saves the lockfile. If saving
fails, skillup restores the old folder.

Run one installer at a time. After a forced interruption, an existing
`~/.skillup.lock` directory blocks another apply. First check that nothing
is still running. Look in any `~/.agents/.skillup-*` staging directory
for an `old` backup before you remove the lock directory and run another check.

## Installer compatibility research

A skill can carry optional metadata. Each installer decides where to track
the source and version. [Agent Skills specification](https://agentskills.io/specification)

| Installer | Where it tracks the source | How it updates |
| --- | --- | --- |
| Vercel `npx skills` | A user lock with the source and Git tree, plus a separate project lock | `skills update -g` reinstalls changed global skills. [Global lock](https://github.com/vercel-labs/skills/blob/v1.9.0/src/skill-lock.ts) · [Commands](https://github.com/vercel-labs/skills/blob/main/docs/commands.md#skills-update) |
| GitHub CLI `gh skill` | The shared version 3 lock and GitHub fields in the installed `SKILL.md` | `gh skill update --dry-run` previews changes. GitHub CLI handles release selection and the local frontmatter. [Frontmatter](https://github.com/cli/cli/blob/v2.105.0/internal/skills/frontmatter/frontmatter.go) · [Update source](https://github.com/cli/cli/blob/v2.105.0/pkg/cmd/skills/update/update.go) |
| Swift `skill` | `agents.json` and `agents.resolved`, with the source and a revision or digest | `skill update --scope user` previews changes. Add `--apply` to update using its own records. [Lock format](https://github.com/agents-tools/core/blob/65df60ef97ff6d347b6944372a1b75de919313fe/Documentation/Architecture/State.md#lock) · [Commands](https://github.com/skill-cli/cli/blob/a59c92c892219280ff6db6d27ab56bb796fbff79/Documentation/Reference/CLI.md#update-restore-remove-and-initialize) |
| Skild | Each install has a `.skild/install.json` file, and Skild also keeps a lock | `skild update` reinstalls from the recorded source. [Locations](https://github.com/Peiiii/skild/blob/42d63166c75842856337ff9624032959f0112edd/packages/core/src/paths.ts#L24-L87) · [Update source](https://github.com/Peiiii/skild/blob/42d63166c75842856337ff9624032959f0112edd/packages/core/src/lifecycle.ts#L694-L720) |
| Codex `$skill-installer` | It copies the folder into the Codex home without an updater manifest | It refuses to overwrite an existing destination. [Installer](https://github.com/openai/skills/blob/main/skills/.system/skill-installer/SKILL.md) |

## Development

The dashboard uses Lip Gloss for its layout and
Bubbles for search input and the loading animation during checks and updates.

```sh
go test ./...
go vet ./...
go run ./cmd/skillup --check
```

Licensed under [MIT](LICENSE).
