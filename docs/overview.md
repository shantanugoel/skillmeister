# What Skillmeister Is

Skillmeister is a local library of agent skills with exactly one router entry
point. You collect skills from anywhere into one git repo, and your agents see
only the router. Everything else is dormant text until the router finds it.

## The problem

- Every natively registered skill costs context in every session, whether or not
  it is relevant. A large collection makes the always-on list unusable.
- Skills come from incompatible places: hand-written directories, `npx skills`
  packages, git repos. Each has its own update story.
- Agents disagree about discovery paths. Claude Code reads `~/.claude/skills`;
  OpenCode reads `~/.agents/skills` and `~/.claude/skills`. Keeping them in sync
  by hand is how drift starts.
- There is no single place to see what you have, what is trusted, or what
  changed since you installed it.

## The idea

```text
        one registered skill                     the actual library
harness ─────────────────▶ shim ──▶ skillmeister CLI ──▶ packs/*/<skill>/SKILL.md
                          (native)      (search)            (dormant text)
```

The shim is a small `SKILL.md` that teaches the agent one thing: how to ask the
CLI for a skill and read the result. Loading a skill is a file read, not a
harness event. That is why one registered skill can front hundreds.

This is a **content router, not a plugin loader.** The harness never learns that
the other skills exist.

## Terms

| Term | Meaning |
|---|---|
| **library** | The whole git repo: manifest, packs, shim, CLI. |
| **pack** | One source of skills — what you would call a plugin. Has one fetch method. |
| **skill** | A directory with `SKILL.md` plus optional supporting files. |
| **shim** | The only natively registered skill. Routes to the CLI. |
| **profile** | A named subset of packs, for scoping a session or project. |
| **trust** | Provenance label: `trusted`, `pinned`, `modified`, `untrusted`. |

## What it is not

- Not a native skill loader. Routed skills do not get harness features.
- Not a sandbox or a security boundary. A malicious `SKILL.md` can still
  persuade an agent through instructions alone.
- Not a registry or marketplace. It consumes existing sources.
- Not a daemon, and not an embedding index. Discovery is a live directory scan.
- Not a replacement for tool-managed skills that an app installs and syncs
  itself.

## The tradeoff, stated plainly

A natively installed skill gets `allowed-tools`, `context: fork`, hooks, `!`
command injection, `${CLAUDE_SKILL_DIR}` substitution, and auto-triggering. A
routed skill gets none of that. It is a document the agent reads and follows.

That is acceptable for reference and workflow skills, which are most of them. For
an action skill that depends on native semantics, install it natively for that
project or session. Skillmeister should say so when it detects those fields
rather than pretend they work.

## Goals

1. One canonical, versioned library that survives a fresh clone and works offline.
2. One registered skill per agent, no matter how many skills the library holds.
3. Add, disable, remove, and update a pack without touching agent config.
4. See provenance and drift: what came from where, what changed, what is modified.
5. Stay portable across Claude Code, OpenCode, and anything else that reads
   `SKILL.md`.

## Non-goals

- Hiding skills that are already natively installed by another tool.
- Running untrusted skill code safely.
- Being a general package manager for non-skill artifacts.
- Multi-user or hosted operation.

## Success looks like

- The harness's skill list shows exactly one entry after a fresh install.
- "find me a skill for X" produces a real hit in under 100ms, from a library of
  hundreds.
- Adding a third-party pack is one command and one reviewed diff.
- `doctor` on a fresh clone on a new machine reports zero drift.
