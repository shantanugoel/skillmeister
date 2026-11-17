# Implementation Notes

This file lists where the code differs from, or adds to, the plan and the
architecture. Read it with [plan.md](plan.md) and [architecture.md](architecture.md).

## Additions

- `add --path D` names the subtree of a git clone that holds the skills. The
  architecture needed it: `sync` must know the subtree when it restores a pack.
- `init [--bin P] [--no-shim]` creates a library (when none exists), writes the
  shim into both discovery paths. It never overwrites
  an existing library file, and it refuses, before it writes anything, to
  replace a directory that skillmeister did not create. Under Homebrew it writes
  the stable `<prefix>/bin` path into the shim, not the versioned Cellar path.
  `scripts/install.sh` runs it, so one piece of code makes the files that
  `doctor` checks.
- The shim template is embedded in the binary (`internal/harness/SKILL.md.tmpl`).
  A library holds no copy, so a binary from Homebrew or a release works alone.
- The default library is `~/.skillmeister`. The older default,
  `~/dev/skillmeister`, still works when it holds a library and the new default
  does not. A command that runs with no library prints a hint to run `init`.
- `internal/doctor`, `internal/pack`, and `internal/security` are new packages.
  The plan put doctor in `internal/harness`. The harness package holds the shim
  check, and `doctor` runs it when `HOME` is set.
- A pack with a symlink that leaves its skill directory is rejected at `add`,
  `update`, and `sync`. A fetched source is opened as its own root, so a symlink
  cannot make a pack take files from the library.
- `sync` puts back a pack from a swap tombstone when a crash left it as the only
  copy.
- `doctor --security` reports a hit in a `trusted` pack as a warning. The
  architecture does not say this. The reason: the user accepted that pack.

## Dropped from the design

- The memory line in `~/.claude/CLAUDE.md` and `~/.config/opencode/AGENTS.md` is
  gone. The spike showed that the shim alone routes the agent, and the line
  meant that install edited files outside the library. `init`, `install.sh`, and
  `doctor` no longer write or check it. A line that an earlier version added is
  harmless; remove it by hand if you want.

## Choices where the docs were open

- `update` moves `resolved` for a branch or tag ref. A ref that is a full commit
  hash is pinned: `update` reports it and changes nothing.
- A non-GitHub npx source cannot be checked for updates. `update --check`
  reports it and exits `3`.
- `skills-lock.json` is keyed by skill name. Two packs with a skill of the same
  name share a key. `remove` keeps entries that another pack shares.
- The soft gate is the default shim. See [phase0.md](phase0.md) for the
  evidence and the checks that are still open.
