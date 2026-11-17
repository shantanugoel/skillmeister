# How Skillmeister Works

Architecture, flows, and the command surface. CLI output is human-readable by
default. With `--json`, the output is a stable machine contract (see Output
contract).

## Shape

```text
~/.agents/skills/skillmeister ─┐
                               ├─▶ rendered shim SKILL.md      (one native skill)
~/.claude/skills/skillmeister ─┘            │
                                            │ Bash
                                            ▼
                                   skillmeister CLI
                                   ├── scan packs (live)
                                   ├── parse frontmatter
                                   ├── rank lexically
                                   └── print JSON
                                            │
                                            ▼
                              packs/<pack>/<skill>/SKILL.md   (read as text)
```

The harness registers one skill. Loading any other skill is a file read that the
agent performs on itself, guided by the shim.

## Repo layout

```text
~/.skillmeister/        # the default root; any git repo with a skillmeister.yaml works
  skillmeister.yaml            # manifest: packs, enablement, profiles, trust
  skillmeister.lock.json       # router lockfile: commits + tree hashes
  skills-lock.json             # npx only; merged from the `skills` CLI at add time
  skills/                      # your own skills, committed
    <name>/SKILL.md
  packs/                       # fetched packs, committed
    <pack>/<skill>/SKILL.md
  .import/                     # transient npx staging, gitignored
  .removed/                    # quarantined packs from `remove`, gitignored
  cmd/skillmeister/            # the router binary
  internal/                    # manifest, library, search, lock, harness
  vendor/                      # go mod vendor; makes install build offline
  scripts/install.sh           # idempotent setup
  docs/
  .gitattributes               # `* -text`, so tree hashes are byte-stable
  .gitignore                   # .import/, .removed/, .skillmeister.lock
  package.json                 # pins the `skills` CLI used for npx fetches
  package-lock.json
  THIRD_PARTY_NOTICES.md
```

Rules that keep the repo from leaking into the harness:

- `packs/`, `.import/`, and `.removed/` never sit at `.agents/skills` or
  `.claude/skills`. Opening the library in an agent must not register skills.
- The shim is **rendered at install** from the template, with the absolute binary
  path and library root substituted. No symlink chain, no `PATH` assumption.
- Packs are committed, so a fresh clone needs no fetch.

## Manifest

```yaml
version: 1

packs:
  own:
    fetch: local
    path: skills
    trust: trusted
    enabled: true

  anthropic:
    fetch: npx
    source: anthropics/skills
    ref: 8f3c2a1
    trust: pinned
    enabled: true
    disable: [pdf-forms]    # bare skill names inside this pack

  superpowers:
    fetch: git
    url: https://github.com/obra/superpowers
    ref: v4.2.0
    path: skills
    trust: pinned
    enabled: false

profiles:
  work: [own, anthropic]

defaults:
  search_limit: 8
```

- `fetch` is exactly one of `local`, `npx`, `git`. One mechanism per pack.
- `path` has two meanings. For `own`, it is the live location of the skills and
  is always `skills`. For all other packs, it is a source hint only: a subtree
  inside the clone for `git`, or the source directory for a local add. The CLI
  does not read a source hint after the copy.
- `trust` is `trusted`, `pinned`, or `untrusted`. `pinned` is not valid for a
  `fetch: local` pack, because a local pack has no lock entry to verify.
- `enabled` hides a pack; `disable` hides skills. Neither touches files.
- `disable` stores bare, normalized skill names. Passing `pack/skill` to the CLI
  records the bare name under that pack.
- Pack IDs match `^[a-z0-9][a-z0-9-]*$`; `own`, `.import`, `.removed`, `.` and
  `..` are reserved or rejected. `--as` is validated the same way.
- `profiles` are named pack subsets, selected with `--profile` or
  `SKILLMEISTER_PROFILE`. Session-scoped; there is no per-project file in v1.

## Lockfile

Two lockfiles exist, with a strict division:

- `skills-lock.json` is owned by the `skills` CLI, lives at the repo root, and is
  authoritative only for `fetch: npx` packs.
- `skillmeister.lock.json` is the only lockfile the router reads. Each `npx` and
  `git` pack has one entry with the same shape: `resolved` (the full commit
  that `ref` pointed to at fetch time) and `tree` (the tree hash of
  `packs/<pack>/` after the copy). For `npx` packs, `resolved` comes from
  `skills-lock.json` at `add` time. For `git` packs, it comes from the clone.

