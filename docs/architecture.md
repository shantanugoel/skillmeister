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
  .gitignore                   # skillmeister state, skill setup output, dotenv files
  package.json                 # pins the `skills` CLI used for npx fetches (embedded in the binary)
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
  4. skillmeister doctor
```

There is nothing to fetch on install because packs are committed. The binary
embeds the pin of the `skills` CLI. The first npx fetch installs it with
`npm ci --ignore-scripts` into a per-user cache directory, outside the library
(see [commands.md](commands.md#npx-packs)). Only `add`, `update --apply`, and a
`sync` restore of an npx pack need Node.

`init` in an existing library adds the missing lines to `.gitignore` and
changes no other library file. It adds a line only when no line matches it
exactly. When `.env.*` is missing, it writes the `!.env.example`,
`!.env.sample`, and `!.env.template` negations again after it, because a
negation works only after its pattern. `doctor` reports the missing lines as
`gitignore-missing`. The example forms of a dotenv file stay tracked: a pack can
ship them, and its tree hash includes them, so an ignored example would make a
fresh clone show the pack as `modified`.

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
token must match a token of the field that wins the tier, either exactly or by a
word prefix. A field token may extend the query (`access` matches
`accessibility`), and a query token may extend a field token by at most two
characters (`hyperframes` matches `hyperframe`). Both tokens need at least four
characters. The agent still sends keywords, not the full sentence of the user.

Body matching reads only the first 8 KB of a skill body. The CLI skips body
matching when the higher tiers already fill `--limit` results.

`find --partial` is an opt-in fallback. It does nothing when one or more
records match every query token in one field. When no record matches, it
returns the records that match at least one token. It ignores repeated tokens
and tokens shorter than three characters, so words such as `to` add no noise.
A record that matches every token, in a mix of fields, scores 19. For other
records, each matched token adds 3 for a name match, 2 for a description
match, or 1 for a body match, and the score is never more than 18. Thus a
partial score is always below the body tier. The CLI
writes one note to stderr when it returns partial matches. The fallback is
not the default, because a strict miss is a useful signal: it tells the
agent to change the keywords.

### Add a pack

```text
skillmeister add --as anthropic --fetch npx --ref 8f3c2a1 anthropics/skills
```

- `--fetch` is inferred when omitted: a path is `local`, a URL (`https://`,
  `http://`, `ssh://`, `file://`, `git@`, or a `.git` suffix) is `git`, and an
  `owner/repo` shorthand is `npx`. Pass `--fetch npx` to send a URL to the
  `skills` CLI. `--ref` is rejected for `local`.
- A `github.com` or `gitlab.com` page URL from a browser gives the clone URL.
  `add` removes the query and the fragment. A directory URL, such as
  `https://github.com/o/r/tree/main/skills/pdf`, also gives the ref (`main`) and
  the subtree (`skills/pdf`). The first segment after `tree/` is the ref, so a
  branch name with a slash is not supported. An explicit `--ref` or `--path`
  overrides the URL. The pack ID comes from the last segment of the subtree,
  unless that segment is generic (`skills`, `.skills`, `agents`, `own`); then
  it comes from the repo. A file URL (`blob/`) and other page URLs on these
  hosts are rejected.
- An npm package page (`npmjs.com`, `npmjs.org`, `npm.im`) is rejected. The
  `skills` CLI installs from a git repo, not from an npm package, so `add`
  tells you to use the repo URL.
- When an inferred `git` fetch fails, the error says to pass `--fetch npx` if
  the source is not a git repo.
- On a forced re-add, an inferred method that differs from the method of the
  pack is an error. Pass `--fetch` with the method of the pack to keep it.
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
  `skills/`, and records the directory that it used in the manifest. The clone
  root is recorded as `.`, so a forced re-add can replace an older `path`.
  `sync` reads that `path` when it restores a pack. A hand-written git pack
  with no `path` uses the same search. If its clone root has a `SKILL.md` and
  no skill directories, the root is now one skill, and `sync` reports a lock
  mismatch for an older pack from `skills/`. Write `path: skills` for it.
