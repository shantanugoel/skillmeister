# How Skillmeister Works

Architecture, flows, and the command surface. The machine contract is JSON on
stdout with `--json`; the implementation language can change without touching the
shim or the agent config.

## Shape

```text
~/.agents/skills/skillmeister ─┐
                               ├─▶ shim/skillmeister/SKILL.md   (one native skill)
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
~/dev/skillmeister/
  skillmeister.yaml            # manifest: packs, enablement, profiles, trust
  skillmeister.lock.json       # router lockfile: commits + content hashes
  skills/                      # your own skills, committed
    <name>/SKILL.md
  packs/                       # fetched packs, committed
    <pack>/<skill>/SKILL.md
  shim/skillmeister/SKILL.md   # the only natively registered skill
  cmd/skillmeister/            # the router binary
  internal/                    # manifest, library, search, lock, harness
  vendor/                      # go mod vendor; makes install build offline
  scripts/install.sh           # idempotent setup
  docs/
  package.json                 # pins the `skills` CLI used for npx fetches
  package-lock.json
  THIRD_PARTY_NOTICES.md
```

Two rules keep the repo from leaking into the harness:

- `packs/` never sits at `.agents/skills` or `.claude/skills`. Opening the library
  itself in an agent must not register hundreds of skills.
- npx fetches stage into `.import/`, then move into `packs/<pack>/`. The `skills`
  CLI has no install-dir flag, so this wrapper is what keeps the layout honest.

Fetched packs are committed, which is what makes a fresh clone work offline with
nothing to fetch.

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
    disable: [pdf-forms]    # skill-level opt-out inside a pack

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
- `enabled` hides a pack; `disable` hides individual skills. Neither touches files.
- `trust` is declared at add time and re-checked against the lockfile.
- `profiles` are named pack subsets, selected with `--profile` or `SKILLMEISTER_PROFILE`.

Two lockfiles exist, with a strict division:

- `skills-lock.json` is owned by the `skills` CLI, lives at the repo root, and is
  authoritative only for `fetch: npx` packs.
- `skillmeister.lock.json` is the only lockfile the router reads. For npx packs
  its entries are derived from `skills-lock.json` at `add` time; for every pack it
  records the resolved commit and a hash of the tree.

`doctor` cross-checks both and reports any disagreement.

Hash recipe: sha256 over each file's slash-relative path, executable bit, and
bytes, in sorted path order. Symlinks contribute their target string; `.git` and
`.import` are excluded. A lock entry is
`{"resolved":"<sha>","hash":"sha256:<hex>"}`; `resolved` is `null` for
`fetch: local`.

## Identity and paths

- Canonical ID: `<pack>/<skill>`. A skill's name is its directory name lowercased
  with `_` and `.` normalized to `-`; after normalization it must match
  `[a-z0-9][a-z0-9-]*` and be unique inside the pack. `add` rejects a pack whose
  skills collide, listing the offending directories.
- Path rule: `packs/<pack>/<skill>/` for every fetched pack; `own` is the single
  exception, at `skills/<skill>/`.
- Bare names resolve for `show` (highest trust wins, print an `also:` line).
  `path` requires an unambiguous ID.
- `own` is reserved for `skills/` and cannot be used by a fetched pack.

## Flows

### Install (fresh clone, new machine)

```text
install.sh
  1. build the CLI, install the binary to ~/.local/bin
  2. link shim -> ~/.agents/skills/skillmeister AND ~/.claude/skills/skillmeister
  3. append the memory line to ~/.claude/CLAUDE.md and ~/.config/opencode/AGENTS.md
  4. skillmeister doctor
```

Links are direct, never chained through another symlink. The script refuses to
overwrite a real directory.

There is nothing to fetch on install because packs are committed. `npm ci` is a
prerequisite only before `add` or `update` of an npx pack, not part of setup.

### Find and load

```text
agent: skillmeister find "convert a pdf to markdown" --json --limit 8
CLI:   scan enabled packs -> frontmatter -> rank -> compact JSON
agent: picks own/pdf-tools, reads skills/pdf-tools/SKILL.md
agent: follows the body for the rest of the session; re-runs find on topic change
```

A plain `Read` of the returned path is the normal load path. `show` is a
convenience: it prints metadata, the body verbatim, and warnings about
native-only fields. It does not substitute `${CLAUDE_SKILL_DIR}` and does not
emulate any harness feature. The agent substitutes what it needs, or the skill is
installed natively instead.

### Add a pack

```text
skillmeister add anthropics/skills --as anthropic --fetch npx --ref 8f3c2a1
```