```json
{"version":1,"packs":{"anthropic":{"fetch":"npx","resolved":"8f3c2a1…","tree":"sha256:…"}}}
```

`fetch: local` packs have **no lock entry at all**. You own them, edits are
expected, and there is nothing to drift. For the same reason, `sync` cannot
restore a missing local pack. It reports the pack and exits `2`.

`sync` restores at `resolved`, never at `ref`. `update --apply` moves `ref` and
`resolved` together. A lock entry whose commit cannot be fetched is an error.

Hash recipe v1: sha256 over a canonical stream — the tag `v1`, then for each entry
in sorted slash-path order: type tag (`f` for file, `l` for symlink), an executable
boolean, the length-prefixed path, and the length-prefixed bytes (for a symlink,
the target). Only regular files and symlinks are entries; empty directories are
ignored. `.git`, `.import`, `.removed`, and `.DS_Store` are excluded. `.gitattributes`
pins `* -text` so line endings cannot drift the hash.

## Identity and paths

- A skill's name is its directory name lowercased with `_` and `.` normalized to
  `-`. After normalization it must match `[a-z0-9][a-z0-9-]*` and be unique
  inside the pack.
- `add` renames skill directories to the normalized name while copying, so the ID
  and the path always agree. Hand-written `skills/` directories must already
  match; `doctor` flags any that do not.
- Only immediate subdirectories of a pack are skills. A nested `SKILL.md` is
  ignored by `find` and reported by `doctor`.
- Canonical ID: `<pack>/<skill>`. Path: `packs/<pack>/<skill>/`; `own` is the one
  exception, at `skills/<skill>/`.
- Bare names resolve for `show` (highest declared trust wins, print an `also:`
  line). `path` requires an unambiguous ID.
- The record `name` is directory-derived. A frontmatter `name` is metadata only;
  if it disagrees, `doctor` warns.

## Flows

### Install (fresh clone, new machine)

```text
install.sh
  1. require Go >= 1.27 and set GOTOOLCHAIN=local
  2. build from the vendored tree, install to ~/.local/bin
  3. render the shim template with absolute {{BIN}} and {{ROOT}} paths into
     ~/.agents/skills/skillmeister/SKILL.md and
     ~/.claude/skills/skillmeister/SKILL.md
  4. append the memory line to ~/.claude/CLAUDE.md and ~/.config/opencode/AGENTS.md
  5. skillmeister doctor
```

There is nothing to fetch on install because packs are committed. `npm ci` is a
prerequisite only before `add` or `update` of an npx pack.

### Find and load

```text
user:  "find me a skill to convert a pdf to markdown"
agent: skillmeister find --json --limit 8 "pdf"
CLI:   scan enabled packs -> frontmatter -> rank -> compact JSON
agent: picks own/pdf-tools, reads skills/pdf-tools/SKILL.md
agent: follows the body for the rest of the session; re-runs find on topic change
```

`find` and `list` skip unparseable skills and missing packs, warn on stderr, and
keep scanning. `show`, `add`, and `doctor` report those errors; only `add` refuses
the pack.

The ranking tiers and scores are in [plan.md](plan.md), Phase 1. Every query
token must match, so the agent sends keywords, not the full sentence of the
user.

Body matching reads only the first 8 KB of a skill body. The CLI skips body
matching when the higher tiers already fill `--limit` results.

### Add a pack

```text
skillmeister add --as anthropic --fetch npx --ref 8f3c2a1 anthropics/skills
```

- `--fetch` is inferred when omitted: a path is `local`, a `git@` or `.git` URL
  is `git`, and an `owner/repo` or package spec is `npx`. Pass `--fetch git` to
  clone a GitHub repo directly. `--ref` is rejected for `local`.
- stage in `.import/` (npx runs there and its lock entries are merged into the
  root `skills-lock.json`; git clones there; local copies there)
- normalize skill directory names, then reject normalized collisions with the
  offending directories listed
- swap into `packs/<pack>/` through a tombstone rename (see Update vs sync)
- write manifest and lock through temp files and a rename; a crash leaves an
  orphan that `doctor` reports and bare `sync` repairs
