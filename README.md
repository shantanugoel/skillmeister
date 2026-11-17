# Skillmeister

Skillmeister is a local library of agent skills with one router entry point. You
collect skills in one git repo. Your agent sees one skill: the router. All other
skills are text files until the router finds one.

Read [docs/overview.md](docs/overview.md) for the idea and
[docs/architecture.md](docs/architecture.md) for the design.

## Install

You need Go 1.27 or newer and git. You do not need a network.

```sh
git clone git@github.com:shantanugoel/skillmeister.git ~/dev/skillmeister
cd ~/dev/skillmeister
scripts/install.sh
```

The script is safe to run again. It does these steps:

1. It builds the binary from `vendor/` and puts it in `~/.local/bin`
   (`BIN_DIR` changes the place).
2. It renders the shim into `~/.agents/skills/skillmeister` and
   `~/.claude/skills/skillmeister`. It stops if one of them is a real directory
   that skillmeister did not create.
3. It adds one memory line to `~/.claude/CLAUDE.md` and
   `~/.config/opencode/AGENTS.md`.
4. It runs `skillmeister doctor`.

Make sure `~/.local/bin` is in your `PATH`.

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
`/home/me/dev/skillmeister`:

```json
{
  "permissions": {
    "allow": [
      "Bash(/home/me/.local/bin/skillmeister:*)",
      "Read(//home/me/dev/skillmeister/**)"
    ],
    "additionalDirectories": ["/home/me/dev/skillmeister"]
  }
}
```

The shim writes the binary path inside double quotes. The `Bash` rule matches
that form: the Phase 0 spike (see [docs/phase0.md](docs/phase0.md)) ran the
quoted command line with this rule and got no permission prompt. The spike
passed the rules as command line options, not through `settings.json`, so the
settings file itself is not tested.

Skillmeister does not change this file for you.

## Use

Flags go before positional arguments. Use `--` to end the flags.

```text
skillmeister find   [--json] [--limit N] [--pack P] [--all] <query>
skillmeister show   [--summary] [--raw] [--meta] <id|name>
skillmeister path   <id>
skillmeister list   [--json] [--pack P] [--all] [--trust T]

skillmeister add    [--as P] [--fetch local|npx|git] [--ref R] [--path D] [--trust T] [--force] <source>
skillmeister enable  <pack|pack/skill>
skillmeister disable <pack|pack/skill>
skillmeister remove  [--purge] <pack>
skillmeister sync    [--frozen]
skillmeister trust   --level trusted|pinned [--accept] [--yes] <pack>
skillmeister doctor  [--json]
```

Global flags go before the command: `--library PATH` (or `SKILLMEISTER_ROOT`),
`--profile P` (or `SKILLMEISTER_PROFILE`), `--no-color`.

Exit codes: `0` success, `1` usage error, `2` drift or integrity failure, `3`
I/O error, lock contention, or subprocess failure.

`add`, `remove`, and `sync` change files in the library. They never commit.
Review the diff with `git status`, then commit it yourself.

## Test

```sh
go vet ./... ./testdata/gen
go test ./...
```

`go run ./testdata/gen/cmd -n 100 -out DIR` writes a synthetic library of 100
skills.

## License notices

See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