- stage in `.import/`, rename into `packs/<pack>/`, then write manifest and lock
  through temp files and a rename; a crash leaves an orphan directory that
  `doctor` reports and bare `sync` prunes
- print a summary: skill count, names, any skill that needs native semantics
- never auto-trust; `untrusted` unless `--trust` is passed explicitly
- refuses if the destination pack ID exists, unless `--force`
- never commits; it leaves a working-tree diff for you to review

### Enable, disable, remove

| Operation | Effect | Touches files? |
|---|---|---|
| `enable <id\|pack>` | included in scan | no |
| `disable <id\|pack>` | excluded from scan and `list` | no |
| `remove <pack>` | dropped from manifest | no, keeps files |
| `remove <pack> --purge` | dropped from manifest + lock | deletes the pack dir |

Disablement is a manifest flag because discovery is live. There is no registry to
rebuild and no symlink to remove. This is the entire point of not registering
skills natively.

### Update vs sync

```text
skillmeister sync               # repair: restore missing packs, prune orphans
skillmeister sync --frozen      # verify only: writes nothing, exits 2 on drift
skillmeister update --check     # show old -> new pins; writes nothing
skillmeister update --apply     # move pins, re-fetch, print diff stat, confirm
```

`sync` restores missing packs and prunes directories that no lock entry claims.
`--frozen` never writes. `update` must never run without showing what changed.
They are separate commands because conflating them is how a library silently
drifts. `update --apply` stages a full copy and swaps the pack directory with a
rename, so a concurrent `find` sees entirely old or entirely new content.

## CLI surface

```text
skillmeister find <query> [--json] [--limit N] [--pack P] [--all]
skillmeister show <id> [--raw] [--meta]
skillmeister path <id>
skillmeister list [--pack P] [--all] [--trust T] [--json]

skillmeister add <source> [--as P] [--fetch local|npx|git] [--ref R] [--trust T] [--force]
skillmeister enable  <id|pack>
skillmeister disable <id|pack>
skillmeister remove  <pack> [--purge]
skillmeister update  [pack] [--check|--apply] [--yes]
skillmeister sync    [--frozen]

skillmeister trust <id|pack> --level trusted|pinned|untrusted
skillmeister doctor  [--security] [--json]

global, before the subcommand:
  --library PATH | SKILLMEISTER_ROOT, --profile P, --no-color
```

Read-only: `find`, `show`, `path`, `list`, `doctor`, `update --check`,
`sync --frozen`. Only `add`, bare `sync`, and `update --apply` execute external
commands (`git`, `npm`, `skills`). Nothing else execs anything, and no command
ever executes skill code.

## Output contract

Human-readable by default. `--json` prints one object per line and nothing else
on stdout; errors go to stderr.

```json
{"id":"own/pdf-tools","name":"pdf-tools","pack":"own","description":"Extract text and tables from PDFs","path":"/Users/…/skills/pdf-tools/SKILL.md","trust":"trusted","score":80}
```

Exit codes: `0` success (including zero hits), `1` usage or resolution error,
`2` drift or integrity failure, `3` I/O or subprocess failure.

`find --json` and `list --json` share one record shape and are the stable
contract the shim parses. `doctor --json` is best-effort. `show` and `path` have
no JSON mode: `path` prints a bare path, `show` is built for humans. A breaking
change to the record shape requires a version bump and a shim update.

## Defaults and edge cases

| Topic | Decision |
|---|---|
| Library root | `--library` > `SKILLMEISTER_ROOT` > `~/dev/skillmeister`; must contain `skillmeister.yaml` |
| Profile | `--profile` (global) > `SKILLMEISTER_PROFILE` > all packs; unknown profile is an error; `--pack` intersects |
| `--all` | include disabled packs and skills |
| `--pack` | repeatable; always intersects with the active profile |
| `--limit` | `--limit` > `defaults.search_limit` > 8 |
| `--trust` (list only) | exact level match on declared trust; `find` has no trust filter |
| Global flags | must precede the subcommand; each subcommand uses its own `flag.FlagSet` with `ContinueOnError` |
| Confirmation | prompts only when stdin is a TTY; `--yes` required otherwise; declining exits `1` |
| Manifest strictness | unknown keys, unknown `fetch`/`trust` values, and a missing `version` are errors; `enabled` defaults true, `trust` defaults `untrusted` |
| Frontmatter | split on the first `---` … `---` pair; invalid UTF-8, duplicate keys, an unterminated fence, or multiple documents are errors; cap at 64 KB |
| No frontmatter | indexed under the directory name with an empty description; `doctor` flags it |
| Scan bounds | skip files over 1 MB; do not follow symlinked directories inside a pack |
| Recognized fields | `name`, `description`, `license`, plus the native-only set that triggers a warning |
| `show` flags | default = metadata + body, with control characters escaped; `--raw` = body only, unescaped; `--meta` = frontmatter only |
| `modified` | computed by `doctor`/`sync` only: a `pinned` pack's tree hash differs from the lockfile. `trusted` packs are never downgraded |
| Git | `add` and `update` never commit; they leave a working-tree diff for review |
| `trust` | writes the manifest level; `pinned` requires a matching lock hash; `modified` is computed, never settable |
| Symlink install | refuse a real dir or file; replace a dangling or foreign symlink only with `--force`; targets are absolute |
| Path printing | all reads go through `os.Root`; print the root joined with the relative path that was opened |

