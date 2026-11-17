#!/bin/sh
# Phase 0 protocol for Claude Code.
# Usage: run_claude.sh soft|hard WORKDIR OUTDIR
# Renders the shim into WORKDIR/.claude/skills (project scope, so the home
# config is untouched), runs each prompt with `claude -p`, and reports whether
# the shim was reached. "Reached" means a Skill call for `skillmeister`, or a
# Bash call that starts with the lister path. A run without a successful final
# result event prints ERROR. ERROR is never a pass.
# The tool calls of each run go to OUTDIR/<variant>-<n>.txt as evidence.
variant="$1"; work="$2"; outdir="$3"
case "$variant" in soft|hard) ;; *) echo "variant must be soft or hard" >&2; exit 1 ;; esac
case "$work" in ""|/|"$HOME") echo "unsafe WORKDIR" >&2; exit 1 ;; esac
[ -n "$outdir" ] || { echo "OUTDIR is required" >&2; exit 1; }
mkdir -p "$outdir" && outdir=$(cd "$outdir" && pwd)
here="$(cd "$(dirname "$0")" && pwd)"
bin="$here/lister.sh"; root="$here/lib"
rm -rf "$work"; mkdir -p "$work/.claude/skills/skillmeister" "$outdir"
"$here/render_shim.sh" "$variant" "$bin" "$root" > "$work/.claude/skills/skillmeister/SKILL.md"
cd "$work" || exit 1
if [ "$variant" = hard ]; then
  # Hard gate: 2 literal invocations, then the natural prompts of prompts.txt.
  prompts=$(printf '%s\n' \
    'literal|/skillmeister find a skill for extracting text from PDFs' \
    'literal|/skillmeister find a skill for writing commit messages'
    grep '^natural|' "$here/prompts.txt")
else
  prompts=$(cat "$here/prompts.txt")
fi
n=0
printf '%s\n' "$prompts" | while IFS='|' read -r kind prompt; do
  n=$((n + 1))
  # The Read rule uses // for an absolute path, as Claude Code requires.
  out=$(claude -p "$prompt" --output-format stream-json --verbose \
    --model sonnet --max-turns 8 --no-session-persistence \
    --add-dir "$root" \
    --allowedTools "Bash($bin:*)" "Read(/$root/**)" 2>/dev/null </dev/null)
  calls=$(printf '%s\n' "$out" | jq -r 'select(.type=="assistant") | .message.content[]? | select(.type=="tool_use") |
     (if .name=="Skill" then "skill:"+(.input.skill//"") elif .name=="Bash" then "bash:"+(.input.command//"") else "tool:"+.name+" "+((.input.file_path//.input.path//"")|tostring) end)' 2>/dev/null)
  printf '%s\n' "$calls" > "$outdir/$variant-$n.txt"
  ok=$(printf '%s\n' "$out" | jq -r 'select(.type=="result") | (.is_error|not)' 2>/dev/null | tail -1)
  reached=$(printf '%s\n' "$calls" | grep -E "^skill:skillmeister\$|^bash:\"?$bin\"? " | head -1)
  # A read of a skill file that did not go through the shim.
  stray=$(printf '%s\n' "$calls" | grep -c -E '^tool:Read .*SKILL\.md')
  if [ "$ok" != true ]; then r=ERROR
  elif [ -n "$reached" ]; then r=REACHED
  else r=no; fi
  printf '%s\t%s\tdirect_skill_reads=%s\t%s\n' "$kind" "$r" "$stray" "$prompt"
done
