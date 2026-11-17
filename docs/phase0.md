# Phase 0 Result: Shim Spike

This file records the shim spike. The spike tested one question: does an agent
use the shim when the user asks for a skill, and only then?

## Method

- Library: `spike/lib`. It has 3 skills: `own/pdf-tools`,
  `community/commit-writer`, and `community/csv-charts`.
- Lister: `spike/lister.sh`. It is a throwaway script. It always prints
  `trust: trusted` and has no `show` command. The test measures routing only.
- Shim: `shim/skillmeister/SKILL.md.tmpl`, rendered with absolute paths by
  `spike/render_shim.sh`. This is the real template.
- Permissions: `--allowedTools "Bash(<lister>:*)" "Read(<library>/**)"` and
  `--add-dir <library>`. No permission prompt can change the result.
- Claude Code: `spike/run_claude.sh soft|hard WORKDIR`. It renders the shim into
  `WORKDIR/.claude/skills/` (project scope), runs each prompt with `claude -p`,
  and reads the tool calls from the stream. "Reached" means one Skill call for
  `skillmeister`, or one Bash call to the lister.
- OpenCode: one manual run for each of 1 natural prompt and 1 control prompt, with
  the shim in `.agents/skills/` (project scope).

## Results

| Harness | Variant | Test | Result | Pass rule | Pass |
|---|---|---|---|---|---|
| Claude Code | soft | 4 natural prompts | 4/4 reached | with the 2 explicit prompts: at least 5/6 | yes |
| Claude Code | soft | 2 explicit prompts | 2/2 reached | (same) | yes |
| Claude Code | soft | 2 controls | 0/2 reached | 0/2 | yes |
| Claude Code | hard | 2 literal `/skillmeister` | 2/2 reached | 2/2 | yes |
| Claude Code | hard | 4 natural prompts | 0/4 reached | 0/4 | yes |
| OpenCode | soft | 1 natural prompt | reached, then read the skill | n/a (sample) | n/a |
| OpenCode | soft | 1 control | not reached | n/a (sample) | n/a |

Both variants pass in Claude Code.

## Decision

Use the **soft gate**. It passes, and it lets the user ask in plain words. The
hard gate stays available: add `disable-model-invocation: true` to the shim
front matter. The MCP fallback is not needed.

## Not measured

The spike did not measure these items. Phase 3 must check them with the real
install before the v0.1 definition of done is met.

- Project scope was used, not user scope. The check that "both discovery paths
  list exactly one entry" is not done. Both paths exist only after
  `install.sh` renders two shims.
- Skill state after compaction was not tested.
- OpenCode has a sample of 2 runs, not the full protocol.
- Whether Claude Code reads `~/.agents/skills` is not tested.
- Whether OpenCode honors `disable-model-invocation` is not tested.