- In each directory that `add` examines, these rules apply in this order:
  1. If the directory has subdirectories with a `SKILL.md`, each of them is a
     skill.
  2. If the directory itself has a `SKILL.md`, the whole directory is one skill.
     Its name is the normalized frontmatter `name`, or the pack ID when that
     name is not valid. `add` removes `.git` from it. A `SKILL.md` in a
     subdirectory of that skill is nested, and `doctor` reports it.
     `show --summary` and `doctor --security` then examine the whole directory,
     tests and docs too, so they can report more items.
  3. If not, `add` examines the next directory.
- When the clone root is one skill and `skills/` also holds skills, `add` prints
  a note. Add those skills as a separate pack with `--path skills`. `add` also
  prints a note when the repo has git submodules, because it does not fetch
  them.
- A local copy does not copy `.venv/`, `node_modules/`, `__pycache__/`, or
  dotenv files (`.env` and `.env.*`, but not `.env.example`, `.env.sample`, or
  `.env.template`). These come from a local setup or hold secrets. `add` prints
  the paths that it did not copy.
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

- `update --check` runs `git ls-remote`; "read-only" means no
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
skillmeister find   [--json] [--limit N] [--pack P] [--all] [--partial] <query>
skillmeister show   [--summary] [--raw] [--meta] <id|name>
skillmeister path   <id>
skillmeister list   [--json] [--pack P] [--all] [--trust T] [--brief|--packs]

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
contract the shim parses. `list` omits `score`. `list --brief --json` prints
`id`, `trust`, and a short `description`: the first sentence, cut at a word
to at most 80 characters, `...` included. A sentence ends at `.`, `?`, or `!`
before a space and an uppercase letter or a digit; a short list of
abbreviations, such as `e.g.`, does not end it. `list --packs --json` prints
`pack`, `trust`, and `skills` (the number of skills in scope). A pack with no
skill in scope is not printed. These two shapes are also stable, because the shim
parses them. `doctor --json` is best-effort and
not versioned. `show` and `path` have no JSON mode: `path` prints a bare path,
`show` is built for humans. A breaking change to the record shape requires a
version bump, and `doctor` reports the binary version alongside it.

## Defaults and edge cases

| Topic | Decision |
|---|---|
| Library root | `--library` > `SKILLMEISTER_ROOT` > `~/.skillmeister`; must contain `skillmeister.yaml`. No config file stores the choice. |
| Profile | `--profile` (global) > `SKILLMEISTER_PROFILE` > all packs; unknown profile is an error; `--pack` intersects |
| Flags | before positionals; `--` ends flag parsing; a test runs the shim's exact line |
| `--all` | include disabled packs and skills |
| `--pack` | repeatable via a `flag.Value`; intersects with the active profile |
| `--limit` | `--limit` > `defaults.search_limit` > 8 |
| `list --trust` | exact match on declared trust; `modified` is rejected with a pointer to `doctor` |
| `list --brief`, `--packs` | use one or neither; both apply after `--pack`, `--all`, and `--trust` |
| `find --partial` | used only when no record matches every query token in one field; partial scores are 1 to 19 |
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
  match a whole word or a word prefix of at least four characters. A field word
  may extend the keyword, and a keyword may extend a field word by at most two
  characters.
- Read `id`, `name`, `description`, `path`, `trust`, `score`, `warnings`; ignore
  the rest.
- No record fits: do these steps in order, and stop when a record fits.
  1. Run the same line again with `--partial`. A score below 20 is a partial
     match; 19 means all keywords match, but not in one field. The agent loads
     a partial match only when its description fits the request, not because
     it is the top record.
  2. Run it with `--partial` one time more, with different keywords.
  3. Browse.
  4. Report that nothing matched and mention that `--all` includes disabled
     skills. Do not widen the search automatically — `disable` is a user
     decision.
