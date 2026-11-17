# Plan

Implementation plan for skillmeister v0.1. Go 1.27, standard library first, one
external dependency. Read [overview.md](overview.md) and
[architecture.md](architecture.md) first.

## Stack

| Piece | Choice | Why |
|---|---|---|
| Language | Go 1.27 | Fast iteration, boring code, single static binary |
| CLI parsing | stdlib `flag`, one `FlagSet` per subcommand, flags before positionals | `flag` stops at the first positional; the docs and the shim use that order |
| YAML reads | `github.com/goccy/go-yaml` | Active, field-level errors; frontmatter surface is small |
| YAML writes | edit a `yaml.Node` tree | Preserves comments and key order, so lifecycle diffs stay reviewable |
| Everything else | stdlib | `encoding/json`, `crypto/sha256`, `os/exec`, `testing` |
| Containment | `os.Root` + `Root.ReadDir` recursion | Escapes fail at runtime; `filepath.WalkDir` would bypass the guarantee |
| Version | `debug.ReadBuildInfo`, overridden by ldflags | One string for `--version` and `doctor` |
| Offline build | `go mod vendor`, `GOTOOLCHAIN=local` | Install works with no network and no toolchain download |

Module: `github.com/shantanugoel/skillmeister`. The offline install path is
`scripts/install.sh` from a clone. `go install
github.com/shantanugoel/skillmeister/cmd/skillmeister@latest` is a network
convenience for developers with a matching toolchain; it ignores `vendor/`.

```text
cmd/skillmeister/main.go
internal/cli/          # dispatch, flags, human/JSON output, exit codes
internal/manifest/     # parse + validate skillmeister.yaml, defaults, profiles
internal/library/      # discovery, IDs, containment via os.Root
internal/search/       # tokenizer, scoring, tie-breaks
internal/lock/         # lockfile read/write, content hashes, drift
internal/harness/      # install, shim rendering, doctor
```

Dependency budget: one. A second needs a reason in the PR.

## Phases

### Phase 0 — Shim spike (≈3h, decision gate)

Before writing real code, prove the premise.

- Skeleton repo, hand-written `skillmeister.yaml`, 3 skills (one own, two copied).
- A throwaway lister that emits `trust: trusted` and no `show`, so the spike
  measures routing, not trust handling.
- The real shim template, rendered with absolute binary and library paths.
- The Claude Code permission config the shim will ship with, so prompts do not
  pollute the measurement.
- Two variants: soft gate (description only) and hard gate
  (`disable-model-invocation: true`).

Protocol, per harness and per variant:

- **Soft gate:** 4 natural prompts ("find me a skill for X"), 2 explicit
  natural-language prompts, 2 controls with no skill intent. Pass: ≥5/6 requests
  reach for the shim, 0/2 controls trigger it.
- **Hard gate:** 2 literal `/skillmeister` invocations must work, and 0/4 natural
  prompts may trigger it. Pass: 2/2 literal, 0/4 natural.
- **Both:** the skill list shows exactly one entry (not two when both discovery
  paths exist), and one request after compaction still follows the loaded skill.

Choose the soft gate if it passes; the hard gate only if soft fails or strictness
is wanted. One variant wins for both harnesses.

**Fail:** wrap the same logic in a stdio MCP server (`search_skills`,
`read_skill`) and re-run the protocol. That is a separate half-day with its own
`cmd/skillmeister-mcp`; it is a Phase 0 outcome, never a second transport
alongside a working shim.

### Phase 1 — Read-only core (≈8h)

The product is search quality; build that before any lifecycle machinery.

- `manifest`: strict parse, defaults, validation, errors with file:line.
- `library`: scan enabled packs, immediate subdirectories only, skip unparseable
  skills with a stderr warning, canonical IDs, bare-name resolution, containment.
- Native-field detector: `allowed-tools`, `context: fork`, hooks, `!`,
  `${CLAUDE_SKILL_DIR}` feed the record's `warnings` array. It lands here because
  `show`, `add`, and the shim all surface warnings before Phase 4 exists.
- `search`: exact name > prefix > name tokens > description > body. Score is
  100 for an exact name, 80 prefix, 60 name tokens, 40 description, 20 body only;
  ties break `own` first, then trust order, then name. Exact name is
  `EqualFold` against the record name or full ID, before tokenization; tokenize by
  lowercasing and splitting on `[^a-z0-9]+`; every query token must appear in the
  winning tier's field; "prefix" means a token prefix. Body matching reads at most
  the first 8 KB.
