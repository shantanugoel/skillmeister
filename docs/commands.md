# Command reference

This page gives the full details of each command. For a short introduction,
read the [README](../README.md).

## Synopsis

Flags go before positional arguments. Use `--` to end the flags.

```text
skillmeister find   [--json] [--limit N] [--pack P] [--all] [--partial] <query>
skillmeister show   [--summary] [--raw] [--meta] <id|name>
skillmeister path   <id>
skillmeister list   [--json] [--pack P] [--all] [--trust T] [--brief|--packs]

skillmeister add    [--as P] [--fetch local|npx|git] [--ref R] [--path D] [--trust T] [--force] <source>
skillmeister enable  <pack|pack/skill>
skillmeister disable <pack|pack/skill>
skillmeister remove  [--purge] <pack>
skillmeister sync    [--frozen]
skillmeister update  [--check|--apply] [--yes] [pack]
skillmeister trust   --level trusted|pinned [--accept] [--yes] <pack>
skillmeister doctor  [--security] [--json]
skillmeister init    [--bin P] [--no-shim]
```

Global flags go before the command: `--library PATH` (or `SKILLMEISTER_ROOT`;
the default is `~/.skillmeister`), `--profile P` (or `SKILLMEISTER_PROFILE`),
`--no-color`.

Exit codes: `0` success, `1` usage error, `2` drift or integrity failure, `3`
I/O error, lock contention, or subprocess failure.

## init

`skillmeister init` is safe to run again.

1. It creates the library at `~/.skillmeister` (a manifest, a `skills/`
   directory, `.gitignore`, and `.gitattributes`). In an existing library, it
   changes one file only: it adds missing lines to `.gitignore`. It never
   removes or changes a line.
2. It writes the shim into `~/.agents/skills/skillmeister` and
   `~/.claude/skills/skillmeister`. It stops, before it writes anything, if one
   of them is a real directory that skillmeister did not create.

`init` changes nothing but the library and those two shim directories. It does
not edit `CLAUDE.md`, `AGENTS.md`, or any settings file.

Use `--library PATH` to put the library in a different location, or
`--no-shim` to make the library without a change to the agent config.
Skillmeister keeps no config file. To use a different library each time, set
`SKILLMEISTER_ROOT` in your shell profile. If you run a command before `init`,
the error tells you to run `init`.

After an upgrade, run `skillmeister init` again if `skillmeister doctor`
reports `shim-stale` or `gitignore-missing`. The new binary has the new shim
text and the new ignore lines, and `init` writes them.

## add

`add` accepts a source that has one or more skills.

- A path is a `local` pack. You own it, and skillmeister does not lock it.
- A URL is a `git` pack: `https://`, `http://`, `ssh://`, `file://`, `git@`,
  or a `.git` suffix.