- print a summary: skill count, names, and skills that need native semantics
- warn when the pack is larger than 25 MB
- `--path D` names the subtree of a git clone (or the source directory) that
  holds the skills. Without it, `add` looks at the clone root, then at
  `skills/`, and records the directory that it used in the manifest. `sync`
  reads that `path` when it restores a pack.
- reject a fetched pack that holds a symlink which leaves its skill directory,
  and a source path that resolves outside the clone
- never auto-trust; `untrusted` unless `--trust` is passed explicitly
- refuses an existing pack ID unless `--force`, which uses the same tombstone swap
- never commits; it leaves a working-tree diff for you to review

### Enable, disable, remove

| Operation | Effect | Touches files? |
|---|---|---|
| `enable <id\|pack>` | included in scan | no |
| `disable <id\|pack>` | excluded from scan and `list` | no |
| `remove <pack>` | manifest and lock entry dropped; files moved to `.removed/<pack>/` | yes, a quarantine move |
| `remove <pack> --purge` | manifest and lock entry dropped; pack deleted | yes |

Quarantine is what makes plain `remove` coherent: the files survive, but nothing
claims them, so `doctor` stays clean and `sync` has nothing to prune.

### Update vs sync

```text
skillmeister update --check [pack]     # network reads only; no library writes
skillmeister update --apply [pack]     # stage, swap, move ref + resolved
skillmeister sync                      # repair: restore missing packs, prune orphans
skillmeister sync --frozen             # verify only: writes nothing, exits 2 on drift
```

- `update --check` may run `git ls-remote` or `npm view`; "read-only" means no
  library writes, not no network.
- `update --apply` asks for confirmation, then stages a full copy, renames the old pack to `.removed/.swap-*`,
  renames the new pack into place, then deletes the tombstone. A concurrent `find`
  sees entirely old content, a missing pack (skipped with a warning), or entirely
  new content — never a partial tree.
- `sync` first puts back a pack from a swap tombstone when a crash left the
  tombstone as the only copy. Then it repairs missing packs and orphan
  directories. It never overwrites a
  `modified` pinned pack: it reports it and exits `2`.
- `sync --frozen` never writes: exit `0` clean, `2` on drift.

## CLI surface

Flags come **before** positionals. Go's `flag` package stops parsing at the first
non-flag argument, so the documented order is the only one that works.

```text
skillmeister find   [--json] [--limit N] [--pack P] [--all] <query>
skillmeister show   [--summary] [--raw] [--meta] <id|name>
skillmeister path   <id>
skillmeister list   [--json] [--pack P] [--all] [--trust T]

skillmeister add    [--as P] [--fetch local|npx|git] [--ref R] [--path D] [--trust T] [--force] <source>
skillmeister enable  <id|pack>
skillmeister disable <id|pack>
skillmeister remove  [--purge] <pack>
skillmeister update  [--check|--apply] [--yes] [pack]
skillmeister sync    [--frozen]

skillmeister trust  --level trusted|pinned [--accept] [--yes] <pack>
skillmeister doctor [--security] [--json]
skillmeister init   [--bin P] [--no-shim]        # create a library, install the shim

skillmeister --version

global, before the subcommand:
  --library PATH | SKILLMEISTER_ROOT, --profile P, --no-color
```

Read-only: `find`, `show`, `path`, `list`, `doctor`, `update --check`,
`sync --frozen`. `update --check` may perform read-only network calls.

Writers: `enable`, `disable`, and `trust` write the manifest or the lockfile
only. `add`, `remove`, bare `sync`, and `update --apply` can also write pack
files. No command ever executes skill code.

Writers take an exclusive lock at the library root; a second writer exits `3`.

## Output contract

Human-readable by default. `--json` prints one object per line and nothing else
on stdout; errors go to stderr.

```json
{"id":"own/pdf-tools","name":"pdf-tools","pack":"own","description":"Extract text and tables from PDFs","path":"/Users/…/skills/pdf-tools/SKILL.md","trust":"trusted","score":80,"warnings":[]}
```

Exit codes: `0` success (including zero hits), `1` usage or resolution error,
`2` drift or integrity failure, `3` I/O, lock contention, or subprocess failure.