- Profiles and `--pack` filtering (a predicate over the scan).
- `find`, `list`, `show` (including `--summary`), `path`; `--json` on `find` and
  `list` only.
- `testdata/gen`: materializes 300 synthetic skills with realistic bodies for the
  perf and golden tests; the same fixture seeds the 100-skill DoD library.
- CI from day one: `go vet`, `go test ./...`.
- Tests: golden JSON, a fuzz test for frontmatter (BOM, CRLF, 64 KB boundary),
  one escape test, and one test that runs the shim's exact command line.

**Acceptance:** 300 synthetic skills → `find` p95 under 100ms, correct top-3 on a
20-query fixture set. No writes. No `exec`.

### Phase 2 — Lifecycle (≈8h)

- `enable` / `disable` (pack and skill level), `trust` with the transition table:
  `pinned` verifies the current hash, `--accept` rewrites it, `trusted` on a
  remote pack warns.
- `lock`: hash recipe v1 with framed, tagged records; local packs have no entry.
- `doctor`: read-only drift report, orphan pack dirs and lock entries, missing or
  non-normalized skill directories, frontmatter flags.
- `add` in three adapters: `local`, `git`, then `npx` (run the `skills` CLI in
  `.import/`, merge its lock entries into the root `skills-lock.json`, move
  normalized skill dirs into `packs/<pack>/`). Tombstone swap; reject ID
  collisions.
- `remove` / `--purge`: quarantine to `.removed/<pack>/` versus delete.
- `sync` repair, then `sync --frozen` verify. Repair re-fetches, so it lands last.
- Manifest writes through `yaml.Node`, with a golden round-trip test.
- An exclusive writer lock at the library root; contention exits `3`.

**Acceptance:** `disable` then `find` excludes it with zero filesystem changes.
`remove` moves the pack to `.removed/` and leaves `doctor` clean; `--purge`
deletes exactly the pack dir. `doctor` catches a hand-edited pinned file and
reports `modified`; editing your own `trusted` skill does not. Two concurrent
writers cannot corrupt the manifest.

### Phase 3 — Install and harness (≈3h)

- `scripts/install.sh`: idempotent, refuses to clobber a real directory, checks
  Go ≥ 1.27 and sets `GOTOOLCHAIN=local`, builds from the vendored tree, installs
  the binary, renders the shim twice with absolute paths, appends memory lines
  once, then runs `doctor`.
- `doctor` grows the harness checks: rendered shim matches the template, memory
  lines present, binary resolves.
- Claude Code permissions block with the exact settings file, documented in the
  README.
- README, THIRD_PARTY_NOTICES.

**Acceptance:** fresh `HOME` + fresh clone → `install.sh` → both harnesses see
exactly one skill, `doctor` exits 0 with no network access, and one natural
request in each harness returns and reads a routed skill end to end.

### Phase 4 — Hardening (≈4h)

- `update --check` / `--apply` with a diff summary, `--yes`, and non-interactive
  behavior.
- `doctor --security` heuristics and its exit code.
- `staticcheck` and cross-builds for darwin/linux in CI.

### Phase 5 — Only if measured (no estimate)

Prebuilt index, session-start hook, a richer ranking model. Each needs a number
that justifies it.

## Risks

| Risk | Mitigation |
|---|---|
| Shim not reliably invoked | Phase 0 protocol; MCP fallback is a separate timebox |
| Ranking feels bad | Fixture query set + golden top-3 tests before tuning |
| `skills add` changes its layout or flags | Pin the version; one adapter file isolates the subprocess |
| Discovery differs per harness | `doctor` checks rendered shims and memory lines |
| Pack size bloats the repo | Warn at add over 25 MB; keep binary assets in their own pack. No gitignore option — offline-on-clone is a goal |
| Scope creep | No embeddings, GUI, daemon, or auto-update in v0.1 |

Estimates cover coding only. Phase 1 and Phase 2 carry most of the risk and are
likely to run closer to 2× if the adapters or the fixture set fight back.

## v0.1 definition of done

- Fresh clone on a clean machine installs and works offline.
- One skill visible per harness, from a library of at least 100.
- One natural request per harness loads a routed skill end to end.
- Add, disable, remove, and update each work without editing agent config.
- `doctor` passes; a hand-edited pinned skill fails it.
- `find` p95 under 100ms at 300 skills.
