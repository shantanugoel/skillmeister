# Plan

Implementation plan for skillmeister v0.1. Go 1.27, standard library first, one
external dependency. Read [overview.md](overview.md) and
[architecture.md](architecture.md) first.

## Stack

| Piece | Choice | Why |
|---|---|---|
| Language | Go 1.27 | Fast iteration, boring code, single static binary |
| CLI parsing | stdlib `flag`, one `FlagSet` per subcommand | 12 commands; cobra earns nothing here |
| YAML | `github.com/goccy/go-yaml` | Active, field-level errors; frontmatter surface is small |
| Everything else | stdlib | `encoding/json`, `crypto/sha256`, `os/exec`, `testing` |
| Containment | `os.Root` + `Root.ReadDir` recursion | Escapes fail at runtime; `filepath.WalkDir` would bypass the guarantee |
| Version | `debug.ReadBuildInfo`, overridden by ldflags | `go install` gets the module version for free |
| Offline build | `go mod vendor` | Install works with no network; one dependency, tiny vendor tree |

Module: `github.com/shantanugoel/skillmeister`. Install form is
`go install github.com/shantanugoel/skillmeister/cmd/skillmeister@latest` — the
root module has no `main` package, so `module@latest` alone builds nothing.

```text
cmd/skillmeister/main.go
internal/cli/          # dispatch, flags, human/JSON output, exit codes
internal/manifest/     # parse + validate skillmeister.yaml, defaults, profiles
internal/library/      # discovery, IDs, containment via os.Root
internal/search/       # tokenizer, scoring, tie-breaks
internal/lock/         # lockfile read/write, content hashes, drift
internal/harness/      # install, symlinks, memory lines, doctor
```

Dependency budget: one. A second needs a reason in the PR.

## Phases

### Phase 0 — Shim spike (≈3h, decision gate)

Before writing real code, prove the premise.

- Skeleton repo, hand-written `skillmeister.yaml`, 3 skills (one own, two copied).
- A throwaway 50-line lister so `find` has something to answer with.
- The real shim, both symlinks, both memory lines.
- Two shim variants: soft gate (description only) and hard gate
  (`disable-model-invocation: true`).

Protocol: per harness and per variant, one fresh session of 8 prompts — 4 natural
("find me a skill for X"), 2 explicit ("use skillmeister to find X"), 2 controls
with no skill intent. Then invoke once, compact, and try one more request.

**Pass:** ≥5/6 pre-compaction requests reach for the shim, the post-compaction
request also succeeds, and 0/2 controls trigger it. If both variants pass, keep
the soft gate.

**Fail:** wrap the same logic in a stdio MCP server (`search_skills`,
`read_skill`) and re-run the protocol. MCP is a Phase 0 outcome, not a later
feature; it never coexists with a working shim in v0.1.

### Phase 1 — Read-only core (≈8h)

The product is search quality; build that before any lifecycle machinery.

- `manifest`: strict parse, defaults, validation, errors with file:line.
- `library`: scan enabled packs, parse frontmatter, canonical IDs, bare-name
  resolution, containment.
- `search`: exact name > prefix > name tokens > description > body. Score is
  100 for an exact name, 80 prefix, 60 name tokens, 40 description, 20 body only;
  ties break `own` first, then trust order, then name. Tokenize by lowercasing and
  splitting on `[^a-z0-9]+`; every query token must appear in the winning tier's
  field; "prefix" means a token prefix.
- Profiles and `--pack` filtering (they are a one-line predicate over the scan).
- `find`, `list`, `show`, `path`; `--json` on `find` and `list` only.
- `testdata/gen`: a script that materializes 300 synthetic skills for the perf and
  golden tests; the same fixture seeds the 100-skill DoD library.
- Tests: golden JSON, fixture library in `t.TempDir()`, a fuzz test for
  frontmatter splitting, one escape test (symlink out of root is refused).

**Acceptance:** 300 synthetic skills → `find` p95 under 100ms, correct top-3 on a
20-query fixture set. No writes. No `exec`.

### Phase 2 — Lifecycle (≈8h)

- `enable` / `disable` (pack and skill level), `trust`.
- `lock`: resolved commit + tree hash; declared vs effective trust.
- `doctor`: read-only drift report, orphan pack dirs and lock entries, frontmatter
  flags. No `--frozen`; it never writes.
- `add` in three adapters: `local`, `git`, then `npx` (stage into `.import/`,
  rename into `packs/<pack>/`, write manifest and lock via temp+rename, derive
  entries from `skills-lock.json`). Reject ID collisions.
- `remove` / `--purge`.
- `sync` repair, then `sync --frozen` verify. Repair re-fetches, so it lands last.

Enable/disable/trust/lock define the state model that `add` writes into, so the
writer comes after them. `sync` comes after the adapters because repair needs them.

**Acceptance:** `disable` then `find` excludes it with zero filesystem changes.
`remove` leaves files; `--purge` deletes exactly the pack dir. `doctor` catches a
hand-edited pinned file and reports `modified`; editing your own `trusted` skill
does not.

### Phase 3 — Install and harness (≈3h)

- `scripts/install.sh`: idempotent, refuses to clobber a real directory, builds
  from the vendored tree and installs the binary, links the shim twice, appends
  memory lines once, then runs `doctor`.
- Claude Code permissions block with the exact settings file, documented in the
  README.
- README, THIRD_PARTY_NOTICES.

**Acceptance:** fresh `HOME` + fresh clone → `install.sh` → both harnesses see
exactly one skill, and `doctor` exits 0 with no network access.

### Phase 4 — Hardening (≈4h)

- `update --check` / `--apply` with a diff summary, `--yes`, and non-interactive
  behavior.
- `doctor --security` heuristics and the native-semantics warning
  (`allowed-tools`, `context: fork`, hooks, `!`, `${CLAUDE_SKILL_DIR}`).
- CI: `go vet`, `go test ./...`, `staticcheck`, build on darwin/linux.

### Phase 5 — Only if measured (no estimate)

Prebuilt index, session-start hook, a richer ranking model. Each needs a number
that justifies it.

## Risks

| Risk | Mitigation |
|---|---|
| Shim not reliably invoked | Phase 0 protocol; MCP fallback is a contained rewrite |
| Ranking feels bad | Fixture query set + golden top-3 tests before tuning |
| `skills experimental_install` changes | Pin the version; one adapter file isolates the subprocess |
| Symlink discovery differs per harness | `doctor` checks both paths and reports drift |
| Pack size bloats the repo | Warn at add over 25 MB; keep binary assets in their own pack. No gitignore option — offline-on-clone is a goal |
| Scope creep | No embeddings, GUI, daemon, or auto-update in v0.1 |

## v0.1 definition of done

- Fresh clone on a clean machine installs and works offline.
- One skill visible per harness, from a library of at least 100.
- Add, disable, remove, and update each work without editing agent config.
- `doctor` passes; a hand-edited pinned skill fails it.
- `find` p95 under 100ms at 300 skills.
