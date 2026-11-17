#!/bin/sh
# Render the shim template. Usage: render_shim.sh soft|hard BIN ROOT > SKILL.md
variant="$1"; bin="$2"; root="$3"
esc() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }
bin=$(esc "$bin"); root=$(esc "$root")
here="$(cd "$(dirname "$0")/.." && pwd)"
sed -e "s|{{BIN}}|$bin|g" -e "s|{{ROOT}}|$root|g" "$here/internal/harness/SKILL.md.tmpl" |
  if [ "$variant" = hard ]; then
    sed -e 's|^name: skillmeister$|name: skillmeister\ndisable-model-invocation: true|'
  else cat; fi
