# Skillmeister

Skillmeister is a local library of agent skills with one router entry point. You
collect skills in one git repo. Your agent sees one skill: the router. All other
skills are text files until the router finds one.

Read [docs/overview.md](docs/overview.md) for the idea and
[docs/architecture.md](docs/architecture.md) for the design.

## Install

### With Homebrew

```sh
brew tap shantanugoel/tap
brew install skillmeister
skillmeister init
```

`skillmeister init` does the setup. It is safe to run again.

1. It creates the library at `~/.skillmeister` (a manifest, a `skills/`
   directory, `.gitignore`, and `.gitattributes`). In an existing library, it
   changes one file only: it adds missing lines to `.gitignore`. It never
   removes or changes a line.
2. It writes the shim into `~/.agents/skills/skillmeister` and
   `~/.claude/skills/skillmeister`. It stops, before it writes anything, if one
   of them is a real directory that skillmeister did not create.

After `brew upgrade`, run `skillmeister init` again if `skillmeister doctor`
reports `shim-stale` or `gitignore-missing`. The new binary carries the new shim
text and the new ignore lines, and `init` writes them.

`init` changes nothing but the library and those two shim directories. It does
not edit `CLAUDE.md`, `AGENTS.md`, or any settings file.

Use `--library PATH` to put the library somewhere else, or `--no-shim` to make
the library without touching agent config. Skillmeister keeps no config file. To
use another library every time, set `SKILLMEISTER_ROOT` in your shell profile.
If you run a command before `init`, the error tells you to run it.

### From a release or a clone

Download a binary from the
[releases page](https://github.com/shantanugoel/skillmeister/releases), put it
in your `PATH`, and run `skillmeister init`.

To build from source, you need Go 1.27 or newer and git. You do not need a
network.

```sh
git clone git@github.com:shantanugoel/skillmeister.git
cd skillmeister
scripts/install.sh
```

The script builds the binary from `vendor/` into `~/.local/bin` (`BIN_DIR`
changes the place), then runs `skillmeister init` for this clone, so the clone
is your library. It stops before it writes anything if a shim path holds
something that is not a skillmeister shim.

Make sure the binary is in your `PATH`.

To add or update an npx pack, run `npm ci` in the library first. This installs
the pinned `skills` CLI.

## Claude Code permissions

Claude Code asks before it runs a command or reads a file outside the project.
Add these rules to `~/.claude/settings.json`. Replace `BIN` with the absolute
path of the binary and `LIB` with the absolute path of the library.

```json
{
  "permissions": {
    "allow": [
      "Bash(BIN:*)",
      "Read(/LIB/**)"
    ],
    "additionalDirectories": ["LIB"]
  }
}
```

An absolute path in a `Read` rule starts with two slashes, so `Read(/LIB/**)`
has the one slash that `LIB` brings and one more. For example, with the binary
at `/home/me/.local/bin/skillmeister` and the library at
`/home/me/.skillmeister`:

```json
{
  "permissions": {
    "allow": [
      "Bash(/home/me/.local/bin/skillmeister:*)",
      "Read(//home/me/.skillmeister/**)"
    ],
    "additionalDirectories": ["/home/me/.skillmeister"]
  }
}
```

The shim writes the binary path inside double quotes. The `Bash` rule matches
that form: the Phase 0 spike (see [docs/phase0.md](docs/phase0.md)) ran the
quoted command line with this rule and got no permission prompt. The spike
passed the rules as command line options, not through `settings.json`, so the
settings file itself is not tested.

Skillmeister does not change this file for you.

The shim lets the agent run the scripts of a `trusted` pack after one question
per skill. Claude Code can still ask before each command, because the `Bash`
rule above covers only the skillmeister binary. To stop these questions for a
pack that you trust, allow its launcher. For example, every HyperFrames skill
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

These rules are not tested. Make it as narrow as possible: a rule such as
`Bash(node:*)` lets the agent run any script.

A skill setup can put secrets in the library, for example an API key in a
`.env` file in the skill directory. The `Read` rule above lets the agent read
such a file with no prompt. To prevent this, add a deny rule:

```json
{
  "permissions": {
    "deny": [
      "Read(//home/me/.skillmeister/**/.env)",
      "Read(//home/me/.skillmeister/**/.env.*)"
    ]
  }
}
```

## Use

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
the default is `~/.skillmeister`),
`--profile P` (or `SKILLMEISTER_PROFILE`), `--no-color`.

Exit codes: `0` success, `1` usage error, `2` drift or integrity failure, `3`
I/O error, lock contention, or subprocess failure.

`find` searches the name, the description, and the first 8 KB of the body of
each skill. Every keyword must match in one of these fields. With `--partial`,
when no skill matches every keyword in one field, `find` returns the skills
that match some of the keywords. A partial match has a score below 20. A
score of 19 means that the skill matches all keywords, but not in one field.
Partial search ignores repeated keywords and keywords shorter than three
characters.

`list --packs` prints one line for each pack, with its number of skills in
scope. A pack with no skill in scope (for example, after `--trust`) is not
shown. `list --brief` prints one line for each skill, with the first sentence
of its description. A longer sentence is cut at a word, and `...` is added,
so the line has at most 80 characters after the trust. Use these options to
browse a large library.

`update --check` reads from the network (`git ls-remote`) and writes nothing.
`update --apply` shows a diff summary, asks for confirmation (or needs `--yes`
when stdin is not a terminal), and then swaps the pack. It also restores the
upstream content of a modified pinned pack.

`doctor` checks the library and also verifies the two shims under `$HOME`
(`~/.agents/skills/skillmeister/SKILL.md` and
`~/.claude/skills/skillmeister/SKILL.md`); missing or stale shims are errors
that make `doctor` exit `2`. Pass `HOME=""` to skip the two shim checks, or use
`sync --frozen` in CI for a read-only library integrity check.

`doctor --security` adds lexical heuristics: bundled scripts, `curl | sh`,
`sudo`, `eval`, base64 blobs, secret paths, broad `allowed-tools`, `!` lines,
and `../` paths that leave the pack directory. A hit in a pack that you declared
`trusted` is a warning. A hit in any other pack is high severity and makes the
command exit `2`. The rules are heuristics, not proof.

`add` accepts a source that is one skill: a repo or directory with `SKILL.md`
at its root. The whole directory becomes one skill. If the directory also has
skill subdirectories, these subdirectories are the skills instead. `--path D`
selects a subtree, and that subtree can also be one skill.

### Skills that need a setup

Some skills tell you to install dependencies or to write a secret into the
skill directory, for example `uv sync` (which makes `.venv/`) or an API key in
`.env`. The library `.gitignore` keeps these files out of git. These limits
apply:

- `update --apply` and `add --force` replace the whole pack directory. The
  `.venv/`, the `.env`, and other local files in it are deleted. Do the setup
  again after an update, and keep a copy of the secret in another place.
- The tree hash of a `pinned` pack includes every file, so a setup in the pack
  directory shows as `modified`. Use `trusted` for a pack that you set up in
  place, or do the setup outside the library.

`add`, `remove`, `update --apply`, and `sync` change files in the library. They never commit.
Review the diff with `git status`, then commit it yourself.

## Test

```sh
go vet ./... ./testdata/gen ./testdata/gen/cmd
go test ./...
```

`go run ./testdata/gen/cmd -n 100 -out DIR` writes a synthetic library of 100
skills.

## License notices

See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