`find --json` and `list --json` share one record shape and are the stable
contract the shim parses. `list` omits `score`. `doctor --json` is best-effort and
not versioned. `show` and `path` have no JSON mode: `path` prints a bare path,
`show` is built for humans. A breaking change to the record shape requires a
version bump, and `doctor` reports the binary version alongside it.

## Defaults and edge cases

| Topic | Decision |
|---|---|
| Library root | `--library` > `SKILLMEISTER_ROOT` > `~/.skillmeister`; must contain `skillmeister.yaml`. When `~/.skillmeister` has no library and `~/dev/skillmeister` has one, the older path is used. No config file stores the choice. |
| Profile | `--profile` (global) > `SKILLMEISTER_PROFILE` > all packs; unknown profile is an error; `--pack` intersects |
| Flags | before positionals; `--` ends flag parsing; a test runs the shim's exact line |
| `--all` | include disabled packs and skills |
| `--pack` | repeatable via a `flag.Value`; intersects with the active profile |
| `--limit` | `--limit` > `defaults.search_limit` > 8 |
| `list --trust` | exact match on declared trust; `modified` is rejected with a pointer to `doctor` |
| `add --trust` | `trusted`, `pinned`, or `untrusted`; `pinned` is rejected for a local pack |
| Global flags | before the subcommand; each subcommand has its own `flag.FlagSet` with `ContinueOnError` |
| Confirmation | `update --apply`, and `trust --level trusted` on a fetched pack; prompts only when stdin is a TTY; `--yes` required otherwise; declining exits `1` |
| Manifest strictness | unknown keys, unknown `fetch`/`trust` values, and a missing `version` are errors; `enabled` defaults true, `trust` defaults `untrusted` |
| Manifest writes | edit a YAML node tree to preserve comments and key order; golden round-trip test |
| Frontmatter | split on the first `---` … `---` pair; strip a BOM; accept CRLF fences; invalid UTF-8, duplicate keys, an unterminated fence, or multiple documents are errors; cap at 64 KB |
| No frontmatter | indexed under the directory name with an empty description; `doctor` flags it |
| Scan bounds | skip files over 1 MB; do not follow symlinked directories |
| Recognized fields | `name`, `description`, `license`, plus the native-only set that triggers a warning |
| `show` flags | default = metadata + body, control characters escaped; `--summary` = metadata + file/script inventory, no body; `--raw` = body only, unescaped; `--meta` = frontmatter only |
| `modified` | computed by `doctor` and `sync` only: a `pinned` pack's tree hash differs from the lockfile |
| Git | `add` and `update` never commit; they leave a working-tree diff for review |
| `trust` | pack-level only; `pinned` verifies the current hash or requires `--accept` to rewrite it; `modified` is never settable |
| Symlink install | not used; the shim is rendered, and install refuses to overwrite a real directory |
| Path printing | all reads go through `os.Root`; print the root joined with the relative path that was opened |
| Writers | exclusive lock at the library root; contention exits `3` |

## Shim contract

The shim is the only consumer of the stable record.

```text
skillmeister find --json --limit 8 "<query>"
```

- Send 1 to 3 keywords as the query, not a sentence. Every query token must
  match.
- Read `id`, `name`, `description`, `path`, `trust`, `score`, `warnings`; ignore
  the rest.
- Zero records: try one time more with fewer or different keywords. If there
  are still no records, report that nothing matched and mention that `--all`
  includes disabled skills. Do not widen the search automatically — `disable` is a user
  decision.
- Non-zero exit: show the stderr line and stop.
- Top two results at the same score: use the descriptions to choose. If the
  descriptions do not make the choice clear, ask the user. Scores have only
  five levels, so equal scores are usual and are not a reason to ask.
- `trust` is `untrusted`: run `show --summary`, describe what the skill does and
  what it would run, and ask before reading the body. `modified` never reaches
  the shim; only `doctor` computes it.
- Surface any non-empty `warnings` before following the skill. Native-only fields
  do not work in a routed skill.
- Read the returned `path`, and supporting files under its directory. Nothing
  outside that directory, and nothing suggested by a path inside a skill body.
- Bundled scripts: run by absolute path under the skill directory, ask before
  every run, and refuse to run anything from an `untrusted` pack.
