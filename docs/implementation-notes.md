# Implementation Notes

This file lists where the code differs from, or adds to, the plan and the
architecture. Read it with [plan.md](plan.md) and [architecture.md](architecture.md).

## Additions

- `add --path D` names the subtree of a git clone that holds the skills. The
  architecture needed it: `sync` must know the subtree when it restores a pack.
- `render-shim [--bin P] [--memory]` prints the shim or the memory line.
  `scripts/install.sh` uses it, so one renderer makes the file that `doctor`
  checks.
- `internal/doctor`, `internal/pack`, and `internal/security` are new packages.
  The plan put doctor in `internal/harness`. The harness package holds the shim
  and memory checks, and `doctor` runs them when `HOME` is set.
- A pack with a symlink that leaves its skill directory is rejected at `add`,
  `update`, and `sync`. A fetched source is opened as its own root, so a symlink
  cannot make a pack take files from the library.
- `sync` puts back a pack from a swap tombstone when a crash left it as the only
  copy.
- `doctor --security` reports a hit in a `trusted` pack as a warning. The
  architecture does not say this. The reason: the user accepted that pack.

## Choices where the docs were open

- `update` moves `resolved` for a branch or tag ref. A ref that is a full commit
  hash is pinned: `update` reports it and changes nothing.
- A non-GitHub npx source cannot be checked for updates. `update --check`
  reports it and exits `3`.
- `skills-lock.json` is keyed by skill name. Two packs with a skill of the same
  name share a key. `remove` keeps entries that another pack shares.
- The soft gate is the default shim. See [phase0.md](phase0.md) for the
  evidence and the checks that are still open.
