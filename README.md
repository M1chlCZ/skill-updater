# skillup

A terminal dashboard for updating AI agent skills you already have. Skillup
is written in Go and uses Git to check the upstream sources.

## Install

Install with Go 1.25 or newer. Git must be on your PATH at runtime.

```sh
go install github.com/M1chlCZ/skill-updater/cmd/skillup@v0.1.0
skillup
```

Go installs the command in `GOBIN` or, by default, in `$(go env GOPATH)/bin`.
Add that directory to your PATH if your shell cannot find the command.
The terminal interface is tested on macOS. Linux is supported through the same
Go and Git commands, including inside WSL.

## Use

| Command | What it does |
| --- | --- |
| `skillup` | Open the dashboard. Updated upstream sources appear at the top. |
| `skillup --check` | Print the inventory and update status for each skill. Redirected output also uses this mode by default. |
| `skillup --apply` | Apply eligible updates and print the result. |

Use **↑/↓** or **j/k** to select a skill and see its source, revision and paths.
Press **r** to refresh, **u** to review the update prompt and **y** to apply,
or **q** to quit.

A skill linked into multiple agent folders appears once with all its locations.
A linked Git bundle may show several skills with the same revision and status.
Updating the bundle advances the checkout once.

## What Skillup can update

- **Vercel `skills` global GitHub installs.** Skillup reads the version 3 global
  lock at `~/.agents/.skill-lock.json` or
  `$XDG_STATE_HOME/skills/.skill-lock.json`. It uses the recorded Git tree to
  verify installed files and compares the whole skill against the current
  source branch. Pins, separate agent copies, symlinked canonical targets,
  unrecoverable trees, and trees with symlinks or submodules are skipped.
- **Linked Git checkouts.** A skill or a `skills/` directory linked to a Git
  checkout can be updated when the current branch tracks a remote branch. Apply
  checks that the checkout is clean and moves it only by fast-forward. For a
  previously unseen remote revision, preview reports a difference and apply
  verifies ancestry before merging.

The inventory covers `~/.agents/skills`, `$CODEX_HOME/skills` (default
`~/.codex/skills`), and `$CLAUDE_CONFIG_DIR/skills` (default
`~/.claude/skills`). Manually copied skills, plugin caches, project-level
installs, and installations managed by other installers are outside the update
scope. A skipped status includes the reason. Git and filesystem errors return
a nonzero exit status in command-line mode.

Checks fetch upstream data into temporary directories. Applying a skill stages
the new files before replacing the installed directory with a user-only copy. It keeps the original
until the lockfile is saved and restores it if writing the lock fails. Run one
installer at a time. After a forced interruption, an existing
`~/.skillup.lock` directory blocks another apply. Once you confirm no update is
running, inspect any `~/.agents/.skillup-*` staging directory for an `old`
backup before removing the lock directory and rerunning the check.

## Installer compatibility research

Agent Skills defines a directory format with optional metadata. Version and
source tracking depend on the installer.
[Agent Skills specification](https://agentskills.io/specification)

| Installer | Where provenance lives | Update behavior |
| --- | --- | --- |
| Vercel `npx skills` | User lock with source and Git tree, plus a separate project lock | `skills update -g` reinstalls changed global skills. [Global lock](https://github.com/vercel-labs/skills/blob/v1.9.0/src/skill-lock.ts) · [Commands](https://github.com/vercel-labs/skills/blob/main/docs/commands.md#skills-update) |
| GitHub CLI `gh skill` | Shared version 3 lock and GitHub fields added to the installed `SKILL.md` | `gh skill update --dry-run` previews changes. Its release selection and local frontmatter belong to GitHub CLI. [Frontmatter](https://github.com/cli/cli/blob/v2.105.0/internal/skills/frontmatter/frontmatter.go) · [Update source](https://github.com/cli/cli/blob/v2.105.0/pkg/cmd/skills/update/update.go) |
| Swift `skill` | `agents.json` and `agents.resolved`, including source and revision or digest | `skill update --scope user` previews, and `--apply` updates through its own state. [Lock format](https://github.com/agents-tools/core/blob/65df60ef97ff6d347b6944372a1b75de919313fe/Documentation/Architecture/State.md#lock) · [Commands](https://github.com/skill-cli/cli/blob/a59c92c892219280ff6db6d27ab56bb796fbff79/Documentation/Reference/CLI.md#update-restore-remove-and-initialize) |
| Skild | Per-install `.skild/install.json` and an installer lock | `skild update` reinstalls from its recorded source. [Locations](https://github.com/Peiiii/skild/blob/42d63166c75842856337ff9624032959f0112edd/packages/core/src/paths.ts#L24-L87) · [Update source](https://github.com/Peiiii/skild/blob/42d63166c75842856337ff9624032959f0112edd/packages/core/src/lifecycle.ts#L694-L720) |
| Codex `$skill-installer` | Copies skill folders into the Codex home, without an updater manifest | Existing destinations are refused. [Installer](https://github.com/openai/skills/blob/main/skills/.system/skill-installer/SKILL.md) |

## Development

```sh
go test ./...
go vet ./...
go run ./cmd/skillup --check
```

Licensed under [MIT](LICENSE).