- Stay in force for the session. Do not re-run `find` while following a loaded
  skill; re-run on a topic change. If the body is lost after compaction, re-read
  the same path before asking the user.

## Harness integration

- **Shim**: default is the soft gate — the description says "use only when the
  user explicitly asks for a skill". The hard gate is one line:
  `disable-model-invocation: true`, invoked only as `/skillmeister` or
  `@skillmeister`. Phase 0 picks one variant for both harnesses.
- **Memory line** in both `~/.claude/CLAUDE.md` and `~/.config/opencode/AGENTS.md`,
  written with the library root that install used:
  "Skill library at `~/.skillmeister`, searched with `skillmeister`. Use it
  only when I explicitly ask for a skill."
- **Claude Code permissions**: an additional directory for the library plus read
  and `skillmeister` command allowances, with the exact settings file documented
  in Phase 3. Phase 0 uses the same config so it measures the shim, not prompts.
- **OpenCode** reads both `~/.agents/skills` and `~/.claude/skills`.

## Assumptions to verify in Phase 0

- That Claude Code also scans `~/.agents/skills`.
- That `disable-model-invocation` exists in both; it may be Claude-only, in which
  case the hard gate degrades to the soft gate in OpenCode.
- The exact invocation spelling per harness.
- The Claude Code permission-rule syntax and whether it belongs in user or
  project settings.
- That an invoked skill stays active after compaction in both harnesses.
- That with both discovery paths present, each harness lists the shim exactly
  once rather than twice.

Each is a one-line check against the installed versions, not a design decision.

## Invariants

1. Only the shim is ever natively registered.
2. Query commands (`find`, `show`, `path`, `list`) never write and never execute
   external commands.
3. Every CLI read is mediated by `os.Root`, so symlink escapes fail at runtime.
   The agent's own reads are bounded by the shim contract instead.
4. Enable/disable is manifest-only.
5. One fetch mechanism per pack.
6. The lockfile pins commit and tree hash for fetched (`npx` and `git`) packs; local packs are
   never locked; `modified` is computed only by `doctor` and `sync`.
7. `sync --frozen` never writes: exit 0 clean, exit 2 on drift.
8. A failed write never leaves a half-written manifest, lockfile, or pack.

## Trust

Two forms exist, and only the declared one is stored:

- **Declared** — the manifest level. `find` and `list` report this.
- **Effective** — declared, downgraded to `modified` when a `pinned` pack's tree
  hash no longer matches. Only `doctor` and `sync` compute it.

Query-time order is `trusted` > `pinned` > `untrusted`; `modified` is a
doctor-only downgrade and never appears in a query result.

Transitions:

| From | Action | To |
|---|---|---|
| `untrusted` | `trust --level pinned` when the hash matches | `pinned` |
| `untrusted` | `trust --level trusted` | `trusted`, with a confirmation for fetched packs |
| `pinned` | edit the tree | `modified` in `doctor`; declared stays `pinned` |
| `modified` | `trust --level pinned --accept` | `pinned`, lock hash rewritten |
| `modified` | `update --apply` | `pinned`, upstream content restored |

`trust` is pack-level only; there is no per-skill trust override. `trusted` on a
fetched pack disables drift detection, so the command asks for confirmation. The
lock entry stays, so a later change back to `pinned` can verify the hash.

Trust is a UX and ranking aid, not a sandbox. A hit in a pack that is declared
`trusted` is reported as a warning, because the user accepted that pack. `doctor
--security` exits `2` when it finds any high-severity pattern in another pack: bundled scripts, `curl | sh`, `sudo`, `eval`,
base64 blobs, secret paths, broad `allowed-tools`, `!` lines, and `../` escapes.
Heuristics, not proof.

## Known limits

Native semantics are the big one: routed skills are plain text and get no
`allowed-tools`, `context: fork`, hooks, or command injection. See
[overview.md](overview.md) for the full tradeoff. Beyond that:

- App-managed skills in `~/.claude/skills/synced/` are still discovered natively
  and cannot be hidden by the router.
- Cloud and remote sessions cannot see a local library unless it is vendored into
  the project.
- Search is lexical in v1. That matches prior art and is enough at hundreds of
  skills; embeddings are a later, measurable decision.
- Windows is untested. Rendered shims avoid the symlink-privilege problem, but
  nothing else has been exercised there.
