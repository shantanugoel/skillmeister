#!/bin/sh
# Phase 0 protocol for Claude Code. Usage: run_claude.sh soft|hard WORKDIR
# Renders the shim into WORKDIR/.claude/skills (project scope, so the user's
# home config is untouched), runs each prompt with `claude -p`, and reports
# whether the shim was reached (Skill tool call or lister Bash call).
variant="$1"; work="$2"
here="$(cd "$(dirname "$0")" && pwd)"
bin="$here/lister.sh"; root="$here/lib"
rm -rf "$work"; mkdir -p "$work/.claude/skills/skillmeister"
"$here/render_shim.sh" "$variant" "$bin" "$root" > "$work/.claude/skills/skillmeister/SKILL.md"
cd "$work" || exit 1
if [ "$variant" = hard ]; then
  prompts=$(printf '%s\n' \
    'literal|/skillmeister find a skill for extracting text from PDFs' \
    'literal|/skillmeister find a skill for writing commit messages' \
    'natural|Find me a skill for converting a PDF into Markdown.' \
    'natural|I need to write a good git commit message. Is there a skill in my library for that?' \
    'natural|Look in my skills for something that can turn a CSV into a chart.' \
    'natural|Search my skill library for anything about commit messages, then use it for my staged changes.')
else
  prompts=$(cat "$here/prompts.txt")
fi
printf '%s\n' "$prompts" | while IFS='|' read -r kind prompt; do
  out=$(claude -p "$prompt" --output-format stream-json --verbose \
    --model sonnet --max-turns 8 --no-session-persistence \
    --add-dir "$root" \
    --allowedTools "Bash($bin:*)" "Read($root/**)" 2>/dev/null </dev/null)
  reached=$(printf '%s\n' "$out" | jq -r 'select(.type=="assistant") | .message.content[]? | select(.type=="tool_use") |
     (if .name=="Skill" then "skill:"+(.input.skill//"") elif .name=="Bash" then "bash:"+(.input.command//"") else empty end)' 2>/dev/null |
     grep -E 'skill:skillmeister|lister.sh' | head -1)
  read_marker=$(printf '%s\n' "$out" | grep -c 'SPIKE-MARKER' )
  if [ -n "$reached" ]; then r=REACHED; else r=no; fi
  printf '%s\t%s\tmarker_lines=%s\t%s\n' "$kind" "$r" "$read_marker" "$prompt"
done