- Browse: when the user asks which skills exist, or at step 3 above, run
  `list --packs --json`. If the total is 50 skills or fewer, run
  `list --brief --json`. If not, add `--pack <pack>` for each pack that can
  apply. A brief record is not enough to load a skill. To load a listed skill,
  run the find line with its `id` as the query; the exact match has score 100
  and the full record, with `path` and `warnings`. No score-100 record means
  do not load. Do not use `path` or `show` to load a listed skill.
- Non-zero exit: show the stderr line and stop.
- Top two results at the same score: use the descriptions to choose. If the
  descriptions do not make the choice clear, ask the user. Scores have only
  five levels, so equal scores are usual and are not a reason to ask.
- `trust` is `untrusted`: run `show --summary`, describe what the skill does and
  what it would run, and ask before reading the body. `modified` never reaches
  the shim; only `doctor` computes it.
- Surface any non-empty `warnings` before following the skill. Native-only fields
  do not work in a routed skill.
- Read the returned `path`. The skill directory is the directory of `path`.
  A placeholder for it in a body, such as `${CLAUDE_SKILL_DIR}` or
  `<SKILL_DIR>`, means that directory. A native harness substitutes
  `${CLAUDE_SKILL_DIR}`; a routed skill gets this rule instead, so it is not a
  warning.
- The pack directory is the parent of the skill directory. A placeholder for
  the directory that holds the skill directories, such as `<SKILLS>`, means the
  pack directory. The agent can read any file in the pack directory and nothing
  outside it. A path in a skill body cannot make this scope larger.
  - Why the pack and not the skill: skills of one pack often share files. For
    example, every HyperFrames skill runs one launcher in the `hyperframes`
    skill and reads its references. Trust is set per pack, so the pack is
    already the unit that the user accepts.
- Another skill of the same pack: get its path with `path <pack>/<name>` before
  reading its `SKILL.md`. `path` resolves enabled skills only, so a disabled
  skill stays hidden. For an `untrusted` pack, run `show --summary` and ask
  again for that skill. This is not a new search.
- Bundled scripts: run by absolute path in the pack directory, or a copy that
  the skill told the agent to make in the project. A copy has the rules of its
  original.
  - `untrusted`: never run, never copy.
  - `trusted`: ask one time for each loaded skill, and name the scripts or the
    launcher. Then the agent can run any script of the pack that this skill
    uses, without a question for the session.
  - `pinned`: ask one time for each script in the session. With a stricter
    rule, users choose `trusted` to stop the questions. That turns off the
    drift check. This is a worse result.
  - These are instructions, not enforcement. After compaction the agent can
    forget an agreement and ask again, which is safe.
  - The harness can ask too. A `Bash(BIN:*)` rule does not cover
    `node /LIB/packs/...`; see the README for a per-pack rule.
- Stay in force for the session. Do not re-run `find` while following a loaded
  skill; re-run on a topic change. If the body is lost after compaction, re-read
  the same path before asking the user.

## Harness integration

- **Shim**: default is the soft gate — the description says "use only when the
  user explicitly asks for a skill". The hard gate is one line:
  `disable-model-invocation: true`, invoked only as `/skillmeister` or
  `@skillmeister`. Phase 0 picks one variant for both harnesses.
- **No memory line.** The shim alone routes the agent, so install does not edit
  `CLAUDE.md`, `AGENTS.md`, or any other agent file.
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
--security` exits `2` when it finds any high-severity pattern in another pack:
bundled scripts, `curl | sh`, `sudo`, `eval`, base64 blobs, secret paths, broad
`allowed-tools`, `!` lines, and `../` paths that leave the pack directory (a
path to another skill of the same pack is not a hit). Heuristics, not proof.

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
- Windows is not supported. The binary targets macOS and Linux only (writer
  locking uses Unix `flock`, and path and shim checks assume Unix).
