#!/bin/sh
# Install skillmeister from this clone. Safe to run again. It needs no network.
#
#   scripts/install.sh
#
# Environment:
#   HOME          where the shims and memory lines go
#   BIN_DIR       where the binary goes (default: $HOME/.local/bin)
#
# Steps: check Go, build from vendor/, install the binary, render the shim into
# the two discovery paths, add the memory lines once, run doctor.
set -eu

die() { printf 'install: %s\n' "$*" >&2; exit 1; }

here=$(cd "$(dirname "$0")/.." && pwd -P)
[ -f "$here/skillmeister.yaml" ] || die "$here has no skillmeister.yaml; run this script from a skillmeister clone"
[ -n "${HOME:-}" ] || die "HOME is not set"
bin_dir=${BIN_DIR:-$HOME/.local/bin}
bin="$bin_dir/skillmeister"

# 1. Go 1.27 or newer. GOTOOLCHAIN=local stops any toolchain download.
command -v go >/dev/null 2>&1 || die "go is not installed (need Go 1.27 or newer)"
export GOTOOLCHAIN=local
gover=$(go env GOVERSION)
case "$gover" in
  go1.[0-9]|go1.[0-9].*|go1.[0-9]rc*|go1.1[0-9]|go1.1[0-9].*|go1.2[0-6]|go1.2[0-6].*|go1.2[0-6]rc*)
    die "Go 1.27 or newer is required (found $gover)" ;;
esac

# 2. Build from the vendored tree.
mkdir -p "$bin_dir"
tmp=$(mktemp "$bin_dir/.skillmeister.XXXXXX")
trap 'rm -f "$tmp"' EXIT
ver=$(git -C "$here" describe --tags --always --dirty 2>/dev/null || echo dev)
(cd "$here" && GOFLAGS=-mod=vendor go build -trimpath \
  -ldflags "-X github.com/shantanugoel/skillmeister/internal/version.Version=$ver" \
  -o "$tmp" ./cmd/skillmeister) || die "build failed"
chmod 755 "$tmp"
mv -f "$tmp" "$bin"
trap - EXIT
printf 'installed %s (%s)\n' "$bin" "$ver"

# 3. Render the shim into both discovery paths. Never replace a directory that
#    skillmeister did not create.
for dir in "$HOME/.agents/skills/skillmeister" "$HOME/.claude/skills/skillmeister"; do
  if [ -L "$dir" ]; then
    die "$dir is a symlink; remove it first"
  fi
  if [ -e "$dir" ]; then
    [ -d "$dir" ] && grep -q '^name: skillmeister$' "$dir/SKILL.md" 2>/dev/null ||
      die "$dir exists and is not a skillmeister shim; move it away first"
  fi
done
for dir in "$HOME/.agents/skills/skillmeister" "$HOME/.claude/skills/skillmeister"; do
  mkdir -p "$dir"
  out=$(mktemp "$dir/.SKILL.md.XXXXXX")
  "$bin" --library "$here" render-shim --bin "$bin" > "$out" || { rm -f "$out"; die "cannot render the shim"; }
  mv -f "$out" "$dir/SKILL.md"
  chmod 644 "$dir/SKILL.md"
  printf 'rendered %s\n' "$dir/SKILL.md"
done

# 4. Memory lines, once.
line=$("$bin" --library "$here" render-shim --memory)
for file in "$HOME/.claude/CLAUDE.md" "$HOME/.config/opencode/AGENTS.md"; do
  mkdir -p "$(dirname "$file")"
  if [ -f "$file" ] && grep -qxF "$line" "$file"; then
    printf 'memory line already in %s\n' "$file"
    continue
  fi
  # Start on a new line when the file does not end with one.
  if [ -s "$file" ] && [ "$(tail -c 1 "$file" | wc -l)" -eq 0 ]; then printf '\n' >> "$file"; fi
  printf '%s\n' "$line" >> "$file"
  printf 'added the memory line to %s\n' "$file"
done

# 5. Check the result.
"$bin" --library "$here" doctor