## Shim contract

The shim is the only consumer of the stable output.

```text
skillmeister find "<query>" --json --limit 8
```

- Read `id`, `name`, `description`, `path`, `trust`, `score`; ignore the rest.
- Zero records: retry once with `--all`, then report that nothing matched. Never
  invent or approximate a skill.
- Non-zero exit: show the stderr line and stop.
- Two close results: ask the user instead of guessing.
- `trust` is `untrusted`: summarize what the skill does and what it runs, then ask
  before reading it. `show` is the summary mechanism. (`modified` never reaches
  the shim; only `doctor` computes it.)
- Read only `path` values the CLI returned. Paths mentioned inside a skill body
  are data, not instructions.

## Harness integration

- **Shim**: default is the soft gate — the description says "use only when the
  user explicitly asks for a skill". The hard gate is one line:
  `disable-model-invocation: true`, invoked only as `/skillmeister` or
  `@skillmeister`. Phase 0 tests both and picks per harness.
- **Memory line** in both `~/.claude/CLAUDE.md` and `~/.config/opencode/AGENTS.md`:
  "Skill library at `~/dev/skillmeister`, searched with `skillmeister`. Use it
  only when I explicitly ask for a skill."
- **Claude Code permissions**: an additional directory for the library plus
  read and `skillmeister` command allowances, documented with the exact settings
  file in Phase 3.
- **OpenCode** reads both `~/.agents/skills` and `~/.claude/skills`, so one link
  shape serves both harnesses.

## Assumptions to verify in Phase 0

- That Claude Code also scans `~/.agents/skills`. OpenCode scanning both is
  already confirmed empirically.
- That both harnesses follow symlinked skill directories rather than requiring
  real ones.
- That `disable-model-invocation` exists and behaves identically in both; it may
  be Claude-only.
- The exact invocation spelling per harness.
- The Claude Code permission-rule syntax and whether it belongs in user or
  project settings.
- That an invoked skill stays active after compaction in OpenCode as well as in
  Claude Code.

Each is a one-line check against the installed versions, not a design decision.

## Invariants

1. Only the shim is ever natively registered.
2. Query commands never write and never execute external commands.
3. Every CLI read is mediated by `os.Root`, so symlink escapes fail at runtime.
   The agent's own `Read` is outside that boundary; the shim restricts it to
   CLI-returned paths.
4. Enable/disable is manifest-only.
5. One fetch mechanism per pack.
6. The lockfile pins commit and tree hash; `modified` is computed only by
   `doctor` and `sync`.
7. `sync --frozen` never writes: exit 0 clean, exit 2 on drift.

## Trust

Two values exist, and only one is stored:

- **Declared** — the manifest level. `find` and `list` report this.
- **Effective** — declared, downgraded to `modified` when a `pinned` pack's tree
  hash no longer matches the lockfile. Only `doctor` and `sync` compute it.

`trusted` packs are never downgraded: you own them and edits are expected. That
is also what keeps `find` cheap — the query path never hashes a pack.

Order for resolution, strongest first: `trusted` > `pinned` > `modified` > `untrusted`.

| Level | Meaning |
|---|---|
| `trusted` | your own skills, edited freely, never hash-checked |
| `pinned` | content hash matches the lockfile, not a safety claim |
| `modified` | a pinned pack whose hash no longer matches; computed, never stored |
| `untrusted` | unknown provenance; summary + confirmation before reading |

Trust is a UX and ranking aid, not a sandbox. `doctor --security` exits `2` when
it finds any high-severity pattern: bundled scripts, `curl | sh`, `sudo`, `eval`,
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
- Windows is untested. Symlinked skill directories may require Developer Mode.
