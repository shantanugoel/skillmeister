# Phase 0 Result: Shim Spike

This file records the shim spike. The spike tested one question: does an agent
use the shim when the user asks for a skill, and only then?

**Status: the soft gate is the provisional default.** It passes the routing
checks in Claude Code. Some checks of the plan are still open. See "Not
measured".

## Method

- Library: `spike/lib`. It has 3 skills: `own/pdf-tools`,
  `community/commit-writer`, and `community/csv-charts`.
- Lister: `spike/lister.sh`. It is a throwaway script. It always prints
  `trust: trusted` and has no `show` command. The test measures routing only.
- Shim: `shim/skillmeister/SKILL.md.tmpl`. `spike/render_shim.sh` renders it
  with absolute paths. This is the real template.
- Claude Code: `spike/run_claude.sh soft|hard WORKDIR OUTDIR`. It renders the
  shim into `WORKDIR/.claude/skills/` (project scope), runs each prompt with
  `claude -p`, and reads the tool calls from the stream.
  - "Reached" means one Skill call for `skillmeister`, or one Bash call that
    starts with the lister path.
  - A run without a successful final result event prints `ERROR`. `ERROR` is
    never a pass.
  - The tool calls of each run are in `spike/out/<variant>-<n>.txt`. The result
    tables are `spike/out/soft.tsv` and `spike/out/hard.tsv`.
- Permissions: the spike passes `--allowedTools` and `--add-dir` on the command
  line. It does not use the settings file that Phase 3 will ship.
- OpenCode: one manual run each for 1 natural prompt and 1 control prompt, with
  the shim in `.agents/skills/` (project scope). No script and no log exist for
  these runs.

## Results

| Harness | Variant | Test | Result | Pass rule | Pass |
|---|---|---|---|---|---|
| Claude Code | soft | 4 natural + 2 explicit prompts | 6/6 reached | at least 5/6 | yes |
| Claude Code | soft | 2 controls | 0/2 reached | 0/2 | yes |
| Claude Code | hard | 2 literal `/skillmeister` | 2/2 reached | 2/2 | yes |
| Claude Code | hard | 4 natural prompts | 0/4 reached | 0/4 | yes |
| OpenCode | soft | 1 natural prompt | reached, then read the skill | sample only | n/a |
| OpenCode | soft | 1 control | not reached | sample only | n/a |

In the hard-gate run, 3 of the 4 natural prompts read a `SKILL.md` file
directly. The spike passes `--add-dir` for the library, so the agent could read
the files without the shim. The shim was not reached in these runs, so they
count as passes for the gate. A real install does not give the agent this
access.

## Decision

Use the **soft gate** as the default. It passes the Claude Code routing checks,
and it lets the user ask in plain words. The hard gate stays available: add
`disable-model-invocation: true` to the shim front matter. The MCP fallback
stays unbuilt, because the soft gate passed in one harness. The decision is
provisional until the open checks below are done.

## Not measured

The spike did not measure these items. Phase 3 must check them with the real
install before the v0.1 definition of done is met.

- The plan requires, for each variant, exactly one list entry when both
  discovery paths exist. The spike used project scope with one path. Both user
  paths exist only after `install.sh` renders two shims.
- The spike did not test whether the loaded skill stays active after
  compaction.
- OpenCode has a sample of 2 runs, not the full protocol.
- The spike did not test whether Claude Code reads `~/.agents/skills`.
- The spike did not test whether OpenCode honors `disable-model-invocation`.
- The spike did not test the exact invocation spelling for each harness.
- The spike did not test the permission-rule syntax in a settings file, or
  whether it belongs in user or project settings.

## Phase 3 follow-up

After the install script existed, one natural request ran in each harness. The
runs used the real binary, the real rendered shim, and a generated library of
100 skills. They used project scope, not user scope.

- Claude Code: the agent called the skill, ran `find`, and read
  `own/pdf-tools`.
- OpenCode: the same three steps.

With both discovery paths present in the project (`.claude/skills` and
`.agents/skills`), each harness listed the skill once when asked.

The checks for the user-scope install (both discovery paths at once, behavior
after compaction) still need a person to run them on a real machine. The
install test checks that the script writes the same shim to both paths and that
`doctor` accepts it. It does not start an agent.