- An `owner/repo` shorthand is an `npx` pack. The pinned `skills` CLI fetches
  it. See [npx packs](#npx-packs).

Use `--fetch` to override the inferred method. For example, `--fetch npx` sends
a URL to the `skills` CLI.

A `github.com` or `gitlab.com` URL from a browser can be a repo page or a
directory page. `add` removes the query and the fragment, for example
`?tab=readme-ov-file`. A directory URL, such as
`https://github.com/o/r/tree/main/skills/pdf`, gives the ref (`main`) and the
subtree (`skills/pdf`). An explicit `--ref` or `--path` overrides the URL. The
first segment after `tree/` is the ref, so a branch name with a slash is not
supported. For such a branch, use the repo URL with `--ref` and `--path`. A file
page (`blob/`) is not a valid source. Other hosts, such as GitHub Enterprise,
need the clone URL.

An npm package page (`npmjs.com`) is not a valid source. Use the Repository
link of the package page.

A source with `SKILL.md` at its root is one skill. If the directory also has
skill subdirectories, these subdirectories are the skills instead. `--path D`
selects a subtree, and that subtree can also be one skill.

The pack ID comes from the last segment of the repo URL or the path. For a
directory URL, it comes from the last segment of the subtree. If that segment is
a generic name such as `skills`, the ID comes from the repo. Use `--as` to set
the ID.

New packs are `untrusted`. Use `--trust` to set a different trust level.
`pinned` is not valid for a local pack.

`--force` replaces a pack that exists. The pack keeps its fetch method. If the
inferred method is different, `add` stops. Use `--fetch` with the method of the
pack to keep it, or remove the pack first to change the method.


## npx packs

An `npx` pack needs Node.js, at the version that the pinned `skills` CLI needs
(22.20 or newer for `skills` 1.9.0). The first install of the CLI also needs
npm. Commands that only read the
library, such as `find`, `show`, `list`, `doctor`, and `sync --frozen`, do not
need Node. Only `add`, `update --apply`, and a `sync` that restores an `npx`
pack run the CLI.

The binary holds the pin (`package.json` and `package-lock.json` of this repo).
On first use, it installs the CLI with `npm ci --ignore-scripts` into
`<cache>/skillmeister/skills-<version>-<hash>/`. `<cache>` is
`~/Library/Caches` on macOS, and `$XDG_CACHE_HOME` or `~/.cache` on Linux. Set
`SKILLMEISTER_CACHE_DIR` to use a different directory. A new pin uses a new
directory. Skillmeister does not remove old directories; you can remove them.
The library holds no Node files.

For a GitHub source, skillmeister finds the commit of the ref with
`git ls-remote` before the CLI runs. A GitHub source is an `owner/repo`
shorthand (also with a subpath), a `github:` source, a `github.com` URL, or a
`git@github.com:` URL. Then the CLI fetches that commit, so add, update, and
restore get the same files. Do not use a name that is both a branch and a tag.
An unknown ref fails. To pin a commit, give the full 40-character hash; a short
hash fails.

If `git ls-remote` cannot read the repo (for example, a private repo that only
the `gh` CLI can read), the CLI gets the ref as it is. There is one exception:
for a source with no ref from `vercel`, `vercel-labs`, `heygen-com`,
`remotion-dev`, or `zapier/connectors`, the add stops. For these sources the
CLI downloads a snapshot when it gets no commit, and a snapshot loses the
executable bit. When `GH_HOST` names a host that is not `github.com`, a
shorthand is a repo on that host, and skillmeister does not resolve it.

A source can name its ref: `o/r#v1`, `o/r@1.2.0`, or
`https://github.com/o/r/tree/v1/skills`. Do not also pass `--ref` for such a
source.

Skillmeister 0.5.0 and older did not resolve the commit. For an `npx` pack
that such a version added from one of these owners with no ref, the CLI
downloaded a snapshot. A restore of such a pack can fail, because the content
does not match the lock. To fix the lock, run the `add --force` command that
the error shows one time. `update --apply` does not fix it, because the commit
did not change.

## find

`find` searches the name, the description, and the first 8 KB of the body of
each skill. Each keyword must match in one of these fields.

With `--partial`, when no skill matches every keyword in one field, `find`
returns the skills that match some of the keywords. A partial match has a score
below 20. A score of 19 means that the skill matches all keywords, but not in
one field. Partial search ignores repeated keywords and keywords shorter than
three characters.

## list

`list --packs` prints one line for each pack, with its number of skills in
scope. A pack with no skill in scope (for example, after `--trust`) is not
shown.

`list --brief` prints one line for each skill, with the first sentence of its
description. A longer sentence is cut at a word, and `...` is added, so the
line has a maximum of 80 characters after the trust.

## update

`update --check` reads from the network (`git ls-remote`) and writes nothing.

`update --apply` shows a diff summary, asks for confirmation (or needs `--yes`
when stdin is not a terminal), and then swaps the pack. It also restores the
upstream content of a modified pinned pack.

## doctor

`doctor` checks the library and also verifies the two shims under `$HOME`
(`~/.agents/skills/skillmeister/SKILL.md` and
`~/.claude/skills/skillmeister/SKILL.md`). Missing or stale shims are errors
that make `doctor` exit `2`. Set `HOME=""` to skip the two shim checks, or use
`sync --frozen` in CI for a read-only library integrity check.

`doctor --security` adds lexical heuristics: bundled scripts, `curl | sh`,
`sudo`, `eval`, base64 blobs, secret paths, broad `allowed-tools`, `!` lines,
and `../` paths that leave the pack directory. A hit in a pack that you declared
`trusted` is a warning. A hit in a different pack is high severity and makes
the command exit `2`. The rules are heuristics, not proof.

## Skills that need a setup

Some skills tell you to install dependencies or to write a secret into the
skill directory, for example `uv sync` (which makes `.venv/`) or an API key in
`.env`. The library `.gitignore` keeps these files out of git. These limits
apply:

- `update --apply` and `add --force` replace the full pack directory. The
  `.venv/`, the `.env`, and other local files in it are deleted. Do the setup
  again after an update, and keep a copy of the secret in a different location.
- The tree hash of a `pinned` pack includes every file, so a setup in the pack
  directory shows as `modified`. Use `trusted` for a pack that you set up in
  place, or do the setup outside the library.

## Commits

`add`, `remove`, `update --apply`, and `sync` change files in the library. They
never commit. Examine the diff with `git status`, then commit it yourself.

## Claude Code permission details

The shim writes the binary path inside double quotes. The `Bash` rule in the
README matches that form: the Phase 0 spike (see [phase0.md](phase0.md)) ran
the quoted command line with this rule and got no permission prompt. The spike
gave the rules as command line options, not through `settings.json`, so the
settings file itself is not tested.

An absolute path in a `Read` rule starts with two slashes. Thus
`Read(//home/me/.skillmeister/**)` is correct.

The shim lets the agent run the scripts of a `trusted` pack after one question
for each skill. Claude Code can still ask before each command, because the
`Bash` rule covers only the skillmeister binary. To stop these questions for a
pack that you trust, allow its launcher. For example, each HyperFrames skill
runs one launcher:

```json
{
  "permissions": {
    "allow": [
      "Bash(node /home/me/.skillmeister/packs/hyperframes/hyperframes/scripts/plugin-cli.mjs:*)"
    ]
  }
}
```

These rules are not tested. Make each rule as narrow as possible: a rule such
as `Bash(node:*)` lets the agent run any script.
