# Skillmeister

Skillmeister keeps all your agent skills in one local library. Your agent sees
only one skill: the router. The router finds the correct skill when the agent
needs it, so a large library does not fill the context of the agent.

Read [docs/overview.md](docs/overview.md) for the idea,
[docs/architecture.md](docs/architecture.md) for the design, and
[docs/commands.md](docs/commands.md) for the full command reference.

## Quick start

```sh
brew tap shantanugoel/tap
brew install skillmeister
skillmeister init
```

`init` makes the library at `~/.skillmeister` (a git repo) and installs the
router skill for Claude Code and other agents. It is safe to run again. After
`brew upgrade`, run `skillmeister doctor`. If it reports `shim-stale` or
`gitignore-missing`, run `skillmeister init` again.

Then add some skills and commit them:

```sh
skillmeister add https://github.com/obra/superpowers
cd ~/.skillmeister && git add -A && git commit -m "add superpowers skills"
```

Now ask your agent for a task, for example "help me debug this failing test". The
agent uses the router to find a skill and load it. Prefer to name the skill directly so it can search and use exactly the skill you want.

## Add skills

Give `add` a URL, a path, or an `owner/repo` shorthand. Skillmeister finds the
fetch method from the source.

```sh
# A git repo. Any https://, ssh://, or git@ URL is a git repo.
skillmeister add https://github.com/obra/superpowers

# One directory of a repo. Copy the URL from your browser.
# Skillmeister reads the branch (main) and the directory from the URL.
skillmeister add https://github.com/obra/superpowers/tree/main/skills/systematic-debugging

# A different repo, with a pack name that you choose.
skillmeister add --as vercel https://github.com/vercel-labs/agent-skills

# A tag or a commit.
skillmeister add --ref v1.2.0 git@github.com:acme/doc-skills.git

# A directory on your computer. You edit it in place.
skillmeister add ~/work/my-skills

# An owner/repo shorthand, through the `skills` CLI.
skillmeister add vercel-labs/agent-skills
```

The `owner/repo` shorthand needs Node.js 22.20 or newer. On first use,
skillmeister uses npm to install the pinned `skills` CLI into its cache
(`~/Library/Caches/skillmeister` on macOS, `~/.cache/skillmeister` on Linux).
The library gets no Node files, so `git status` shows no Node files after an
add. A URL needs only git.

New packs are `untrusted`. If you read a pack and trust it, tell skillmeister:

```sh
skillmeister trust --level trusted superpowers
```

Skillmeister does not commit. After `add`, `remove`, `update --apply`, or
`sync`, examine the change with `git status` and commit it yourself.

## Other common commands

```sh
skillmeister list --packs                       # show the packs and the number of skills
skillmeister find debugging                     # search for a skill
skillmeister show systematic-debugging          # show a skill
skillmeister disable superpowers/brainstorming  # hide one skill (or a full pack)
skillmeister update --check                     # look for upstream changes
skillmeister update --apply                     # get the upstream changes
skillmeister remove superpowers                 # remove a pack
skillmeister doctor                             # check the library and the router
```

## Claude Code permissions

Claude Code asks before it runs a command or reads a file outside the project.
To stop these questions for skillmeister, add these rules to
`~/.claude/settings.json`. Change the two paths to the paths on your computer:
`which skillmeister` gives the binary path.

```json
{
  "permissions": {
    "allow": [
      "Bash(/opt/homebrew/bin/skillmeister:*)",
      "Read(//Users/me/.skillmeister/**)"
    ],
    "deny": [
      "Read(//Users/me/.skillmeister/**/.env)",
      "Read(//Users/me/.skillmeister/**/.env.*)"
    ],
    "additionalDirectories": ["/Users/me/.skillmeister"]
  }
}
```

The `Read` path starts with two slashes. This is correct. The `deny` rules stop
the agent from reading secrets that a skill setup puts in the library. Remove
them if you do not need them.

Skillmeister does not change this file for you. For more about permissions, see
[docs/commands.md](docs/commands.md#claude-code-permission-details).

## Other install methods

**Release binary.** Download a binary from the
[releases page](https://github.com/shantanugoel/skillmeister/releases), put it
in your `PATH`, and run `skillmeister init`.

**From source.** You need Go 1.27 or newer and git. You do not need a network.

```sh
git clone git@github.com:shantanugoel/skillmeister.git
cd skillmeister
scripts/install.sh
```

The script builds the binary into `~/.local/bin` (set `BIN_DIR` to change it).
Then it runs `skillmeister init` for this clone, so the clone is your library.
It stops before it writes anything if a shim path holds something that is not a
skillmeister shim. Make sure that `~/.local/bin` is in your `PATH`.

**A different library.** Use `skillmeister --library PATH init`, or set
`SKILLMEISTER_ROOT` in your shell profile.

## Test

```sh
go vet ./... ./testdata/gen ./testdata/gen/cmd
go test ./...
```

`go run ./testdata/gen/cmd -n 100 -out DIR` writes a synthetic library of 100
skills.

## License notices

See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
