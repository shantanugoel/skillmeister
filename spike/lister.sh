#!/bin/sh
# Throwaway lister for the Phase 0 spike. Emits trust "trusted" and no "show".
# Usage: lister.sh [--library DIR] find [--json] [--limit N] [--] <query>
# Matches when every query word appears in the name or description.
lib="$(cd "$(dirname "$0")/lib" && pwd)"
if [ "$1" = "--library" ]; then lib="$2"; shift 2; fi
[ "$1" = "find" ] || { echo "spike lister: only find" >&2; exit 1; }
shift
while [ $# -gt 0 ]; do
  case "$1" in
    --json) shift ;;
    --limit) shift 2 ;;
    --) shift; break ;;
    *) break ;;
  esac
done
set -f
query=$(printf '%s' "$*" | tr 'A-Z' 'a-z')
for f in "$lib"/skills/*/SKILL.md "$lib"/packs/*/*/SKILL.md; do
  [ -f "$f" ] || continue
  dir=$(dirname "$f"); name=$(basename "$dir")
  case "$f" in "$lib"/skills/*) pack=own ;; *) pack=$(basename "$(dirname "$dir")") ;; esac
  desc=$(sed -n 's/^description: *//p' "$f" | head -1)
  hay=$(printf '%s %s' "$name" "$desc" | tr 'A-Z' 'a-z')
  ok=1
  for w in $query; do
    case "$hay" in *"$w"*) ;; *) ok=0 ;; esac
  done
  [ "$ok" = 1 ] && printf '{"id":"%s/%s","name":"%s","pack":"%s","description":"%s","path":"%s","trust":"trusted","score":40,"warnings":[]}\n' "$pack" "$name" "$name" "$pack" "$desc" "$f"
done
exit 0
